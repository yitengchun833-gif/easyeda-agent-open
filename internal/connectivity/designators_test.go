package connectivity

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func numberedRepairFixture() (Document, map[string]string) {
	d := Document{SchemaVersion: "1.4", ProjectID: "project", DocumentID: "page", Nets: []Net{{ID: "stable-net", Name: "GND", Scope: "global", Role: "ground"}}, Issues: []Issue{{Code: "electrical-review", Severity: "warning", ComponentID: "stable-2", PinNumber: "1", Message: "preserve this actual circuit finding"}, {Code: nonstandardDesignatorIssue, Severity: "warning", ComponentID: "stale", Message: "stale"}}}
	refs := []string{"U1", "U_RF", "U_TALK", "J_AUDIO_MOD", "P_PWR", "c0001", "C_DECOUPLE", "C10", "U00000000000000000000000000999999999999999999999999"}
	devices := []string{"logic", "logic", "logic", "connector", "power", "cap", "cap", "cap", "logic"}
	for i, ref := range refs {
		id := "stable-" + string(rune('1'+i))
		c := Component{ID: id, Ref: ref, Device: Device{LibraryUUID: "official", UUID: devices[i], Name: "device"}, Footprint: "footprint", Pins: []Pin{{Number: "1", Name: "GND", Type: "power", X: float64(i), Y: 2}, {Number: "2", Name: "NC", NoConnected: true, X: 3, Y: 4}}, Placement: &Placement{X: float64(i), Y: 20, Rotation: 90, Mirror: true, BBox: &BBox{MinX: 1, MinY: 2, MaxX: 3, MaxY: 4}}, PageID: "page", PageName: "page-name"}
		if ref == "U_RF" {
			c.Role = "RF transceiver"
		}
		d.Components = append(d.Components, c)
		d.Connections = append(d.Connections, Connection{ComponentID: id, PinNumber: "1", NetID: "stable-net", Kind: "pin_net"})
	}
	d.Modules = []Module{{ID: "module", Name: "Functional module", CoreComponents: []string{"stable-1"}, PeripheralComponents: []string{"stable-2", "stable-3"}, InternalNets: []string{"stable-net"}, Ports: []Port{{ID: "port", Name: "GND", NetID: "stable-net", PinRefs: []string{"stable-1.1"}}}}}
	return d, map[string]string{"official/logic": "U?", "official/connector": "CN", "official/power": "P?", "official/cap": "C"}
}

func TestAllocateDesignatorsUsesOfficialPrefixesAndPreservesElectricalIR(t *testing.T) {
	in, prefixes := numberedRepairFixture()
	before, _ := json.Marshal(in)
	out, changes, err := AllocateDesignators(in, prefixes)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"U1", "U2", "U3", "CN1", "P1", "c0001", "C2", "C10", in.Components[8].Ref}
	for i, c := range out.Components {
		if c.Ref != want[i] || c.ID != in.Components[i].ID {
			t.Fatalf("component order/identity or allocated ref differs: %+v", c)
		}
		role := in.Components[i].Role
		if role == "" && c.Ref != in.Components[i].Ref {
			role = in.Components[i].Ref
		}
		if c.Role != role {
			t.Fatalf("functional role lost on %s: %q", c.Ref, c.Role)
		}
		unchanged := c
		unchanged.Ref, unchanged.Role = in.Components[i].Ref, in.Components[i].Role
		if !reflect.DeepEqual(unchanged, in.Components[i]) {
			t.Fatalf("allocation changed device/pins/NC/placement on %s", c.ID)
		}
	}
	if len(changes) != 5 || changes[0] != (DesignatorChange{ComponentID: "stable-2", Before: "U_RF", After: "U2"}) || changes[2].After != "CN1" {
		t.Fatalf("unexpected ordered change mapping: %+v", changes)
	}
	if !reflect.DeepEqual(out.Nets, in.Nets) || !reflect.DeepEqual(out.Connections, in.Connections) || !reflect.DeepEqual(out.Modules, in.Modules) {
		t.Fatal("allocation changed stable nets/connections/module bindings")
	}
	if len(out.Issues) != 1 || out.Issues[0] != in.Issues[0] {
		t.Fatalf("stale designator issue was not refreshed, or electrical finding changed: %+v", out.Issues)
	}
	if err := ValidatePlacementDesignators(out); err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("allocation or placement validation mutated the input")
	}
	// Second application is idempotent and needs no prefix lookups at all.
	second, noChanges, err := AllocateDesignators(out, nil)
	if err != nil || len(noChanges) != 0 || !reflect.DeepEqual(second, out) {
		t.Fatalf("second allocation was not a no-op: %+v %v", noChanges, err)
	}
}

func TestAllocateDesignatorsDeepCopiesNestedData(t *testing.T) {
	in, prefixes := numberedRepairFixture()
	before, _ := json.Marshal(in)
	out, _, err := AllocateDesignators(in, prefixes)
	if err != nil {
		t.Fatal(err)
	}
	out.Components[0].Pins[0].Name = "changed"
	out.Components[0].Placement.X++
	out.Components[0].Placement.BBox.MinX++
	out.Nets[0].Name = "changed"
	out.Connections[0].NetID = "changed"
	out.Issues[0].Message = "changed"
	out.Modules[0].CoreComponents[0] = "changed"
	out.Modules[0].PeripheralComponents[0] = "changed"
	out.Modules[0].InternalNets[0] = "changed"
	out.Modules[0].Ports[0].Name = "changed"
	out.Modules[0].Ports[0].PinRefs[0] = "changed"
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("output shares mutable IR data with the input")
	}
}

func TestAllocateDesignatorsRefusesMissingOrGuessedPrefix(t *testing.T) {
	for _, prefix := range []string{"", "U1", "U_RF", "?", "U??", "1"} {
		in, prefixes := numberedRepairFixture()
		prefixes["official/logic"] = prefix
		before, _ := json.Marshal(in)
		if _, changes, err := AllocateDesignators(in, prefixes); err == nil || changes != nil {
			t.Fatalf("invalid prefix %q was guessed or partially accepted", prefix)
		}
		after, _ := json.Marshal(in)
		if string(before) != string(after) {
			t.Fatal("rejected allocation changed input")
		}
	}
	in, prefixes := numberedRepairFixture()
	delete(prefixes, "official/logic")
	if _, _, err := AllocateDesignators(in, prefixes); err == nil {
		t.Fatal("missing official prefix guessed from U_RF")
	}
	in.Components[1].ID = in.Components[0].ID
	if _, _, err := AllocateDesignators(in, prefixes); err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("duplicate stable ID accepted: %v", err)
	}
}

func TestLegacyDesignatorsRemainReadableButCannotBePlaced(t *testing.T) {
	d := Document{SchemaVersion: "1.4", Components: []Component{{ID: "radio", Ref: "U_RF", Pins: []Pin{{Number: "1", NoConnected: true}}}, {ID: "unassigned", Ref: "CN?", Pins: []Pin{{Number: "1", NoConnected: true}}}}}
	if err := d.Validate(); err != nil {
		t.Fatal("legacy snapshot must remain readable", err)
	}
	first := append([]Issue(nil), d.Issues...)
	if len(first) != 2 || first[0].Code != nonstandardDesignatorIssue || first[0].Severity != "warning" {
		t.Fatalf("missing nonstandard warnings: %+v", first)
	}
	if err := d.Validate(); err != nil || !reflect.DeepEqual(first, d.Issues) {
		t.Fatalf("designator findings are not idempotent: %+v %v", d.Issues, err)
	}
	before, _ := json.Marshal(d)
	if err := ValidatePlacementDesignators(d); err == nil {
		t.Fatal("legacy functionality-as-ref can be replayed without repair")
	}
	after, _ := json.Marshal(d)
	if string(before) != string(after) {
		t.Fatal("placement validation mutated source")
	}
	d.Components[0].Ref, d.Components[1].Ref = "u001", "CN2"
	if err := d.Validate(); err != nil || len(d.Issues) != 0 {
		t.Fatalf("repaired designators kept old warnings: %+v %v", d.Issues, err)
	}
}

func TestV4CustomDesignatorPolicyPreservesAndValidatesReferences(t *testing.T) {
	d := Document{
		SchemaVersion:    "1.4",
		DesignatorPolicy: &DesignatorPolicy{Mode: "custom", Pattern: `^CTRL-[A-Z]+-[0-9]{3}$`},
		Components:       []Component{{ID: "controller", Ref: "CTRL-MAIN-001", Pins: []Pin{{Number: "1", NoConnected: true}}}},
	}
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(d.Issues) != 0 {
		t.Fatalf("valid custom designator was diagnosed as nonstandard: %+v", d.Issues)
	}
	if err := ValidatePlacementDesignators(d); err != nil {
		t.Fatal(err)
	}
	out, changes, err := AllocateDesignators(d, map[string]string{"unused": "U"})
	if err != nil || len(changes) != 0 || out.Components[0].Ref != "CTRL-MAIN-001" {
		t.Fatalf("custom designator was changed or rejected: %+v %+v %v", out, changes, err)
	}
	out.DesignatorPolicy.Pattern = "changed"
	if d.DesignatorPolicy.Pattern == "changed" {
		t.Fatal("custom designator policy pointer was not cloned")
	}
}

func TestV4CustomDesignatorPolicyFailsClosed(t *testing.T) {
	for _, policy := range []*DesignatorPolicy{
		{Mode: "custom", Pattern: "CTRL-.*"},
		{Mode: "custom", Pattern: "^[$"},
		{Mode: "future", Pattern: "^.*$"},
	} {
		d := Document{SchemaVersion: "1.4", DesignatorPolicy: policy, Components: []Component{{ID: "c", Ref: "CTRL-1", Pins: []Pin{{Number: "1", NoConnected: true}}}}}
		if err := d.Validate(); err == nil {
			t.Fatalf("invalid policy accepted: %+v", policy)
		}
	}
	d := Document{SchemaVersion: "1.4", DesignatorPolicy: &DesignatorPolicy{Mode: "custom", Pattern: `^CTRL-[0-9]+$`}, Components: []Component{{ID: "c", Ref: "U1", Pins: []Pin{{Number: "1", NoConnected: true}}}}}
	if err := ValidatePlacementDesignators(d); err == nil || !strings.Contains(err.Error(), "custom") {
		t.Fatalf("mismatched custom reference was not refused: %v", err)
	}
	if _, _, err := AllocateDesignators(d, nil); err == nil || !strings.Contains(err.Error(), "preserved") {
		t.Fatalf("allocator guessed a V4 custom sequence: %v", err)
	}
}

func boundComponent(ref string, properties map[string]any) map[string]any {
	return map[string]any{"componentType": "part", "designator": ref, "otherProperty": properties, "pins": []any{map[string]any{"pinNumber": "1", "net": "GND"}, map[string]any{"pinNumber": "2", "net": "", "noConnected": true}}}
}

func TestFromReadStableComponentBindingSurvivesRename(t *testing.T) {
	properties := map[string]any{ComponentIDProperty: "radio-component", ComponentRoleProperty: "RF transceiver"}
	before, err := FromRead(map[string]any{"components": []any{boundComponent("U_RF", properties)}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := FromRead(map[string]any{"components": []any{boundComponent("U2", properties)}})
	if err != nil {
		t.Fatal(err)
	}
	if before.Components[0].ID != "radio-component" || after.Components[0].ID != before.Components[0].ID || after.Components[0].Role != "RF transceiver" {
		t.Fatalf("bound identity/role drifted: %+v %+v", before.Components, after.Components)
	}
	if !reflect.DeepEqual(before.Connections, after.Connections) || !reflect.DeepEqual(before.Nets, after.Nets) || !reflect.DeepEqual(before.Components[0].Pins, after.Components[0].Pins) {
		t.Fatal("renaming rebound pin/net identity")
	}
	if diff := Compare(before, after); !reflect.DeepEqual(diff, Diff{}) {
		t.Fatalf("rename fabricated electrical differences: %+v", diff)
	}
	legacy, err := FromRead(map[string]any{"components": []any{boundComponent("J_AUDIO_MOD", nil)}})
	if err != nil || legacy.Components[0].ID != "cmp-J_AUDIO_MOD" {
		t.Fatalf("unbound legacy ID fallback changed: %+v %v", legacy, err)
	}
}

func TestFromReadRejectsInvalidAndDuplicateBindings(t *testing.T) {
	for _, key := range []string{ComponentIDProperty, ComponentRoleProperty} {
		for _, value := range []any{"", " \t", nil, 42, true, []any{"id"}} {
			if _, err := FromRead(map[string]any{"components": []any{boundComponent("U1", map[string]any{key: value})}}); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("explicit invalid binding %s=%v was silently discarded: %v", key, value, err)
			}
		}
	}
	if _, err := FromRead(map[string]any{"components": []any{boundComponent("U1", map[string]any{ComponentIDProperty: "same"}), boundComponent("U2", map[string]any{ComponentIDProperty: "same"})}}); err == nil || !strings.Contains(err.Error(), "duplicate component") {
		t.Fatalf("duplicate stable binding accepted: %v", err)
	}
}

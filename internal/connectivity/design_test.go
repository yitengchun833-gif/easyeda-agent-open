package connectivity

import (
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
)

func designFixture() Document {
	d := Document{SchemaVersion: "1.4", ProjectID: "project", DocumentID: "page", Nets: []Net{{ID: "n-power", Name: "VCC", Scope: "global", Role: "power"}, {ID: "n-ground", Name: "GND", Scope: "global", Role: "ground"}}, Connections: []Connection{}, Components: []Component{}}
	for _, id := range []string{"a", "b"} {
		d.Components = append(d.Components, Component{ID: id, Ref: "U0" + id, Role: "controller", Device: Device{LibraryUUID: "library", UUID: strings.Repeat("a", 32), Name: "device"}, Footprint: "SOIC", PageID: "page", PageName: "POWER", Placement: &Placement{X: 10, Y: 20, Rotation: 90, Mirror: true, BBox: &BBox{MinX: 5, MinY: 15, MaxX: 15, MaxY: 25}}, Pins: []Pin{{Number: "1", Name: "VIN", Type: "power", X: 5, Y: 20}, {Number: "2", Name: "GND", X: 10, Y: 15}, {Number: "3", Name: "NC", NoConnected: true, X: 15, Y: 20}}})
		d.Connections = append(d.Connections, Connection{ComponentID: id, PinNumber: "1", NetID: "n-power", Kind: "pin_net"}, Connection{ComponentID: id, PinNumber: "2", NetID: "n-ground", Kind: "pin_net"})
		d.Modules = append(d.Modules, Module{ID: "module-" + id, Name: "module " + id, CoreComponents: []string{id}, InternalNets: []string{"n-ground", "n-power"}, Ports: []Port{{ID: "port-vcc", Name: "VCC", NetID: "n-power", PinRefs: []string{id + ".1"}}}})
	}
	return d
}
func designRaw(t *testing.T, d Document) []byte {
	t.Helper()
	b, e := json.Marshal(d)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func designEvidence(t *testing.T, d Document) DesignEvidence {
	t.Helper()
	e, err := DecodeDesignEvidence(designRaw(t, d))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDesignDiffPreservesIdentityAndEveryModeledDomain(t *testing.T) {
	tests := []struct {
		name, path string
		change     func(*Document)
	}{
		{"ref", "/components/a/ref", func(d *Document) { d.Components[0].Ref = "u001" }},
		{"role", "/components/a/role", func(d *Document) { d.Components[0].Role = "analog" }},
		{"library", "/components/a/device/libraryUuid", func(d *Document) { d.Components[0].Device.LibraryUUID = "another" }},
		{"uuid", "/components/a/device/deviceUuid", func(d *Document) { d.Components[0].Device.UUID = strings.Repeat("b", 32) }},
		{"device name", "/components/a/device/name", func(d *Document) { d.Components[0].Device.Name = "different" }},
		{"footprint", "/components/a/footprint", func(d *Document) { d.Components[0].Footprint = "DIP" }},
		{"page name", "/components/a/pageName", func(d *Document) { d.Components[0].PageName = "PWR" }},
		{"x", "/components/a/placement/x", func(d *Document) { d.Components[0].Placement.X = 0 }},
		{"y", "/components/a/placement/y", func(d *Document) { d.Components[0].Placement.Y = 25 }},
		{"rotation", "/components/a/placement/rotation", func(d *Document) { d.Components[0].Placement.Rotation = 45 }},
		{"mirror", "/components/a/placement/mirror", func(d *Document) { d.Components[0].Placement.Mirror = false }},
		{"bbox", "/components/a/placement/bbox/minX", func(d *Document) { d.Components[0].Placement.BBox.MinX = 4 }},
		{"pin name", "/components/a/pins/1/name", func(d *Document) { d.Components[0].Pins[0].Name = "IN" }},
		{"pin type", "/components/a/pins/1/type", func(d *Document) { d.Components[0].Pins[0].Type = "input" }},
		{"pin x", "/components/a/pins/1/x", func(d *Document) { d.Components[0].Pins[0].X = 4 }},
		{"pin y", "/components/a/pins/1/y", func(d *Document) { d.Components[0].Pins[0].Y = 21 }},
		{"nc", "/components/a/pins/3/noConnected", func(d *Document) {
			d.Components[0].Pins[2].NoConnected = false
			d.Connections = append(d.Connections, Connection{ComponentID: "a", PinNumber: "3", NetID: "n-ground"})
		}},
		{"net name", "/nets/n-power/name", func(d *Document) { d.Nets[0].Name = "+3V3" }},
		{"net scope", "/nets/n-power/scope", func(d *Document) { d.Nets[0].Scope = "local" }},
		{"net role", "/nets/n-power/role", func(d *Document) { d.Nets[0].Role = "analog" }},
		{"connection kind", "/connections/[\"a\",\"1\"]/kind", func(d *Document) { d.Connections[0].Kind = "netlist" }},
		{"module name", "/modules/module-a/name", func(d *Document) { d.Modules[0].Name = "A" }},
		{"port name", "/modules/module-a/ports/port-vcc/name", func(d *Document) { d.Modules[0].Ports[0].Name = "SUPPLY" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := designFixture()
			b := cloneDesignatorDocument(a)
			tc.change(&b)
			before := designRaw(t, a)
			diff, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
			if err != nil {
				t.Fatal(err)
			}
			if diff.Status != "different" || diff.ExpectedRevision == diff.ActualRevision {
				t.Fatalf("unexpected result %+v", diff)
			}
			found := false
			for _, c := range diff.Changes {
				if c.Path == tc.path {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing %s in %+v", tc.path, diff.Changes)
			}
			if string(before) != string(designRaw(t, a)) {
				t.Fatal("comparison mutated caller")
			}
		})
	}
}

func TestDesignDiffInventoryOrderAndDerivedIssuesIgnored(t *testing.T) {
	a := designFixture()
	b := cloneDesignatorDocument(a)
	b.Components[0], b.Components[1] = b.Components[1], b.Components[0]
	for i := range b.Components {
		p := b.Components[i].Pins
		p[0], p[2] = p[2], p[0]
	}
	b.Nets[0], b.Nets[1] = b.Nets[1], b.Nets[0]
	b.Connections[0], b.Connections[3] = b.Connections[3], b.Connections[0]
	b.Modules[0].InternalNets[0], b.Modules[0].InternalNets[1] = b.Modules[0].InternalNets[1], b.Modules[0].InternalNets[0]
	b.Issues = []Issue{{Code: "some-diagnostic", Severity: "warning", Message: "ignored"}}
	diff, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if diff.Status != "synced" || len(diff.Changes) != 0 || diff.ExpectedRevision != diff.ActualRevision {
		t.Fatalf("ordering false positive %+v", diff)
	}
	b.Modules[0], b.Modules[1] = b.Modules[1], b.Modules[0]
	diff, err = CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Path != "/moduleOrder" {
		t.Fatalf("module authored order must differ %+v", diff)
	}
	if diff.Coverage.Scope != "canonical" || diff.Coverage.DrawingCompared || len(diff.Coverage.Unverified) == 0 {
		t.Fatal("canonical equality claimed drawing coverage")
	}
}

func TestDesignDiffStableIDsDistinguishReplacementAndEscapePaths(t *testing.T) {
	a := designFixture()
	b := cloneDesignatorDocument(a)
	b.Components[0].ID = "id/with~separator"
	for i := range b.Connections {
		if b.Connections[i].ComponentID == "a" {
			b.Connections[i].ComponentID = b.Components[0].ID
		}
	}
	b.Modules[0].CoreComponents[0] = b.Components[0].ID
	b.Modules[0].Ports[0].PinRefs[0] = b.Components[0].ID + ".1"
	diff, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, c := range diff.Changes {
		seen[c.Path] = true
	}
	if !seen["/components/a"] || !seen["/components/id~1with~0separator"] {
		t.Fatalf("replacement must use stable IDs: %+v", diff)
	}
}

func TestDesignDiffUnknownGeometryNeverBecomesZero(t *testing.T) {
	raw := designRaw(t, designFixture())
	var a map[string]any
	_ = json.Unmarshal(raw, &a)
	c := a["components"].([]any)[0].(map[string]any)
	p := c["pins"].([]any)[0].(map[string]any)
	delete(p, "x")
	missing, _ := json.Marshal(a)
	e, err := DecodeDesignEvidence(missing)
	if err != nil {
		t.Fatal(err)
	}
	p["x"] = float64(0)
	zero, _ := json.Marshal(a)
	z, err := DecodeDesignEvidence(zero)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := CompareDesignEvidence(e, z)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Status != "incomplete" || diff.Coverage.CanonicalComplete || diff.ExpectedRevision == diff.ActualRevision {
		t.Fatalf("missing coordinate treated as measured zero %+v", diff)
	}
	if len(diff.Changes) != 1 || diff.Changes[0].Before != nil || diff.Changes[0].After != float64(0) {
		t.Fatalf("presence loss %+v", diff.Changes)
	}
	same, err := CompareDesignEvidence(e, e)
	if err != nil || same.Status != "incomplete" {
		t.Fatalf("missing both sides claimed sync %+v %v", same, err)
	}
	delete(c, "placement")
	withoutPlacement, _ := json.Marshal(a)
	e, err = DecodeDesignEvidence(withoutPlacement)
	if err != nil {
		t.Fatal(err)
	}
	same, err = CompareDesignEvidence(e, e)
	if err != nil || same.Status != "incomplete" {
		t.Fatalf("missing placement claimed sync %+v %v", same, err)
	}
}

func TestDesignDiffOptionalDefaultsAndEmptyDocument(t *testing.T) {
	d := Document{SchemaVersion: "1.4", ProjectID: "project", DocumentID: "page", Components: []Component{}, Nets: []Net{}, Connections: []Connection{}}
	e := designEvidence(t, d)
	diff, err := CompareDesignEvidence(e, e)
	if err != nil || diff.Status != "synced" {
		t.Fatalf("explicit empty inventories must be valid %+v %v", diff, err)
	}
	raw := designRaw(t, designFixture())
	a, err := DecodeDesignEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"noConnected":true`, `"noConnected":true,"type":"","name":"NC"`, 1))
	if _, err := DecodeDesignEvidence(raw); err == nil {
		t.Fatal("duplicate name should reject")
	}
	d = designFixture()
	d.Components[0].Placement.Rotation = 0
	d.Components[0].Placement.Mirror = false
	raw = designRaw(t, d)
	a, err = DecodeDesignEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"placement":{"x":10`, `"placement":{"rotation":0,"mirror":false,"x":10`, 1))
	b, err := DecodeDesignEvidence(raw)
	if err != nil {
		t.Fatal(err)
	}
	diff, err = CompareDesignEvidence(a, b)
	if err != nil || diff.Status != "synced" {
		t.Fatalf("optional orientation defaults differ %+v %v", diff, err)
	}
}

func TestDesignDecodeRejectsMalformedAndIncompleteInput(t *testing.T) {
	base := string(designRaw(t, designFixture()))
	cases := map[string]string{
		"duplicate":    strings.Replace(base, `"ref":"U0a"`, `"ref":"U0a","ref":"U1"`, 1),
		"alias":        strings.Replace(base, `"ref":"U0a"`, `"ref":"U0a","Ref":"U1"`, 1),
		"single alias": strings.Replace(base, `"projectId"`, `"ProjectId"`, 1),
		"unknown":      strings.Replace(base, `"projectId"`, `"projectIDTypo"`, 1),
		"trailing":     base + ` {}`,
		"overflow":     strings.Replace(base, `"x":10`, `"x":1e999`, 1),
		"null number":  strings.Replace(base, `"x":10`, `"x":null`, 1),
		"null array":   strings.Replace(base, `"components":[`, `"components":null,"discarded":[`, 1),
		"bad uuid":     strings.Replace(base, strings.Repeat("a", 32), strings.Repeat("z", 32), 1),
		"scope":        strings.Replace(base, `"scope":"global"`, `"scope":"globla"`, 1),
		"kind":         strings.Replace(base, `"kind":"pin_net"`, `"kind":"pin_not"`, 1),
		"rotation":     strings.Replace(base, `"rotation":90`, `"rotation":"45"`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := DecodeDesignEvidence([]byte(raw)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	d := designFixture()
	d.Connections = d.Connections[1:]
	_, err := DecodeDesignEvidence(designRaw(t, d))
	var incomplete *IncompleteDesignError
	if !errors.As(err, &incomplete) {
		t.Fatalf("unassigned pin should be incomplete: %v", err)
	}
	d = designFixture()
	d.Components[0].Pins[0].NoConnected = true
	if _, err := DecodeDesignEvidence(designRaw(t, d)); err == nil {
		t.Fatal("NC and connected accepted")
	}
	d = designFixture()
	d.Modules[0].PeripheralComponents = []string{"a"}
	if _, err := DecodeDesignEvidence(designRaw(t, d)); err == nil {
		t.Fatal("same member in core/peripheral accepted")
	}
	d = designFixture()
	d.Modules[0].Ports[0].PinRefs = []string{"a.2"}
	if _, err := DecodeDesignEvidence(designRaw(t, d)); err == nil {
		t.Fatal("port references other net")
	}
	d = designFixture()
	d.Components[0].Placement.X = math.Inf(1)
	if _, err := DesignRevision(d); err == nil {
		t.Fatal("nonfinite accepted by direct API")
	}
}

func TestDesignDiffWrongTargetAndRepeatability(t *testing.T) {
	a := designFixture()
	b := cloneDesignatorDocument(a)
	b.ProjectID = "other"
	diff, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil || diff.Status != "wrong-target" {
		t.Fatalf("wrong target lost %+v %v", diff, err)
	}
	again, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil || !reflect.DeepEqual(diff, again) {
		t.Fatal("not deterministic")
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Minimal P1-shaped regression: interface/protection plus a second supply
// branch attached through a common rail. A U ref intentionally names a 2-pin
// terminal; reference prefixes must never become component classification.
func zoneReviewFixture() SchematicZonesInput {
	component := func(id, ref string, nets ...string) SchematicLayoutComponent {
		c := standaloneLayoutFixture().Components[1]
		c.ID, c.Measurement.Designator = id, ref
		c.Measurement.Pins = nil
		for i, net := range nets {
			c.Measurement.Pins = append(c.Measurement.Pins, SchematicPin{Number: fmt.Sprint(i + 1), Net: net, X: 280, Y: float64(90 + i*5)})
		}
		return c
	}
	return SchematicZonesInput{SchemaVersion: 1,
		Components: []SchematicLayoutComponent{
			component("a", "D1", "usb", "vbus", "ground", "usb"),
			component("b", "USBC1", "usb", "vbus", "ground", "cc"),
			component("c", "D2", "vbus", "supply"),
			component("d", "D3", "external", "supply"),
			component("e", "U3", "external", "ground"),
			component("f", "C8", "supply", "ground"),
		},
		NetPolicies: map[string]string{"usb": "module_port", "vbus": "direct", "ground": "local_ground", "cc": "direct", "supply": "local_power", "external": "direct"},
		Zones:       []SchematicZone{{ID: "entry", Title: "Entry", CoreComponentID: "a", ComponentIDs: []string{"a", "b", "c", "d", "e", "f"}}},
		Attachments: []SchematicLayoutPeripheral{{ComponentID: "d", PinNumber: "2", AttachTo: &SchematicLayoutAttach{ComponentID: "c", PinNumber: "2"}}},
	}
}

func mustZoneReview(t *testing.T, in SchematicZonesInput) *SchematicZoneReview {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	r, err := reviewSchematicZonesJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if r.SourceSHA256 != sha256Hex(raw) {
		t.Fatal("lost source hash")
	}
	return r
}

func TestZoneReviewP1EvidenceAndNoMutation(t *testing.T) {
	in := zoneReviewFixture()
	before, _ := json.Marshal(in)
	r := mustZoneReview(t, in)
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(r, mustZoneReview(t, in)) {
		t.Fatal("mutated input or unstable report")
	}
	if r.Status != "review-required" || len(r.Findings) != 3 {
		t.Fatalf("missing rules: %+v", r)
	}
	for _, f := range r.Findings {
		ids := []string{}
		for _, c := range f.Components {
			ids = append(ids, c.ID)
		}
		switch f.Rule {
		case "multiple-multipin-members":
			if !reflect.DeepEqual(ids, []string{"a", "b"}) {
				t.Fatal("used U3 prefix as core evidence", ids)
			}
		case "non-rail-subgraph-detached":
			if !reflect.DeepEqual(ids, []string{"d", "e"}) || len(f.Nets) != 1 || f.Nets[0].Name != "external" || len(f.Nets[0].Pins) != 2 {
				t.Fatalf("lost branch witnesses: %+v", f)
			}
		case "rail-only-attachment":
			if !reflect.DeepEqual(ids, []string{"d", "c"}) || len(f.Nets) != 1 || f.Nets[0].Policy != "local_power" {
				t.Fatalf("lost rail evidence: %+v", f)
			}
		default:
			t.Fatal(f.Rule)
		}
	}
}

func TestZoneReviewAdvisoryDoesNotInventSplits(t *testing.T) {
	in := zoneReviewFixture()
	// A declared non-rail path connects the previously detached branch. The
	// physical circuit is unchanged; only the advisory policy input changes.
	in.NetPolicies["supply"] = "direct"
	r := mustZoneReview(t, in)
	if len(r.Findings) != 1 || r.Findings[0].Rule != "multiple-multipin-members" {
		t.Fatalf("unexpected findings: %+v", r)
	}
	// Ordinary isolated decouplers are not proposed as detached subcircuits.
	r = mustZoneReview(t, zonesFixture())
	if r.Status != "no-findings" || len(r.Limitations) == 0 {
		t.Fatal("claimed semantic approval", r)
	}
	// An implicit host hint can connect through another peripheral. It must not
	// be converted to a fabricated explicit attachment to the core.
	in = zoneReviewFixture()
	in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "e", PinNumber: "1"}}
	r = mustZoneReview(t, in)
	for _, f := range r.Findings {
		if f.Rule == "rail-only-attachment" {
			t.Fatal("invented an implicit host", f)
		}
	}
}

func TestZoneReviewRailRuleUsesExplicitEndpointPins(t *testing.T) {
	in := zoneReviewFixture()
	// D3 and D2 now also share a non-rail net on other pins. Their explicit
	// attachment still names pin 2 to pin 2 (+5V), so the rail warning remains.
	in.Components[2].Measurement.Pins[0].Net = "also-shared"
	in.Components[3].Measurement.Pins[0].Net = "also-shared"
	in.NetPolicies["also-shared"] = "direct"
	r := mustZoneReview(t, in)
	found := false
	for _, f := range r.Findings {
		if f.Rule == "rail-only-attachment" {
			found = true
			if len(f.Nets) != 1 || f.Nets[0].Name != "supply" {
				t.Fatalf("used unrelated shared net: %+v", f)
			}
		}
	}
	if !found {
		t.Fatal("explicit rail endpoint warning disappeared")
	}
}

func TestZoneReviewRenameReorderAndGeometryInvariants(t *testing.T) {
	in := zoneReviewFixture()
	before := mustZoneReview(t, in)
	idMap := map[string]string{}
	for i, c := range in.Components {
		idMap[c.ID] = fmt.Sprintf("opaque-%d", len(in.Components)-i)
	}
	for i := range in.Components {
		c := &in.Components[i]
		c.ID = idMap[c.ID]
		c.Measurement.Designator = "RENAMED" + c.ID
		c.Measurement.X += 600
		c.Measurement.Y -= 300
		c.Measurement.Rotation = 90
		c.Measurement.Mirror = true
		for j := range c.Measurement.Pins {
			p := &c.Measurement.Pins[j]
			p.Net = "renamed-" + p.Net
			p.X, p.Y = p.Y+600, p.X-300
		}
	}
	policies := map[string]string{}
	for n, p := range in.NetPolicies {
		policies["renamed-"+n] = p
	}
	in.NetPolicies = policies
	for i := range in.Zones {
		z := &in.Zones[i]
		z.CoreComponentID = idMap[z.CoreComponentID]
		for j, id := range z.ComponentIDs {
			z.ComponentIDs[j] = idMap[id]
		}
	}
	for i := range in.Attachments {
		a := &in.Attachments[i]
		a.ComponentID = idMap[a.ComponentID]
		a.AttachTo.ComponentID = idMap[a.AttachTo.ComponentID]
	}
	for i, j := 0, len(in.Components)-1; i < j; i, j = i+1, j-1 {
		in.Components[i], in.Components[j] = in.Components[j], in.Components[i]
	}
	after := mustZoneReview(t, in)
	if len(before.Findings) != len(after.Findings) {
		t.Fatal("geometry/name-sensitive rules")
	}
	for i, f := range before.Findings {
		other := after.Findings[i]
		if f.Rule != other.Rule || len(f.Components) != len(other.Components) {
			t.Fatal("changed rules")
		}
		got := map[string]bool{}
		for _, c := range other.Components {
			got[c.ID] = true
		}
		for _, c := range f.Components {
			if !got[idMap[c.ID]] {
				t.Fatal("changed affected members")
			}
		}
	}
}

func TestZoneReviewRejectsInvalidEvidence(t *testing.T) {
	for _, edit := range []func(*SchematicZonesInput){
		func(in *SchematicZonesInput) { in.Zones[0].ComponentIDs = append(in.Zones[0].ComponentIDs, "a") },
		func(in *SchematicZonesInput) { in.Zones[0].ComponentIDs = in.Zones[0].ComponentIDs[:5] },
		func(in *SchematicZonesInput) { in.Attachments[0].AttachTo.PinNumber = "missing" },
		func(in *SchematicZonesInput) { in.Attachments[0].PinNumber = "1" },
		func(in *SchematicZonesInput) { in.Components[0].Measurement.Pins[0].Net = "" },
		func(in *SchematicZonesInput) { in.Components[0].Measurement.Pins[1].Number = "1" },
		func(in *SchematicZonesInput) {
			in.Attachments = append(in.Attachments, SchematicLayoutPeripheral{ComponentID: "c", AttachTo: &SchematicLayoutAttach{ComponentID: "d", PinNumber: "2"}})
		},
	} {
		in := zoneReviewFixture()
		edit(&in)
		raw, _ := json.Marshal(in)
		r, err := reviewSchematicZonesJSON(raw)
		if err == nil || r.Status != "invalid" || len(r.Findings) != 0 {
			t.Fatalf("invalid evidence accepted: %+v %v", r, err)
		}
	}
}

func TestZoneReviewCLIAndAutomaticFailedSolveReport(t *testing.T) {
	dir := t.TempDir()
	from, report, out := filepath.Join(dir, "source.json"), filepath.Join(dir, "report.json"), filepath.Join(dir, "layout.json")
	in := zonesFixture()
	in.Attachments = []SchematicLayoutPeripheral{{ComponentID: "peripheral", PinNumber: "1", AttachTo: &SchematicLayoutAttach{ComponentID: "anchor", PinNumber: "1"}}}
	for _, budget := range []int{0, 1} {
		in.MaxCandidates = budget
		raw, _ := json.Marshal(in)
		if err := os.WriteFile(from, raw, 0600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		cmd := newSchLayoutPlanCmd(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--zones", "--from", from, "--out", out, "--report", report})
		err := cmd.Execute()
		if (err != nil) != (budget == 1) {
			t.Fatal(err)
		}
		if !strings.Contains(stderr.String(), "rail-only-attachment") {
			t.Fatal("missing automatic warning", stderr.String())
		}
		data, _ := os.ReadFile(report)
		var result schLayoutReport
		if err := json.Unmarshal(data, &result); err != nil {
			t.Fatal(err)
		}
		if result.ZoneReview == nil || result.ZoneReview.Status != "review-required" || result.ZoneReview.SourceSHA256 != sha256Hex(raw) {
			t.Fatal("lost review on success/failure", string(data))
		}
		if budget == 0 {
			continue
		}
		cmd = newSchZoneReviewCmd(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"--from", from, "--report", report})
		if err := cmd.Execute(); err != nil {
			t.Fatal("advisory review tried to solve", err)
		}
		kept, _ := os.ReadFile(from)
		if !bytes.Equal(raw, kept) {
			t.Fatal("source changed")
		}
		cmd = newSchZoneReviewCmd(&stdout)
		cmd.SetArgs([]string{"--from", from, "--report", from})
		if err := cmd.Execute(); err == nil {
			t.Fatal("source overwrite accepted")
		}
	}
}

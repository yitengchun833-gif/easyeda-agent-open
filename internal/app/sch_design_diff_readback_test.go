package app

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func designReadbackMap(t *testing.T, p schCompositionPlan) map[string]any {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(designDiffBytes(t, p.Connectivity), &value); err != nil {
		t.Fatal(err)
	}
	return value
}

func TestSchDesignDiffSinglePagePlanAndReadbackRoundoff(t *testing.T) {
	p := designDiffPlan(t)
	readback := designReadbackMap(t, p)
	for _, record := range readback["components"].([]any) {
		component := record.(map[string]any)
		delete(component, "pageId")
		placement := component["placement"].(map[string]any)
		placement["x"] = math.Nextafter(placement["x"].(float64), math.Inf(1))
		bbox := placement["bbox"].(map[string]any)
		bbox["minX"] = math.Nextafter(bbox["minX"].(float64), math.Inf(-1))
		pin := component["pins"].([]any)[0].(map[string]any)
		pin["x"] = math.Nextafter(pin["x"].(float64), math.Inf(1))
	}
	diff, err := compareSchDesignInputs(designDiffInput(t, p), designDiffInput(t, readback))
	if err != nil || diff.Status != "synced" || len(diff.Changes) != 0 || diff.ExpectedRevision != diff.ActualRevision || diff.Coverage.DrawingCompared {
		t.Fatalf("equivalent page readback differs: %+v %v", diff, err)
	}
	component := readback["components"].([]any)[0].(map[string]any)
	component["pageId"] = "other-page"
	if _, err := decodeSchDesignInput(designDiffBytes(t, readback)); err == nil {
		t.Fatal("opposing component page accepted")
	}
}

func TestSchDesignDiffReadbackMissingMetadataIsUnverifiedNotDeletion(t *testing.T) {
	p := designDiffPlan(t)
	p.Connectivity.Connections[0].Kind = "net_port_bi"
	readback := designReadbackMap(t, p)
	delete(readback, "modules")
	readback["connections"].([]any)[0].(map[string]any)["kind"] = "netlist"
	a, b := designDiffInput(t, p), designDiffInput(t, readback)
	for _, pair := range [][2]schDesignInput{{a, b}, {b, a}} {
		diff, err := compareSchDesignInputs(pair[0], pair[1])
		if err != nil || diff.Status != "incomplete" || diff.Coverage.CanonicalComplete || len(diff.Changes) != 0 || len(diff.Coverage.Unverified) != 4 {
			t.Fatalf("missing metadata incorrectly compared: %+v %v", diff, err)
		}
		found := map[string]bool{}
		for _, u := range diff.Coverage.Unverified {
			found[u.Path] = true
		}
		if !found["/modules"] || !found["/moduleOrder"] {
			t.Fatalf("missing explicit module coverage: %+v", diff.Coverage)
		}
		// Known changes must remain visible even while another field is unknown.
		known := designReadbackMap(t, p)
		delete(known, "modules")
		known["components"].([]any)[0].(map[string]any)["placement"].(map[string]any)["x"] = p.Connectivity.Components[0].Placement.X + 1e-6
		diff, err = compareSchDesignInputs(a, designDiffInput(t, known))
		if err != nil || diff.Status != "incomplete" || len(diff.Changes) != 1 || !strings.HasSuffix(diff.Changes[0].Path, "/placement/x") {
			t.Fatalf("known true movement hidden: %+v %v", diff, err)
		}
	}
	readback["modules"] = []any{}
	readback["connections"].([]any)[0].(map[string]any)["kind"] = "net_port_bi"
	diff, err := compareSchDesignInputs(a, designDiffInput(t, readback))
	if err != nil || diff.Status != "different" || !diff.Coverage.CanonicalComplete || len(diff.Changes) == 0 {
		t.Fatalf("explicit empty module inventory was treated as unknown: %+v %v", diff, err)
	}
	for _, change := range diff.Changes {
		if change.Domain != "module" {
			t.Fatalf("unexpected change: %+v", change)
		}
	}
	readback["connections"].([]any)[0].(map[string]any)["kind"] = "invented-marker"
	if _, err := decodeSchDesignInput(designDiffBytes(t, readback)); err == nil {
		t.Fatal("illegal connection kind accepted")
	}
}

func TestSchDesignDiffCompletePlansRetainKindsAndDrawingPrecision(t *testing.T) {
	a := designDiffPlan(t)
	a.Connectivity.Connections[0].Kind = "net_port_bi"
	var b schCompositionPlan
	_ = json.Unmarshal(designDiffBytes(t, a), &b)
	b.Connectivity.Connections[0].Kind = "netlist"
	diff, err := compareSchDesignInputs(designDiffInput(t, a), designDiffInput(t, b))
	if err != nil || diff.Status != "different" || !diff.Coverage.CanonicalComplete || len(diff.Changes) != 1 || !strings.HasSuffix(diff.Changes[0].Path, "/kind") {
		t.Fatalf("complete plans lost kind differences: %+v %v", diff, err)
	}
	b.Connectivity.Connections[0].Kind = a.Connectivity.Connections[0].Kind
	b.Layout.Frames[0].TitleX = math.Nextafter(a.Layout.Frames[0].TitleX, math.Inf(1))
	// Layout/canonical geometry can independently carry API tails as well.
	b.Layout.Placements[0].Pins[0].X = math.Nextafter(b.Layout.Placements[0].Pins[0].X, math.Inf(1))
	b.Layout.Placements[0].BBox.MinX = math.Nextafter(b.Layout.Placements[0].BBox.MinX, math.Inf(-1))
	diff, err = compareSchDesignInputs(designDiffInput(t, a), designDiffInput(t, b))
	if err != nil || diff.Status != "synced" || diff.ExpectedRevision != diff.ActualRevision || !diff.Coverage.DrawingCompared {
		t.Fatalf("drawing float tails changed hashes: %+v %v", diff, err)
	}
	for _, delta := range []float64{5, 1e-6, 1e-9} {
		b.Layout.Frames[0].TitleX = a.Layout.Frames[0].TitleX + delta
		diff, err = compareSchDesignInputs(designDiffInput(t, a), designDiffInput(t, b))
		if err != nil || diff.Status != "different" || diff.ExpectedRevision == diff.ActualRevision || len(diff.Changes) != 1 || diff.Changes[0].Domain != "drawing" {
			t.Fatalf("real drawing movement %g hidden: %+v %v", delta, diff, err)
		}
	}
}

package app

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func nativeDiffFixture(t *testing.T) map[string]any {
	t.Helper()
	var wrapper map[string]any
	if err := json.Unmarshal([]byte(autolayoutScene("p", 100, 100, 0)), &wrapper); err != nil {
		t.Fatal(err)
	}
	s := wrapper["result"].(map[string]any)
	s["projectUuid"], s["documentUuid"], s["wiresAvailable"] = "project", "p", true
	s["readScope"] = map[string]any{"complete": true, "concurrentChange": false, "missingIds": []any{}}
	s["wires"] = []any{}
	page := map[string]any{}
	for _, kind := range []string{"components", "wires", "buses", "arcs", "circles", "rectangles", "polygons", "texts", "attributes", "objects"} {
		page[kind] = []any{}
	}
	s["pagePrimitives"] = page
	part := s["components"].([]any)[1].(map[string]any)
	part["device"] = map[string]any{"uuid": strings.Repeat("a", 32), "libraryUuid": strings.Repeat("b", 32)}
	part["netlistAvailable"] = true
	pin := part["pins"].([]any)[0].(map[string]any)
	pin["net"], pin["noConnected"] = "NET", false
	return s
}

func TestSchDesignDiffNativeSnapshotUsesCanonicalAndDrawingComparison(t *testing.T) {
	s := nativeDiffFixture(t)
	raw, _ := json.Marshal(s)
	a, err := decodeSchDesignInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	b, err := decodeSchDesignInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	diff, err := compareSchDesignInputs(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Status != "synced" || !diff.Coverage.DrawingCompared || diff.Coverage.Scope != "native-page-observations" || len(diff.Coverage.Unverified) != 0 {
		t.Fatalf("unexpected diff: %+v", diff)
	}
	s["components"].([]any)[1].(map[string]any)["x"] = 110
	raw, _ = json.Marshal(s)
	b, err = decodeSchDesignInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	diff, err = compareSchDesignInputs(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Status != "different" || len(diff.Changes) == 0 || len(diff.Coverage.Unverified) != 0 {
		t.Fatalf("pose change missed: %+v", diff)
	}
	s["wiresAvailable"] = false
	raw, _ = json.Marshal(s)
	if _, err = decodeSchDesignInput(raw); err == nil {
		t.Fatal("unknown wire inventory passed")
	}
}

func TestSchDesignDiffNativeCoverageIncludesEveryDrawingInventory(t *testing.T) {
	for _, kind := range []string{"components", "wires", "buses", "arcs", "circles", "rectangles", "polygons", "texts", "attributes", "objects"} {
		t.Run(kind, func(t *testing.T) {
			before := nativeDiffFixture(t)
			expected := designDiffInput(t, before)
			before["pagePrimitives"].(map[string]any)[kind] = []any{map[string]any{"primitiveId": "added", "X": 110}}
			diff, err := compareSchDesignInputs(expected, designDiffInput(t, before))
			if err != nil || diff.Status != "different" || !diff.Coverage.DrawingCompared || len(diff.Coverage.Unverified) != 0 {
				t.Fatalf("native inventory coverage incorrect: %+v, %v", diff, err)
			}
			found := false
			for _, change := range diff.Changes {
				if strings.HasPrefix(change.Path, "/drawing/pagePrimitives/"+kind) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing compared inventory %s: %+v", kind, diff.Changes)
			}
		})
	}
}

func TestSchDesignDiffNativeCoverageKeepsMissingEvidence(t *testing.T) {
	for _, scenario := range []string{"partial", "incomplete-scope", "concurrent-change", "missing-ids", "missing-components", "missing-wires", "missing-drawing", "missing-drawing-kind"} {
		t.Run(scenario, func(t *testing.T) {
			snapshot := nativeDiffFixture(t)
			scope := snapshot["readScope"].(map[string]any)
			switch scenario {
			case "partial":
				snapshot["partial"] = true
			case "incomplete-scope":
				scope["complete"] = false
			case "concurrent-change":
				scope["concurrentChange"] = true
			case "missing-ids":
				scope["missingIds"] = []any{"unread"}
			case "missing-components":
				delete(snapshot, "components")
			case "missing-wires":
				delete(snapshot, "wires")
			case "missing-drawing":
				delete(snapshot, "pagePrimitives")
			case "missing-drawing-kind":
				delete(snapshot["pagePrimitives"].(map[string]any), "texts")
			}
			if _, err := decodeSchDesignInput(designDiffBytes(t, snapshot)); err == nil {
				t.Fatal("incomplete native evidence accepted")
			}
		})
	}
	input := designDiffInput(t, nativeDiffFixture(t))
	document := input.canonical.Document
	document.Components[0].Placement = nil
	var err error
	input.canonical, err = connectivity.DecodeDesignEvidence(designDiffBytes(t, document))
	if err != nil {
		t.Fatal(err)
	}
	input.missingDrawing = []connectivity.DesignUnverified{{Path: "/drawing/diagnostic", Reason: "not observed"}}
	diff, err := compareSchDesignInputs(input, input)
	if err != nil || diff.Status != "incomplete" || diff.Coverage.CanonicalComplete {
		t.Fatalf("missing canonical evidence changed: %+v, %v", diff, err)
	}
	paths := map[string]int{}
	for _, item := range diff.Coverage.Unverified {
		paths[item.Path]++
	}
	if len(paths) != 2 || paths["/drawing/diagnostic"] != 2 || paths["/components/"+document.Components[0].ID+"/placement"] != 2 {
		t.Fatalf("specific evidence gaps hidden: %+v", diff.Coverage.Unverified)
	}

	diff, err = compareSchDesignInputs(designDiffInput(t, nativeDiffFixture(t)), designDiffInput(t, designDiffPlan(t)))
	if err != nil || len(diff.Coverage.Unverified) == 0 || diff.Coverage.Unverified[0].Path != "/editor" {
		t.Fatalf("mixed plan/native inputs claimed observed editor state: %+v, %v", diff, err)
	}
}

func TestSchDesignDiffNativeReopenKeepsPinAttributeContentAndRealLabelsProtected(t *testing.T) {
	before := nativeDiffFixture(t)
	part := before["components"].([]any)[1].(map[string]any)
	part["pins"].([]any)[0].(map[string]any)["primitiveId"] = "native-pin"
	before["wires"] = []any{map[string]any{"primitiveId": "wire", "net": "", "x0": 100, "y0": 100, "x1": 120, "y1": 100}}
	page := before["pagePrimitives"].(map[string]any)
	page["wires"] = []any{map[string]any{"primitiveId": "wire", "Net": "", "Line": []any{100, 100, 120, 100}}}
	page["attributes"] = []any{
		map[string]any{"primitiveId": "pin-old", "ParentPrimitiveId": "native-pin", "Key": "Pin Number", "Value": "1", "X": 100, "Y": 100},
		map[string]any{"primitiveId": "empty-name", "ParentPrimitiveId": "wire", "Key": "Name", "Value": "", "KeyVisible": false, "ValueVisible": false},
		map[string]any{"primitiveId": "persistent", "ParentPrimitiveId": "part", "Key": "Name", "Value": "Visible"},
	}
	before["pagePrimitiveBounds"] = map[string]any{"empty-name": map[string]any{"bbox": map[string]any{"minX": 0, "minY": 0, "maxX": 0, "maxY": 0}}}
	raw, _ := json.Marshal(before)
	expected, err := decodeSchDesignInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"reopened", "pin-value", "pin-position", "pin-owner", "persistent-id", "visible-name", "visible-key", "missing-bounds", "nonzero-bounds", "extra-pin-attribute", "missing-pin-attribute", "net-change"} {
		t.Run(scenario, func(t *testing.T) {
			var after map[string]any
			if err := json.Unmarshal(raw, &after); err != nil {
				t.Fatal(err)
			}
			p := after["pagePrimitives"].(map[string]any)
			attrs := p["attributes"].([]any)
			pin, label := attrs[0].(map[string]any), attrs[1].(map[string]any)
			pin["primitiveId"], label["ValueVisible"] = "pin-reopened", true
			switch scenario {
			case "pin-value":
				pin["Value"] = "2"
			case "pin-position":
				pin["X"] = 110
			case "pin-owner":
				pin["ParentPrimitiveId"] = "unknown-pin"
			case "persistent-id":
				attrs[2].(map[string]any)["primitiveId"] = "changed"
			case "visible-name":
				label["Value"] = "NET"
			case "visible-key":
				label["KeyVisible"] = true
			case "missing-bounds":
				delete(after, "pagePrimitiveBounds")
			case "nonzero-bounds":
				after["pagePrimitiveBounds"].(map[string]any)["empty-name"].(map[string]any)["bbox"].(map[string]any)["maxX"] = 10
			case "extra-pin-attribute":
				p["attributes"] = append(attrs, pin)
			case "missing-pin-attribute":
				p["attributes"] = attrs[1:]
			case "net-change":
				after["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["net"] = "OTHER"
			}
			bytes, _ := json.Marshal(after)
			actual, err := decodeSchDesignInput(bytes)
			if err != nil {
				t.Fatal(err)
			}
			diff, err := compareSchDesignInputs(expected, actual)
			if err != nil {
				t.Fatal(err)
			}
			want := "different"
			if scenario == "reopened" {
				want = "synced"
			}
			if diff.Status != want {
				t.Fatalf("%s: want %s, got %+v", scenario, want, diff)
			}
		})
	}
}

package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOfflineLayoutLintReusesGeometryGate(t *testing.T) {
	var native map[string]any
	if err := json.Unmarshal([]byte(autolayoutScene("p", 100, 100, 0)), &native); err != nil {
		t.Fatal(err)
	}
	snapshot := native["result"].(map[string]any)
	file := filepath.Join(t.TempDir(), "geometry.json")
	write := func() {
		raw, _ := json.Marshal(snapshot)
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	var out bytes.Buffer
	if err := offlineLayoutLint(file, 2.54, 0, true, true, &out); err != nil {
		t.Fatal(err)
	}
	var report layoutReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || !report.Strict || report.Total != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	components := snapshot["components"].([]any)
	copyPart := map[string]any{}
	for key, value := range components[1].(map[string]any) {
		copyPart[key] = value
	}
	copyPart["primitiveId"], copyPart["designator"] = "other", "R1"
	snapshot["components"] = append(components, copyPart)
	snapshot["count"] = 3
	write()
	out.Reset()
	if err := offlineLayoutLint(file, 2.54, 0, true, true, &out); err == nil {
		t.Fatal("overlap passed")
	}
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Overlaps) == 0 {
		t.Fatal("existing overlap predicate was not used")
	}
}

func TestOfflineLayoutLintRejectsInterleavedFunctionalGroups(t *testing.T) {
	var native map[string]any
	_ = json.Unmarshal([]byte(autolayoutScene("p", 100, 100, 0)), &native)
	snapshot := native["result"].(map[string]any)
	base := snapshot["components"].([]any)
	for i := 1; i <= 2; i++ {
		raw, _ := json.Marshal(base[1])
		var part map[string]any
		_ = json.Unmarshal(raw, &part)
		part["primitiveId"], part["designator"] = fmt.Sprintf("p%d", i), fmt.Sprintf("R%d", i)
		part["x"] = float64(100 + i*100)
		bbox := part["bbox"].(map[string]any)
		bbox["minX"] = float64(90 + i*100)
		bbox["maxX"] = float64(110 + i*100)
		pin := part["pins"].([]any)[0].(map[string]any)
		pin["x"] = float64(90 + i*100)
		base = append(base, part)
	}
	snapshot["components"], snapshot["count"] = base, 4
	snapshot["groups"] = []offlineLayoutGroup{{ID: "outer", PrimitiveIDs: []string{"id-U1", "p2"}}, {ID: "inner", PrimitiveIDs: []string{"p1"}}}
	file := filepath.Join(t.TempDir(), "groups.json")
	raw, _ := json.Marshal(snapshot)
	_ = os.WriteFile(file, raw, 0600)
	var out bytes.Buffer
	if err := offlineLayoutLint(file, 2.54, 0, true, true, &out); err == nil {
		t.Fatal("interleaved group hulls passed")
	}
	var result struct {
		GroupOverlaps []layoutFinding `json:"groupOverlaps"`
	}
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.GroupOverlaps) != 1 {
		t.Fatalf("missing group finding: %s", out.String())
	}
}

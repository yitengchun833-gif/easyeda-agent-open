package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moduleOwnedCommandFixture(t *testing.T) (pcbLayoutCandidate, []byte, *boardSnapshot) {
	t.Helper()
	region := pcbModuleRegion{ID: "ko", Layer: 1, RuleTypes: []string{"no-pours"}, Points: rectPoints(layoutBBox{MinX: 10, MinY: 10, MaxX: 40, MaxY: 40})}
	pour := pcbModulePour{ID: "old-ground", Net: "GND", Layer: 1, Points: rectPoints(layoutBBox{MinX: 0, MinY: 0, MaxX: 50, MaxY: 50})}
	via := pcbModuleVia{ID: "fence", Net: "GND", X: 30, Y: 20, HoleMil: 10, DiameterMil: 20, Role: "fence"}
	c := pcbLayoutCandidate{ApplySHA256: "apply-sha", Bundle: &pcbLayoutModuleBundle{Kind: "crystal-guard", GroundRoutes: []pcbModuleRoute{{ID: "guard", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{0, 20}, {100, 20}}, Role: "guard"}}, Vias: []pcbModuleVia{via}, Regions: []pcbModuleRegion{region}, Pours: []pcbModulePour{pour}}}
	steps := []playbookStep{
		{ID: "route-guard-01", Action: "pcb.line.create", Capture: map[string]string{"TRACK": "$.primitiveId"}},
		{ID: "via-fence", Action: "pcb.via.create", Capture: map[string]string{"VIA": "$.primitiveId"}},
		{ID: "region-ko", Action: "pcb.region.create", Capture: map[string]string{"REGION": "$.primitiveId"}},
		{ID: "pour-old-ground", Action: "pcb.pour.create", Capture: map[string]string{"POUR": "$.primitiveId"}},
	}
	c.Apply.Steps = steps
	var lines []string
	h, _ := json.Marshal(map[string]any{"playbookSha256": "apply-sha"})
	lines = append(lines, string(h))
	for i, e := range []struct{ id, name, pid string }{{"route-guard-01", "TRACK", "old-track"}, {"via-fence", "VIA", "via-id"}, {"region-ko", "REGION", "region-id"}, {"pour-old-ground", "POUR", "pour-id"}} {
		raw, _ := json.Marshal(journalEntry{Idx: i + 1, ID: e.id, Status: "ok", Captured: map[string]string{e.name: e.pid}})
		lines = append(lines, string(raw))
	}
	available := true
	s := &boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"}, ArcsAvailable: &available, Arcs: []any{}, Fills: []any{}}}
	s.Copper.Lines = []any{
		map[string]any{"primitiveId": "split-a", "net": "GND", "layer": float64(1), "startX": 0.0, "startY": 20.0, "endX": 50.0, "endY": 20.0, "lineWidth": 8.0},
		map[string]any{"primitiveId": "split-b", "net": "GND", "layer": float64(1), "startX": 50.0, "startY": 20.0, "endX": 100.0, "endY": 20.0, "lineWidth": 8.0},
		map[string]any{"primitiveId": "unrelated-ground", "net": "GND", "layer": float64(1), "startX": 0.0, "startY": 80.0, "endX": 100.0, "endY": 80.0, "lineWidth": 8.0},
	}
	s.Copper.Vias = []any{map[string]any{"primitiveId": "via-id", "net": "GND", "x": 30.0, "y": 20.0, "holeDiameter": 10.0, "diameter": 20.0}}
	s.Copper.Regions = []any{moduleCheckRegionMap("region-id", 1, pointsBBox(region.Points), "no-pours")}
	s.Copper.Pours = []any{moduleCheckAreaMap("pour-id", "GND", 1, pointsBBox(pour.Points))}
	s.Copper.Poured = []any{moduleCheckPouredMap("materialized", "pour-id", "GND", 1, []any{moduleCheckPolygonSource(pour.Points)})}
	return c, []byte(strings.Join(lines, "\n") + "\n"), s
}

func TestRecoverPCBModuleOwnedUsesJournalAndExactSplitGeometry(t *testing.T) {
	c, journal, s := moduleOwnedCommandFixture(t)
	owned, err := recoverPCBModuleOwned(c, journal, s, "candidate", "journal", "board")
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) != 5 {
		t.Fatalf("owned=%+v", owned)
	}
	for _, o := range owned {
		if o.PrimitiveID == "unrelated-ground" || !strings.Contains(o.Source, "candidate") {
			t.Fatalf("wrong ownership %+v", o)
		}
	}
	s.Copper.Lines = append(s.Copper.Lines, map[string]any{"primitiveId": "overlap", "net": "GND", "layer": float64(1), "startX": 25.0, "startY": 20.0, "endX": 75.0, "endY": 20.0, "lineWidth": 8.0})
	if _, err := recoverPCBModuleOwned(c, journal, s, "candidate", "journal", "board"); err == nil || !strings.Contains(err.Error(), "overlapping/ambiguous") {
		t.Fatalf("overlap accepted: %v", err)
	}
}

func TestPcbModuleOwnedCommandWritesExactOfflineInventory(t *testing.T) {
	candidate, journal, board := moduleOwnedCommandFixture(t)
	dir := t.TempDir()
	candidatePath := filepath.Join(dir, "candidate.json")
	journalPath := filepath.Join(dir, "journal.jsonl")
	boardPath := filepath.Join(dir, "board.json")
	outPath := filepath.Join(dir, "owned.json")
	for path, value := range map[string]any{candidatePath: candidate, boardPath: board} {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(journalPath, journal, 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	cmd := newPcbModuleOwnedCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--candidate", candidatePath, "--journal", journalPath, "--board", boardPath, "--out", outPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, stderr.String())
	}
	var owned []pcbModuleOwnedObject
	raw, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &owned); err != nil {
		t.Fatal(err)
	}
	if len(owned) != 5 || !strings.Contains(stdout.String(), outPath) || !strings.Contains(stderr.String(), "5 exact object") {
		t.Fatalf("owned=%+v stdout=%q stderr=%q", owned, stdout.String(), stderr.String())
	}
	for _, object := range owned {
		if object.PrimitiveID == "unrelated-ground" || !strings.Contains(object.Source, "previousCandidateSha256=") {
			t.Fatalf("unsafe ownership output: %+v", object)
		}
	}
}

func TestPcbModuleOwnedCommandHelpAndOverwriteGuard(t *testing.T) {
	var help bytes.Buffer
	cmd := newPcbModuleOwnedCmd(&help, &help)
	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--candidate", "--journal", "--board", "--out", "Shared net names never grant ownership"} {
		if !strings.Contains(help.String(), want) {
			t.Fatalf("help missing %q: %s", want, help.String())
		}
	}

	dir := t.TempDir()
	input := filepath.Join(dir, "input.json")
	if err := os.WriteFile(input, []byte(`{"preserve":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd = newPcbModuleOwnedCmd(&help, &help)
	cmd.SetArgs([]string{"--candidate", input, "--journal", input, "--board", input, "--out", input})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must not overwrite") {
		t.Fatalf("unsafe overwrite accepted: %v", err)
	}
	if raw, _ := os.ReadFile(input); string(raw) != `{"preserve":true}` {
		t.Fatalf("input overwritten: %s", raw)
	}
}

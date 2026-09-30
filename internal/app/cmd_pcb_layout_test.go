package app

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func solveCLISnapshot(t *testing.T) boardSnapshot {
	t.Helper()
	shape := func() any { return []any{"RECT", 10.0, 10.0, 0.0} }
	comp := func(id, ref string, x, y, minX, minY, maxX, maxY float64, pads ...boardPad) boardComp {
		return boardComp{ID: id, Designator: ref, Layer: 1, X: x, Y: y, BBox: &layoutBBox{MinX: minX, MinY: minY, MaxX: maxX, MaxY: maxY}, Pads: pads}
	}
	pad := func(id, number, net string, x, y float64) boardPad {
		return boardPad{ID: id, Number: number, Net: net, Layer: 1, X: x, Y: y, W: 10, H: 10, Shape: shape()}
	}
	available := true
	snap := boardSnapshot{
		Components: []boardComp{
			comp("u", "U1", 30, 100, 20, 65, 40, 135, pad("u1", "1", "A", 30, 80), pad("u2", "2", "B", 30, 120)),
			comp("j", "J1", 270, 100, 260, 65, 280, 135, pad("j1", "1", "A", 270, 80), pad("j2", "2", "B", 270, 120)),
			comp("m", "M1", 150, 100, 140, 70, 160, 130, pad("m1", "1", "X", 150, 80), pad("m2", "2", "Y", 150, 120)),
			comp("c", "C1", 180, 100, 170, 90, 190, 110, pad("c1", "1", "X", 175, 100), pad("c2", "2", "GND", 185, 100)),
		},
		Outline:      &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 220}, Points: [][2]float64{{0, 0}, {300, 0}, {300, 220}, {0, 220}}},
		CopperLayers: 2,
		Rules:        &boardRules{Source: "live", ClearanceMil: 5, ClearanceTrackTrackMil: 5, TrackWidthMinMil: 4, CopperToEdgeMil: 5, ViaDiameterMil: 16, ViaDrillMil: 8},
		Copper: &boardCopperSnapshot{
			Availability:  map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
			ArcsAvailable: &available,
			Lines:         []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{},
		},
	}
	hash, err := boardSnapshotSemanticSHA256(&snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	return snap
}

func solveCLIRequest() pcbsolve.Request {
	return pcbsolve.Request{SchemaVersion: 1, Units: "mil", MaxStates: 200000, MaxCandidates: 3,
		Layout:  pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, FixedRefs: []string{"U1", "J1"}, Groups: []pcblayout.Group{{ID: "blocker", AnchorRef: "M1", Refs: []string{"M1", "C1"}, Offsets: []pcblayout.Offset{{}, {Y: 60}}}}},
		Routing: pcbsolve.RoutingRequest{Step: 10, MaxDetour: 0, MaxStates: 100000, Demands: []pcbsolve.Demand{{ID: "a", Net: "A", From: "U1.1", To: "J1.1", Width: 4, Layers: []int{1}}, {ID: "b", Net: "B", From: "U1.2", To: "J1.2", Width: 4, Layers: []int{1}}}},
	}
}

func writeSolveCLIJSON(t *testing.T, path string, value any) {
	t.Helper()
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestPCBLayoutSolveCheckRenderCLI(t *testing.T) {
	dir := t.TempDir()
	boardPath, requestPath, reportPath := filepath.Join(dir, "board.json"), filepath.Join(dir, "request.json"), filepath.Join(dir, "report.json")
	writeSolveCLIJSON(t, boardPath, solveCLISnapshot(t))
	request := solveCLIRequest()
	request.Layout.Keepouts = []pcblayout.Keepout{{ID: "mechanical-opening", BBox: pcbmodel.BBox{MinX: 5, MinY: 5, MaxX: 15, MaxY: 15}, Layers: []int{1}}}
	writeSolveCLIJSON(t, requestPath, request)
	var stdout, stderr bytes.Buffer
	cmd := newPcbLayoutSolveCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", boardPath, "--from", requestPath, "--out", reportPath, "--apply-project", "ceshi", "--apply-doc", "pcb-doc"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("solve: %v\n%s", err, stderr.String())
	}
	for _, path := range []string{reportPath, filepath.Join(dir, "report.svg"), filepath.Join(dir, "report.local.svg"), filepath.Join(dir, "report.baseline.svg"), filepath.Join(dir, "report.baseline.local.svg"), filepath.Join(dir, "report.attempt-01.svg"), filepath.Join(dir, "report.attempt-01.local.svg"), filepath.Join(dir, "report.candidate-01.json"), filepath.Join(dir, "report.candidate-01.svg"), filepath.Join(dir, "report.candidate-01.local.svg"), filepath.Join(dir, "report.candidate-01.layout.apply.json")} {
		if info, err := os.Stat(path); err != nil || info.Size() == 0 {
			t.Fatalf("missing solve artifact %s: %v", path, err)
		}
	}
	checkPath := filepath.Join(dir, "check.json")
	cmd = newPcbLayoutCheckCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", boardPath, "--from", requestPath, "--candidate", filepath.Join(dir, "report.candidate-01.json"), "--out", checkPath})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("check: %v\n%s", err, stderr.String())
	}
	renderPath := filepath.Join(dir, "manual.svg")
	cmd = newPcbLayoutRenderCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", boardPath, "--candidate", filepath.Join(dir, "report.candidate-01.json"), "--out", renderPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(renderPath)
	if err != nil || !strings.Contains(string(raw), "moved after route feedback") {
		t.Fatalf("render lacks route-feedback evidence: %v", err)
	}
	// --from adds the independent mechanical constraints to the exact same
	// candidate preview; a different request must never supply its overlays.
	cmd = newPcbLayoutRenderCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", boardPath, "--from", requestPath, "--candidate", filepath.Join(dir, "report.candidate-01.json"), "--out", renderPath})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	withRequest, err := os.ReadFile(renderPath)
	if err != nil || !bytes.Contains(withRequest, []byte(`data-layout-keepout="mechanical-opening"`)) {
		t.Fatalf("render --from omitted its request keepout: %v", err)
	}
	changedRequest := request
	changedRequest.Layout.MinGap++
	writeSolveCLIJSON(t, requestPath, changedRequest)
	cmd = newPcbLayoutRenderCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--board", boardPath, "--from", requestPath, "--candidate", filepath.Join(dir, "report.candidate-01.json"), "--out", renderPath})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "provenance") {
		t.Fatalf("render accepted unrelated constraint overlays: %v", err)
	}
	retained, err := os.ReadFile(renderPath)
	if err != nil || !bytes.Equal(retained, withRequest) {
		t.Fatalf("failed render replaced the prior preview: %v", err)
	}
}

func TestSolveLocalRenderFocusExcludesUnrelatedFarComponents(t *testing.T) {
	snap := solveCLISnapshot(t)
	snap.Outline.BBox.MaxX = 1200
	snap.Outline.Points = [][2]float64{{0, 0}, {1200, 0}, {1200, 220}, {0, 220}}
	snap.Components = append(snap.Components, boardComp{
		ID: "far", Designator: "FAR1", Layer: 1, X: 1100, Y: 100,
		BBox: &layoutBBox{MinX: 1080, MinY: 80, MaxX: 1120, MaxY: 120},
	})
	hash, err := boardSnapshotSemanticSHA256(&snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	board, err := boardSnapshotToModel(&snap)
	if err != nil {
		t.Fatal(err)
	}
	focus := solveFocusBBox(board, board, solveCLIRequest(), nil)
	if focus.MaxX >= 1080 || focus.Width() >= board.Outline.BBox.Width() {
		t.Fatalf("local focus included unrelated far component: focus=%+v board=%+v", focus, board.Outline.BBox)
	}
}

func TestPCBLayoutCheckWritesFailureReportForTamperedCandidate(t *testing.T) {
	dir := t.TempDir()
	boardPath, requestPath := filepath.Join(dir, "board.json"), filepath.Join(dir, "request.json")
	writeSolveCLIJSON(t, boardPath, solveCLISnapshot(t))
	request := solveCLIRequest()
	writeSolveCLIJSON(t, requestPath, request)
	board, err := boardSnapshotToModel(func() *boardSnapshot { s := solveCLISnapshot(t); return &s }())
	if err != nil {
		t.Fatal(err)
	}
	report, err := pcbsolve.Solve(t.Context(), board, request)
	if err != nil || len(report.Candidates) == 0 {
		t.Fatal(err)
	}
	candidate := report.Candidates[0]
	candidate.Routes[0].Routes[0].Points[0][0]++
	candidatePath, outPath := filepath.Join(dir, "tampered.json"), filepath.Join(dir, "check.json")
	writeSolveCLIJSON(t, candidatePath, candidate)
	cmd := newPcbLayoutCheckCmd(ioDiscard{}, ioDiscard{})
	cmd.SetArgs([]string{"--board", boardPath, "--from", requestPath, "--candidate", candidatePath, "--out", outPath})
	if err := cmd.Execute(); err == nil {
		t.Fatal("tampered candidate passed")
	}
	raw, err := os.ReadFile(outPath)
	if err != nil || !strings.Contains(string(raw), `"status": "fail"`) {
		t.Fatalf("failure report missing: %v %s", err, raw)
	}
	if _, err := os.Stat(filepath.Join(dir, "check.svg")); err != nil {
		t.Fatal(err)
	}
}

func TestBoardSnapshotAdapterDoesNotInferFourLayerRoles(t *testing.T) {
	snap := solveCLISnapshot(t)
	snap.CopperLayers = 4
	hash, err := boardSnapshotSemanticSHA256(&snap)
	if err != nil {
		t.Fatal(err)
	}
	snap.SemanticSHA256 = hash
	if _, err := boardSnapshotToModel(&snap); err == nil || !strings.Contains(err.Error(), "ordered layer roles") {
		t.Fatalf("four-layer count was inferred: %v", err)
	}
}

type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }

func TestPCBAutomaticChannelArtifacts(t *testing.T) {
	testPCBRouteFeedbackArtifacts(t, "automatic-channel")
}

func TestPCBMultiGroupClearanceArtifacts(t *testing.T) {
	testPCBRouteFeedbackArtifacts(t, "multi-group-clearance")
}

func TestPCBFixedRouteOrderArtifacts(t *testing.T) {
	testPCBRouteFeedbackArtifacts(t, "fixed-route-order")
}

func testPCBRouteFeedbackArtifacts(t *testing.T, name string) {
	t.Helper()
	var board pcbmodel.Board
	var req pcbsolve.Request
	fixture := filepath.Join("..", "..", "pkg", "pcbsolve", "testdata", name)
	for name, dst := range map[string]any{"board.json": &board, "request.json": &req} {
		raw, err := os.ReadFile(filepath.Join(fixture, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := decodeStrict(raw, dst); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := pcbsolve.Solve(t.Context(), board, req)
	if err != nil || len(rep.Candidates) == 0 {
		t.Fatalf("solve: %v %s", err, rep.Reason)
	}
	if err := pcbsolve.Check(t.Context(), board, req, rep.Candidates[0]); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if root := os.Getenv("EASYEDA_PCB_SOLVE_ARTIFACT_DIR"); root != "" {
		dir = filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "report.json")
	if err := writeJSONAtomic(path, rep); err != nil {
		t.Fatal(err)
	}
	if err := writeSolveArtifacts(path, board, req, rep, nil, []string{filepath.Join(fixture, "board.json"), filepath.Join(fixture, "request.json")}); err != nil {
		t.Fatal(err)
	}
	markers := map[string]string{"report.candidate-01.svg": "data-reservation=", "report.candidate-01.local.svg": "planning only"}
	if name != "fixed-route-order" {
		markers["report.attempt-01.svg"] = "data-conflict="
	}
	for name, marker := range markers {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(raw, []byte(marker)) {
			t.Fatalf("%s missing %s", name, marker)
		}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		var visibleNote strings.Builder
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid SVG %s: %v", name, err)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "text" {
				for _, attr := range start.Attr {
					if attr.Name.Local == "data-review-note" {
						var line string
						if err := decoder.DecodeElement(&line, &start); err != nil {
							t.Fatal(err)
						}
						visibleNote.WriteString(line)
					}
				}
			}
		}
		if strings.Contains(name, "candidate-01") && visibleNote.String() != strings.Join(rep.Candidates[0].MoveReasons, "; ") {
			t.Fatalf("candidate preview truncated its move reasons: %q", visibleNote.String())
		}
	}
	for _, keepout := range req.Layout.Keepouts {
		for _, name := range []string{"report.baseline.svg", "report.candidate-01.svg", "report.candidate-01.local.svg"} {
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil || !bytes.Contains(raw, []byte(`data-layout-keepout="`+keepout.ID+`"`)) {
				t.Fatalf("%s omitted independent mechanical keepout %s: %v", name, keepout.ID, err)
			}
		}
	}
	t.Logf("review=%s states=%d attempts=%d movement=%g", path, rep.States, len(rep.Attempts), rep.Candidates[0].Metrics.TotalDistance)
}

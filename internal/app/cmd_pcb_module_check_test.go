package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func moduleCheckCopperSnapshot() *boardSnapshot {
	available := true
	return &boardSnapshot{
		Components: []boardComp{},
		Copper: &boardCopperSnapshot{
			Availability:  map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
			ArcsAvailable: &available,
			Lines:         []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{},
		},
	}
}

func TestModuleCheckRejectsStaleSemanticHashAndUnknownCopper(t *testing.T) {
	before := moduleCheckCopperSnapshot()
	after := moduleCheckCopperSnapshot()
	h, err := boardSnapshotSemanticSHA256(before)
	if err != nil {
		t.Fatal(err)
	}
	candidate := pcbLayoutCandidate{ID: "candidate-01", Module: "m", BoardSemanticSHA256: h + "-stale", Bundle: &pcbLayoutModuleBundle{Kind: "fixture"}}
	rep := checkPCBModule(candidate, before, after, filepath.Join(t.TempDir(), "missing.journal"))
	if rep.Status != "fail" || !containsText(rep.Findings, "semantic hash differs") {
		t.Fatalf("stale baseline was not rejected: %+v", rep)
	}
	after.Copper.Availability["poured"] = "unknown"
	rep = checkPCBModule(candidate, before, after, filepath.Join(t.TempDir(), "missing.journal"))
	if !containsText(rep.Limitations, "poured is \"unknown\"") {
		t.Fatalf("unknown poured geometry was not reported: %+v", rep)
	}
}

func TestModuleJournalRejectsPartialStep(t *testing.T) {
	candidate := pcbLayoutCandidate{
		ApplySHA256: "expected", Bundle: &pcbLayoutModuleBundle{Kind: "fixture"},
		Apply: playbook{Steps: []playbookStep{{ID: "create", Action: "pcb.line.create", Capture: map[string]string{"PID": "$.primitiveId"}}}},
	}
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	content := "{\"playbookSha256\":\"expected\"}\n{\"idx\":1,\"id\":\"create\",\"status\":\"error\"}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyModuleJournal(candidate, moduleCheckCopperSnapshot(), moduleCheckCopperSnapshot(), path); err == nil || !strings.Contains(err.Error(), "not successful") {
		t.Fatalf("partial journal err=%v", err)
	}
}

func TestModuleJournalRejectsDuplicateExtraAndMismatchedCapturedPID(t *testing.T) {
	before, after := moduleCheckCopperSnapshot(), moduleCheckCopperSnapshot()
	after.Copper.Lines = []any{map[string]any{
		"primitiveId": "new-track", "net": "SIG", "layer": 1.0,
		"startX": 0.0, "startY": 0.0, "endX": 20.0, "endY": 0.0, "lineWidth": 8.0,
	}}
	candidate := pcbLayoutCandidate{
		ApplySHA256: "expected", Bundle: &pcbLayoutModuleBundle{Kind: "fixture"},
		Apply: playbook{Steps: []playbookStep{{ID: "create", Action: "pcb.line.create", Payload: map[string]any{
			"net": "SIG", "layer": 1, "startX": 0.0, "startY": 0.0, "endX": 20.0, "endY": 0.0, "lineWidth": 8.0,
		}, Capture: map[string]string{"PID": "$.primitiveId"}}}},
	}
	write := func(lines ...string) string {
		path := filepath.Join(t.TempDir(), "journal.jsonl")
		content := "{\"playbookSha256\":\"expected\"}\n" + strings.Join(lines, "\n") + "\n"
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	good := `{"idx":1,"id":"create","status":"ok(verified)","captured":{"PID":"new-track"}}`
	if _, err := verifyModuleJournal(candidate, before, after, write(good)); err != nil {
		t.Fatalf("valid exact journal failed: %v", err)
	}
	if _, err := verifyModuleJournal(candidate, before, after, write(good, good)); err == nil || !strings.Contains(err.Error(), "want exactly") {
		t.Fatalf("duplicate/extra journal step accepted: %v", err)
	}
	after.Copper.Lines[0].(map[string]any)["endX"] = 21.0
	if _, err := verifyModuleJournal(candidate, before, after, write(good)); err == nil || !strings.Contains(err.Error(), "exactly match") {
		t.Fatalf("captured PID with wrong geometry accepted: %v", err)
	}
	before.Copper.Lines = cloneModuleCheckValue(t, after.Copper.Lines).([]any)
	if _, err := verifyModuleJournal(candidate, before, after, write(good)); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("captured PID already present before was accepted: %v", err)
	}
}

func TestModuleCopperRejectsMissingAndUnexpectedObjects(t *testing.T) {
	before := moduleCheckCopperSnapshot()
	after := moduleCheckCopperSnapshot()
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "fixture", SignalRoutes: []pcbModuleRoute{{ID: "sig", Net: "SIG", Layer: 1, WidthMil: 8, Points: [][2]float64{{0, 0}, {20, 0}}}}}}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "not exactly covered") {
		t.Fatalf("missing route err=%v", err)
	}
	candidate.Bundle.SignalRoutes = nil
	after.Copper.Lines = []any{map[string]any{"primitiveId": "surprise", "net": "SIG", "layer": float64(1), "startX": 0.0, "startY": 0.0, "endX": 20.0, "endY": 0.0, "lineWidth": 8.0}}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "unexpected non-baseline track") {
		t.Fatalf("unexpected route err=%v", err)
	}
}

func TestModuleComponentsRejectsFreshPadOffsetDespiteMatchingAnchor(t *testing.T) {
	pad := boardPad{ID: "x1-1", Number: "1", Net: "OSC", Layer: 1, X: 10, Y: 20, W: 12, H: 8, Shape: []any{"RECT", 12.0, 8.0, 0.0}}
	before := moduleCheckCopperSnapshot()
	before.Components = []boardComp{{ID: "x1", Designator: "X1", Layer: 1, X: 100, Y: 100, Pads: []boardPad{pad}}}
	after := cloneModuleCheckSnapshot(t, before)
	candidate := pcbLayoutCandidate{Placements: []pcbLayoutPlacement{{Ref: "X1", PrimitiveID: "x1", Layer: 1, XMil: 100, YMil: 100, Pads: []boardPad{pad}}}}
	if err := verifyModuleComponents(candidate, before, after); err != nil {
		t.Fatalf("matching fresh pad failed: %v", err)
	}
	after.Components[0].Pads[0].X += 1
	if err := verifyModuleComponents(candidate, before, after); err == nil || !strings.Contains(err.Error(), "pad 1") {
		t.Fatalf("shifted fresh pad accepted because anchor matched: %v", err)
	}
}

func TestModuleCheckAcceptsHostReadbackQuantizationAndSplitTracks(t *testing.T) {
	tracks := []pcbTrack{
		{ID: "a", Net: "GND", Layer: 1, Width: 8, X1: 0, Y1: 0, X2: 40, Y2: 0},
		{ID: "b", Net: "GND", Layer: 1, Width: 8, X1: 40, Y1: 0, X2: 100, Y2: 0},
	}
	if got, ok := matchingTrackCoverage(tracks, "GND", 1, 8, [2]float64{0, 0}, [2]float64{100, 0}); !ok || len(got) != 2 {
		t.Fatalf("host-split route was not accepted: ok=%v tracks=%v", ok, got)
	}
	expected := pcbModuleVia{Net: "GND", X: 10, Y: 20, HoleMil: 12.01, DiameterMil: 24.02}
	if !moduleViaMatchesExpected(pcbViaP{Net: "GND", X: 10, Y: 20, Hole: 12, Dia: 24}, expected) {
		t.Fatal("host-rounded via size was not accepted")
	}
	if moduleViaMatchesExpected(pcbViaP{Net: "GND", X: 10, Y: 20, Hole: 11.9, Dia: 24}, expected) {
		t.Fatal("material via-size change was hidden by the readback tolerance")
	}
}

func TestModuleCheckReadsNormalizedMilAndThermalStrokes(t *testing.T) {
	boundary := moduleCheckAreaMap("pour", "GND", 1, layoutBBox{MinX: 1000, MinY: 1000, MaxX: 2000, MaxY: 2000})
	poured := moduleCheckPouredMap("material", "pour", "GND", 1, []any{1000.0, 1000.0, "L", 2000.0, 1000.0, 2000.0, 2000.0, 1000.0, 2000.0, 1000.0, 1000.0})
	poured["fills"] = append(poured["fills"].([]any), map[string]any{"id": "thermal", "fill": false, "lineWidth": 10.0, "sourceUnits": "mil", "lineWidthUnits": "mil", "arcSweepUnits": "degree", "source": []any{[]any{1500.0, 1500.0, "L", 1600.0, 1500.0}}})
	areas, strokes, err := parseModuleCopperAreasAndStrokes(nil, []any{boundary}, []any{poured})
	if err != nil {
		t.Fatal(err)
	}
	if len(areas) != 1 || len(strokes) != 1 || math.Abs(strokes[0].Width-10) > netPathGeomEps || math.Abs(strokes[0].X1-1500) > netPathGeomEps || math.Abs(strokes[0].X2-1600) > netPathGeomEps {
		t.Fatalf("normalized geometry mismatch: areas=%+v strokes=%+v", areas, strokes)
	}
}

func TestModuleSemanticHashIgnoresDumpProjectLabel(t *testing.T) {
	s := moduleCheckCopperSnapshot()
	s.Project = "human-readable-label"
	h1, err := moduleCheckSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	s.Project = "another-label"
	h2, err := moduleCheckSemanticSHA256(s)
	if err != nil || h1 != h2 {
		t.Fatalf("project metadata changed module semantic hash: %s %s err=%v", h1, h2, err)
	}
}

func TestModuleKeepoutRejectsStaticAndMaterializedCopper(t *testing.T) {
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "fixture", Regions: []pcbModuleRegion{{ID: "ko", Layer: 1, RuleTypes: []string{"no-pours"}, Points: [][2]float64{{0, 0}, {100, 0}, {100, 100}, {0, 100}}}}}}
	after := moduleCheckCopperSnapshot()
	after.Copper.Regions = []any{moduleCheckRegionMap("ko-readback", 1, layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}, "no-pours")}
	inside := []any{10.0, 10.0, "L", 20.0, 10.0, 20.0, 20.0, 10.0, 20.0, 10.0, 10.0}
	after.Copper.Fills = []any{map[string]any{"primitiveId": "fill", "layer": float64(1), "geometryAvailable": true, "source": inside}}
	if err := verifyModuleKeepoutEmpty(candidate, after); err == nil || !strings.Contains(err.Error(), "static fill enters") {
		t.Fatalf("static fill intrusion err=%v", err)
	}
	after.Copper.Fills = []any{}
	after.Copper.Pours = []any{moduleCheckAreaMap("pour-boundary", "GND", 1, layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})}
	after.Copper.Poured = []any{map[string]any{"primitiveId": "poured", "pourPrimitiveId": "pour-boundary", "net": "GND", "layer": float64(1), "fills": []any{map[string]any{"sourceUnits": "mil", "lineWidthUnits": "mil", "arcSweepUnits": "degree", "id": "inside", "lineWidth": 0.0, "fill": true, "source": []any{inside}}}}}
	if err := verifyModuleKeepoutEmpty(candidate, after); err == nil || !strings.Contains(err.Error(), "materialized poured copper enters") {
		t.Fatalf("poured intrusion err=%v", err)
	}
}

func TestPolygonSourceContoursPreservesHoleAndFlattensVerifiedArc(t *testing.T) {
	outer := []any{-10.0, 0.0, "ARC", 180.0, 10.0, 0.0, "ARC", 180.0, -10.0, 0.0}
	hole := []any{-2.0, -2.0, "L", -2.0, 2.0, 2.0, 2.0, 2.0, -2.0, -2.0, -2.0}
	contours, err := polygonSourceContours([]any{outer, hole})
	if err != nil {
		t.Fatal(err)
	}
	if len(contours) != 2 || len(contours[0]) < 20 || len(contours[1]) != 4 {
		t.Fatalf("contours=%d outer=%d hole=%d", len(contours), len(contours[0]), len(contours[1]))
	}
	if _, err := polygonSourceContours([]any{0.0, 0.0, "C", 1.0, 1.0}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unverified curve command should fail closed: %v", err)
	}
}

func TestPolygonEquivalenceRejectsSameBBoxDifferentContourAndHoleDirection(t *testing.T) {
	rect := []any{0.0, 0.0, "L", 100.0, 0.0, 100.0, 100.0, 0.0, 100.0, 0.0, 0.0}
	reversedRect := []any{0.0, 0.0, "L", 0.0, 100.0, 100.0, 100.0, 100.0, 0.0, 0.0, 0.0}
	if equal, err := polygonSourcesEquivalent(rect, reversedRect); err != nil || !equal {
		t.Fatalf("globally reversed equivalent contour failed: equal=%v err=%v", equal, err)
	}
	segmentedRect := []any{0.0, 0.0, "L", 50.0, 0.0, 100.0, 0.0, 100.0, 50.0, 100.0, 100.0, 0.0, 100.0, 0.0, 0.0}
	if equal, err := polygonSourcesEquivalent(rect, segmentedRect); err != nil || !equal {
		t.Fatalf("collinear host resegmentation was not normalized: equal=%v err=%v", equal, err)
	}
	diamond := []any{0.0, 50.0, "L", 50.0, 0.0, 100.0, 50.0, 50.0, 100.0, 0.0, 50.0}
	if equal, err := polygonSourcesEquivalent(rect, diamond); err != nil || equal {
		t.Fatalf("same-bbox different polygon accepted: equal=%v err=%v", equal, err)
	}
	holeCW := []any{25.0, 25.0, "L", 25.0, 75.0, 75.0, 75.0, 75.0, 25.0, 25.0, 25.0}
	holeCCW := []any{25.0, 25.0, "L", 75.0, 25.0, 75.0, 75.0, 25.0, 75.0, 25.0, 25.0}
	if equal, err := polygonSourcesEquivalent([]any{rect, holeCW}, []any{rect, holeCCW}); err != nil || equal {
		t.Fatalf("independently reversed hole direction accepted: equal=%v err=%v", equal, err)
	}
	if equal, err := polygonSourcesEquivalent(rect, []any{0.0, 0.0, "C", 100.0, 100.0}); err == nil || equal {
		t.Fatalf("unknown curve did not fail closed: equal=%v err=%v", equal, err)
	}
}

func TestMaterializedPourConnectivityUsesRealIslandsAndAnalyticAnnulusContact(t *testing.T) {
	outer := moduleCheckRectSource(layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})
	holePoints := rectPoints(layoutBBox{MinX: 20, MinY: 20, MaxX: 80, MaxY: 80})
	for left, right := 0, len(holePoints)-1; left < right; left, right = left+1, right-1 {
		holePoints[left], holePoints[right] = holePoints[right], holePoints[left]
	}
	island := moduleCheckRectSource(layoutBBox{MinX: 40, MinY: 40, MaxX: 60, MaxY: 60})
	materialized := moduleCheckPouredMap("materialized", "local", "GND", 1, []any{outer, moduleCheckPolygonSource(holePoints), island})
	connected, err := materializedPourConnectsViaToAnchor(materialized,
		pcbModuleVia{ID: "island", X: 50, Y: 50, HoleMil: 4, DiameterMil: 10},
		[]pcbModuleVia{{ID: "outer", X: 10, Y: 10, HoleMil: 4, DiameterMil: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if connected {
		t.Fatal("nested island was incorrectly treated as the same copper component as the outer material")
	}
	connected, err = materializedPourConnectsViaToAnchor(materialized,
		pcbModuleVia{ID: "outer-a", X: 10, Y: 10, HoleMil: 4, DiameterMil: 10},
		[]pcbModuleVia{{ID: "outer-b", X: 90, Y: 10, HoleMil: 4, DiameterMil: 10}})
	if err != nil || !connected {
		t.Fatalf("same outer material component did not connect: connected=%v err=%v", connected, err)
	}

	// This 0.2mil spoke crosses the annulus between the old fixed 15-degree
	// samples. Exact segment/annulus intersection must still prove contact.
	thinSpoke := moduleCheckPouredMap("thin", "thin-local", "GND", 1,
		[]any{moduleCheckRectSource(layoutBBox{MinX: -20, MinY: .6, MaxX: 40, MaxY: .8})})
	connected, err = materializedPourConnectsViaToAnchor(thinSpoke,
		pcbModuleVia{ID: "target", X: 0, Y: 0, HoleMil: 4, DiameterMil: 20},
		[]pcbModuleVia{{ID: "anchor", X: 30, Y: .7, HoleMil: 4, DiameterMil: 20}})
	if err != nil || !connected {
		t.Fatalf("analytic annulus contact missed a thin spoke: connected=%v err=%v", connected, err)
	}
}

func TestExpectedLocalPourCannotBorrowGlobalSameNetMaterial(t *testing.T) {
	expected := pcbModulePour{ID: "local", Net: "GND", Layer: 1, Points: rectPoints(layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})}
	localBoundary := moduleCheckAreaMap("local-boundary", "GND", 1, pointsBBox(expected.Points))
	globalBoundary := moduleCheckAreaMap("global-boundary", "GND", 1, layoutBBox{MinX: -100, MinY: -100, MaxX: 200, MaxY: 200})
	localMaterial := moduleCheckPouredMap("local-material", "local-boundary", "GND", 1,
		[]any{moduleCheckRectSource(layoutBBox{MinX: 0, MinY: 0, MaxX: 20, MaxY: 20})})
	globalMaterial := moduleCheckPouredMap("global-material", "global-boundary", "GND", 1,
		[]any{moduleCheckRectSource(layoutBBox{MinX: -100, MinY: -100, MaxX: 200, MaxY: 200})})
	materialized, err := findExpectedMaterializedPour(expected, []any{localBoundary, globalBoundary}, []any{localMaterial, globalMaterial})
	if err != nil {
		t.Fatal(err)
	}
	connected, err := materializedPourConnectsViaToAnchor(materialized,
		pcbModuleVia{ID: "target", X: 50, Y: 50, HoleMil: 4, DiameterMil: 10},
		[]pcbModuleVia{{ID: "anchor", X: 10, Y: 10, HoleMil: 4, DiameterMil: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if connected {
		t.Fatal("local pour borrowed connectivity from a different global same-net materialized pour")
	}
}

func TestModuleKeepoutUsesExactConcaveContourAndLayer(t *testing.T) {
	concave := [][2]float64{{0, 0}, {100, 0}, {100, 40}, {40, 40}, {40, 100}, {0, 100}}
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "fixture", Regions: []pcbModuleRegion{{ID: "concave", Layer: 1, RuleTypes: []string{"no-pours"}, Points: concave}}}}
	after := moduleCheckCopperSnapshot()
	region := moduleCheckRegionMap("concave-readback", 1, pointsBBox(concave), "no-pours")
	region["source"] = moduleCheckPolygonSource(concave)
	after.Copper.Regions = []any{region}
	fill := func(id string, layer int, box layoutBBox) map[string]any {
		m := moduleCheckAreaMap(id, "GND", layer, box)
		m["fillMode"] = "solid"
		return m
	}
	// Inside the concave polygon's bbox but outside its actual L shape.
	after.Copper.Fills = []any{fill("legal-notch", 1, layoutBBox{MinX: 60, MinY: 60, MaxX: 80, MaxY: 80})}
	if err := verifyModuleKeepoutEmpty(candidate, after); err != nil {
		t.Fatalf("legal copper in concave notch was rejected: %v", err)
	}
	// Same XY on another layer is unrelated to a TOP-only region.
	after.Copper.Fills = []any{fill("other-layer", 2, layoutBBox{MinX: 10, MinY: 60, MaxX: 20, MaxY: 80})}
	if err := verifyModuleKeepoutEmpty(candidate, after); err != nil {
		t.Fatalf("other-layer copper was rejected by TOP no-pours: %v", err)
	}
	after.Copper.Fills = []any{fill("intrusion", 1, layoutBBox{MinX: 10, MinY: 60, MaxX: 20, MaxY: 80})}
	if err := verifyModuleKeepoutEmpty(candidate, after); err == nil || !strings.Contains(err.Error(), "concave") {
		t.Fatalf("actual concave no-pours intrusion passed: %v", err)
	}
}

func TestModuleSnapshotContextRejectsRuleAndOutlineDrift(t *testing.T) {
	_, before := crystalGuardFixture()
	after := cloneModuleCheckSnapshot(t, before)
	if err := verifyModuleSnapshotContext(before, after); err != nil {
		t.Fatalf("unchanged context failed: %v", err)
	}
	after.Rules.ClearanceMil++
	if err := verifyModuleSnapshotContext(before, after); err == nil || !strings.Contains(err.Error(), "rules differ") {
		t.Fatalf("rule drift passed: %v", err)
	}
	after = cloneModuleCheckSnapshot(t, before)
	after.Outline.Points[1][0]--
	if err := verifyModuleSnapshotContext(before, after); err == nil || !strings.Contains(err.Error(), "outline polygon differs") {
		t.Fatalf("outline drift passed: %v", err)
	}
}

func TestCrystalLiveClearanceRechecksFreshGeometry(t *testing.T) {
	after := moduleCheckCopperSnapshot()
	after.Rules = &boardRules{ClearanceMil: 5, ClearanceTrackTrackMil: 5, CopperToEdgeMil: 5}
	after.Outline = &boardOutline{BBox: layoutBBox{MinX: -100, MinY: -100, MaxX: 200, MaxY: 200}, Source: "bbox"}
	after.Components = []boardComp{
		{ID: "x1", Designator: "X1", Layer: 1, BBox: &layoutBBox{MinX: 0, MinY: -5, MaxX: 10, MaxY: 5}},
		{ID: "u1", Designator: "U1", Layer: 1, BBox: &layoutBBox{MinX: 30, MinY: 30, MaxX: 40, MaxY: 40}},
	}
	after.Copper.Lines = []any{
		map[string]any{"primitiveId": "sig", "net": "OSC", "layer": 1.0, "startX": 0.0, "startY": 0.0, "endX": 20.0, "endY": 0.0, "lineWidth": 4.0},
		map[string]any{"primitiveId": "foreign", "net": "OTHER", "layer": 1.0, "startX": 0.0, "startY": 8.0, "endX": 20.0, "endY": 8.0, "lineWidth": 4.0},
	}
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "crystal-guard", OwnedRefs: []string{"X1"}, SignalRoutes: []pcbModuleRoute{{ID: "osc", Net: "OSC", Layer: 1, WidthMil: 4, Points: [][2]float64{{0, 0}, {20, 0}}}}}}
	if err := verifyCrystalLiveClearance(candidate, after); err == nil || !strings.Contains(err.Error(), "live clearance") {
		t.Fatalf("fresh other-net clearance violation passed: %v", err)
	}
}

func TestCrystalSensitiveCoverageRechecksConsistentButTooSmallRegions(t *testing.T) {
	after := moduleCheckCopperSnapshot()
	after.Rules = &boardRules{ClearanceMil: 5, ClearanceTrackTrackMil: 5}
	after.Components = []boardComp{{ID: "x1", Designator: "X1", Layer: 1, BBox: &layoutBBox{MinX: 40, MinY: 40, MaxX: 60, MaxY: 60}}}
	after.Copper.Lines = []any{map[string]any{"primitiveId": "osc", "net": "OSC", "layer": 1.0, "startX": 60.0, "startY": 50.0, "endX": 80.0, "endY": 50.0, "lineWidth": 4.0}}
	regions := []pcbModuleRegion{
		{ID: "top", Layer: 1, RuleTypes: []string{"no-pours"}, Points: rectPoints(layoutBBox{MinX: 30, MinY: 30, MaxX: 90, MaxY: 70})},
		{ID: "bottom", Layer: 2, RuleTypes: []string{"no-pours"}, Points: rectPoints(layoutBBox{MinX: 30, MinY: 30, MaxX: 90, MaxY: 70})},
	}
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "crystal-guard", OwnedRefs: []string{"X1"}, Regions: regions,
		SignalRoutes: []pcbModuleRoute{{ID: "osc", Net: "OSC", Layer: 1, WidthMil: 4, Points: [][2]float64{{60, 50}, {80, 50}}}},
		Metrics:      map[string]float64{"sensitiveKeepoutMarginMil": 2}}}
	for _, region := range regions {
		after.Copper.Regions = append(after.Copper.Regions, moduleCheckRegionMap(region.ID+"-readback", region.Layer, pointsBBox(region.Points), "no-pours"))
	}
	if err := verifyCrystalSensitiveCoverage(candidate, after); err != nil {
		t.Fatalf("complete sensitive coverage failed: %v", err)
	}
	tooSmall := rectPoints(layoutBBox{MinX: 30, MinY: 30, MaxX: 85, MaxY: 70})
	candidate.Bundle.Regions[0].Points = tooSmall
	after.Copper.Regions[0].(map[string]any)["source"] = moduleCheckPolygonSource(tooSmall)
	if err := verifyCrystalSensitiveCoverage(candidate, after); err == nil || !strings.Contains(err.Error(), "complete OSC") {
		t.Fatalf("consistent but too-small no-pours region passed: %v", err)
	}
}

func TestModuleCopperPreservesArcsAndMaterializedPours(t *testing.T) {
	before, after := moduleCheckCopperSnapshot(), moduleCheckCopperSnapshot()
	arc := map[string]any{"primitiveId": "arc-1", "net": "AUX", "layer": 1.0, "startX": 0.0, "startY": 0.0, "endX": 20.0, "endY": 0.0, "lineWidth": 8.0, "arcAngle": 90.0}
	pour := moduleCheckAreaMap("pour-1", "GND", 1, layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})
	poured := moduleCheckPouredMap("poured-1", "pour-1", "GND", 1, []any{moduleCheckRectSource(layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})})
	before.Copper.Arcs, after.Copper.Arcs = []any{arc}, []any{cloneModuleCheckValue(t, arc)}
	before.Copper.Pours, after.Copper.Pours = []any{pour}, []any{cloneModuleCheckValue(t, pour)}
	before.Copper.Poured, after.Copper.Poured = []any{poured}, []any{cloneModuleCheckValue(t, poured)}
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "fixture"}}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err != nil {
		t.Fatalf("unchanged arc/poured copper failed: %v", err)
	}
	after.Copper.Arcs[0].(map[string]any)["arcAngle"] = 80.0
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "arc") {
		t.Fatalf("changed baseline arc accepted: %v", err)
	}
	after.Copper.Arcs = []any{cloneModuleCheckValue(t, arc)}
	after.Copper.Poured[0].(map[string]any)["fills"] = []any{map[string]any{"source": []any{moduleCheckRectSource(layoutBBox{MinX: 0, MinY: 0, MaxX: 90, MaxY: 100})}}}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "materialized") {
		t.Fatalf("changed baseline materialized pour accepted: %v", err)
	}
}

func TestAffectedMaterializedPourAllowsOnlyDeclaredEnvelopeChange(t *testing.T) {
	before, after := moduleCheckCopperSnapshot(), moduleCheckCopperSnapshot()
	boundary := moduleCheckAreaMap("baseline-pour", "GND", 1, layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})
	outer := moduleCheckRectSource(layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100})
	beforePoured := moduleCheckPouredMap("baseline-materialized", "baseline-pour", "GND", 1, []any{outer})
	before.Copper.Pours, after.Copper.Pours = []any{boundary}, []any{cloneModuleCheckValue(t, boundary)}
	before.Copper.Poured = []any{beforePoured}
	insideHole := moduleCheckRectSource(layoutBBox{MinX: 42, MinY: 42, MaxX: 58, MaxY: 58})
	after.Copper.Poured = []any{moduleCheckPouredMap("rebuilt-materialized", "baseline-pour", "GND", 1, []any{outer, insideHole})}
	declaration := pcbModuleAffectedPour{
		BoundaryPrimitiveID: "baseline-pour", MaterializedPrimitiveID: "baseline-materialized", Net: "GND", Layer: 1,
		ImpactEnvelope: layoutBBox{MinX: 40, MinY: 40, MaxX: 60, MaxY: 60},
	}
	candidate := pcbLayoutCandidate{Bundle: &pcbLayoutModuleBundle{Kind: "fixture", AffectedBaselinePours: []pcbModuleAffectedPour{declaration}}}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err != nil {
		t.Fatalf("declared inside-envelope rebuild failed: %v", err)
	}
	candidate.Bundle.AffectedBaselinePours = nil
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "materialized") {
		t.Fatalf("undeclared materialized change passed: %v", err)
	}
	candidate.Bundle.AffectedBaselinePours = []pcbModuleAffectedPour{declaration}
	outOfEnvelopeHole := moduleCheckRectSource(layoutBBox{MinX: 42, MinY: 42, MaxX: 75, MaxY: 58})
	after.Copper.Poured = []any{moduleCheckPouredMap("rebuilt-materialized", "baseline-pour", "GND", 1, []any{outer, outOfEnvelopeHole})}
	if err := verifyModuleCopper(candidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "outside declared impact envelope") {
		t.Fatalf("outside-envelope materialized change passed: %v", err)
	}
}

func TestBaselineAreaSemanticFieldChangesFail(t *testing.T) {
	box := layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	region := moduleCheckRegionMap("r", 1, box, "no-pours")
	changedRegion := cloneModuleCheckValue(t, region).(map[string]any)
	changedRegion["regionName"] = "changed"
	if err := compareRegionMaps(region, changedRegion); err == nil || !strings.Contains(err.Error(), "regionName") {
		t.Fatalf("region name change accepted: %v", err)
	}
	pour := moduleCheckAreaMap("p", "GND", 1, box)
	changedPour := cloneModuleCheckValue(t, pour).(map[string]any)
	changedPour["priority"] = 2.0
	if err := comparePourMaps(pour, changedPour); err == nil || !strings.Contains(err.Error(), "priority") {
		t.Fatalf("pour priority change accepted: %v", err)
	}
	fill := moduleCheckAreaMap("f", "GND", 1, box)
	fill["fillMode"] = "solid"
	changedFill := cloneModuleCheckValue(t, fill).(map[string]any)
	changedFill["fillMode"] = "mesh"
	if err := compareAreaMaps(fill, changedFill, "fill"); err == nil || !strings.Contains(err.Error(), "fillMode") {
		t.Fatalf("fill mode change accepted: %v", err)
	}
}

func TestModuleCheckPassesCompleteCrystalGuardReadback(t *testing.T) {
	in, before := crystalGuardFixture()
	setModuleCheckPadShapes(before)
	before.Components = append(before.Components, lpComp("r1", "R1", 900, 820, 0, lpBBox(885, 810, 915, 830),
		boardPad{ID: "r1-1", Number: "1", Net: "AUX", Layer: 1, X: 890, Y: 820, W: 12, H: 12, Shape: []any{"RECT", 12.0, 12.0, 0.0}},
		boardPad{ID: "r1-2", Number: "2", Net: "GND", Layer: 1, X: 910, Y: 820, W: 12, H: 12, Shape: []any{"RECT", 12.0, 12.0, 0.0}},
	))
	baselineTrack := map[string]any{"primitiveId": "baseline-aux", "net": "AUX", "layer": float64(1), "startX": 850.0, "startY": 850.0, "endX": 900.0, "endY": 850.0, "lineWidth": 8.0}
	baselineVia := map[string]any{"primitiveId": "baseline-via", "net": "GND", "x": 940.0, "y": 820.0, "holeDiameter": 10.0, "diameter": 20.0}
	baselineRegion := moduleCheckRegionMap("baseline-region", 1, layoutBBox{MinX: 900, MinY: 720, MaxX: 920, MaxY: 740}, "no-tracks")
	baselineFill := moduleCheckAreaMap("baseline-fill", "", 1, layoutBBox{MinX: 930, MinY: 750, MaxX: 950, MaxY: 770})
	baselineFill["fillMode"] = "solid"
	baselinePour := moduleCheckAreaMap("baseline-pour", "GND", 1, layoutBBox{MinX: 850, MinY: 780, MaxX: 970, MaxY: 860})
	baselinePoured := moduleCheckPouredMap("baseline-poured", "baseline-pour", "GND", 1,
		[]any{moduleCheckRectSource(layoutBBox{MinX: 850, MinY: 780, MaxX: 970, MaxY: 860})})
	before.Copper.Lines = append(before.Copper.Lines, baselineTrack)
	before.Copper.Vias = append(before.Copper.Vias, baselineVia)
	before.Copper.Regions = append(before.Copper.Regions, baselineRegion)
	before.Copper.Fills = append(before.Copper.Fills, baselineFill)
	before.Copper.Pours = append(before.Copper.Pours, baselinePour)
	before.Copper.Poured = append(before.Copper.Poured, baselinePoured)
	semantic, err := boardSnapshotSemanticSHA256(before)
	if err != nil {
		t.Fatal(err)
	}
	before.SemanticSHA256 = semantic

	report, err := planPCBLayoutModule(in, before, "crystal-guard", 1)
	if err != nil {
		t.Fatalf("plan crystal guard: %v rejected=%+v", err, report.Rejected)
	}
	candidate := report.Candidates[0]
	if candidate.Bundle == nil || len(candidate.Bundle.SignalRoutes) != 4 || len(candidate.Bundle.GroundRoutes) == 0 || len(candidate.Bundle.Regions) != 2 || len(candidate.Bundle.Pours) != 8 {
		t.Fatalf("candidate does not exercise the complete crystal bundle: %+v", candidate.Bundle)
	}
	applyRaw, err := json.MarshalIndent(candidate.Apply, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	candidate.ApplySHA256 = sha256String(append(applyRaw, '\n'))

	after := cloneModuleCheckSnapshot(t, before)
	applyModuleCheckPlacements(after, candidate.Placements)
	// The old owned OSC copper is replaced. Every route segment, via, region and
	// pour below is a fresh readback object; unrelated baseline objects retain
	// both identity and geometry.
	after.Copper.Lines = []any{cloneModuleCheckValue(t, baselineTrack)}
	for _, route := range append(append([]pcbModuleRoute(nil), candidate.Bundle.SignalRoutes...), candidate.Bundle.GroundRoutes...) {
		for segmentIndex := 0; segmentIndex+1 < len(route.Points); segmentIndex++ {
			p, q := route.Points[segmentIndex], route.Points[segmentIndex+1]
			after.Copper.Lines = append(after.Copper.Lines, map[string]any{
				"primitiveId": fmt.Sprintf("readback-route-%s-%02d", route.ID, segmentIndex+1),
				"net":         route.Net, "layer": float64(route.Layer), "startX": p[0], "startY": p[1], "endX": q[0], "endY": q[1], "lineWidth": route.WidthMil,
			})
		}
	}
	after.Copper.Vias = []any{cloneModuleCheckValue(t, baselineVia)}
	for _, via := range candidate.Bundle.Vias {
		after.Copper.Vias = append(after.Copper.Vias, map[string]any{
			"primitiveId": "readback-via-" + via.ID, "net": via.Net,
			"x": via.X, "y": via.Y, "holeDiameter": via.HoleMil, "diameter": via.DiameterMil,
		})
	}
	after.Copper.Regions = []any{cloneModuleCheckValue(t, baselineRegion)}
	for _, region := range candidate.Bundle.Regions {
		after.Copper.Regions = append(after.Copper.Regions, moduleCheckRegionMap("readback-region-"+region.ID, region.Layer, pointsBBox(region.Points), region.RuleTypes...))
	}
	after.Copper.Fills = []any{cloneModuleCheckValue(t, baselineFill)}
	after.Copper.Pours = []any{cloneModuleCheckValue(t, baselinePour)}
	after.Copper.Poured = []any{cloneModuleCheckValue(t, baselinePoured)}
	for _, pour := range candidate.Bundle.Pours {
		primitiveID := "readback-pour-" + pour.ID
		after.Copper.Pours = append(after.Copper.Pours, moduleCheckAreaMap(primitiveID, pour.Net, pour.Layer, pointsBBox(pour.Points)))
		// Each boundary is already a slab outside the expanded no-pours bbox;
		// materialized copper therefore needs no host-generated hole to prove the
		// sensitive area empty.
		after.Copper.Poured = append(after.Copper.Poured, moduleCheckPouredMap("materialized-"+pour.ID, primitiveID, pour.Net, pour.Layer,
			[]any{moduleCheckPolygonSource(pour.Points)}))
	}

	journalPath := writeModuleCheckJournal(t, candidate)
	check := checkPCBModule(candidate, before, after, journalPath)
	if check.Status != "pass" || len(check.Findings) != 0 || !containsText(check.Limitations, "legacy candidate") {
		t.Fatalf("complete crystal-guard readback did not pass: %+v", check)
	}
	for _, want := range []string{
		"baseline semantic hash matches",
		"apply journal matches every playbook step exactly",
		"member poses match and non-member components are unchanged",
		"tracks, arcs, vias, regions, pours, materialized pours and non-owned copper match",
		"signal OSC_IN topology C21.1 -> X1.1 -> U6.2 is TOP, 0 via; actual length",
		"signal OSC_OUT topology C20.1 -> X1.3 -> U6.3 is TOP, 0 via; actual length",
		"owned GND pads, guard, two-layer via path and MCU ground anchor are connected",
		"no-pours envelopes contain no materialized or static copper",
	} {
		if !containsText(check.Checks, want) {
			t.Fatalf("successful report did not exercise %q: %+v", want, check.Checks)
		}
	}
	subsetReplacementCandidate := candidate
	subsetReplacementBundle := *candidate.Bundle
	subsetReplacementCandidate.Bundle = &subsetReplacementBundle
	subsetReplacementBundle.ReplacePrimitiveIDs = append([]string(nil), candidate.Bundle.ReplacePrimitiveIDs[:1]...)
	if err := verifyModuleCopper(subsetReplacementCandidate, before, after, pcbModuleJournalProof{}); err == nil || !strings.Contains(err.Error(), "fresh baseline replacement proof failed") || !strings.Contains(err.Error(), "missing=") {
		t.Fatalf("module-check accepted a replacement subset: %v", err)
	}
	missingBottomPour := candidate
	missingBottomPourBundle := *candidate.Bundle
	missingBottomPour.Bundle = &missingBottomPourBundle
	missingBottomPourBundle.Pours = append([]pcbModulePour(nil), candidate.Bundle.Pours[:len(candidate.Bundle.Pours)-1]...)
	if err := verifyCrystalGroundConnectivity(missingBottomPour, after); err == nil || !strings.Contains(err.Error(), "TOP=4 and BOTTOM=4") {
		t.Fatalf("crystal-guard without BOTTOM pour passed: %v", err)
	}
	crossingPour := candidate
	crossingPourBundle := *candidate.Bundle
	crossingPour.Bundle = &crossingPourBundle
	crossingPourBundle.Pours = append([]pcbModulePour(nil), candidate.Bundle.Pours...)
	keepoutBox := pointsBBox(candidate.Bundle.Regions[0].Points)
	for i := range crossingPourBundle.Pours {
		if crossingPourBundle.Pours[i].Layer == 1 && strings.HasSuffix(crossingPourBundle.Pours[i].ID, "-left") {
			box := pointsBBox(crossingPourBundle.Pours[i].Points)
			box.MaxX = (keepoutBox.MinX + keepoutBox.MaxX) / 2
			crossingPourBundle.Pours[i].Points = rectPoints(box)
			break
		}
	}
	if err := verifyCrystalProtectionStructure(crossingPour, "GND"); err == nil || !strings.Contains(err.Error(), "intersects or touches no-pours") {
		t.Fatalf("local ring slab entering no-pours was accepted: %v", err)
	}
	missingBottomRegion := candidate
	missingBottomRegionBundle := *candidate.Bundle
	missingBottomRegion.Bundle = &missingBottomRegionBundle
	missingBottomRegionBundle.Regions = append([]pcbModuleRegion(nil), candidate.Bundle.Regions[:1]...)
	if err := verifyModuleKeepoutEmpty(missingBottomRegion, after); err == nil || !strings.Contains(err.Error(), "TOP=1 and BOTTOM=2") {
		t.Fatalf("crystal-guard without BOTTOM no-pours region passed: %v", err)
	}
	withoutFenceContact := cloneModuleCheckSnapshot(t, after)
	var fence pcbModuleVia
	for _, via := range candidate.Bundle.Vias {
		if via.Role == "fence" {
			fence = via
			break
		}
	}
	if fence.ID == "" {
		t.Fatal("fixture has no fence via")
	}
	removedFenceContact := false
	for _, pour := range candidate.Bundle.Pours {
		if pour.Layer != 1 {
			continue
		}
		materialized, err := findExpectedMaterializedPour(pour, withoutFenceContact.Copper.Pours, withoutFenceContact.Copper.Poured)
		if err != nil {
			t.Fatal(err)
		}
		touches, err := materializedPourTouchesVia(materialized, fence)
		if err != nil {
			t.Fatal(err)
		}
		if !touches {
			continue
		}
		for _, raw := range withoutFenceContact.Copper.Poured {
			m := raw.(map[string]any)
			if asString(m["pourPrimitiveId"]) != "readback-pour-"+pour.ID {
				continue
			}
			fill := m["fills"].([]any)[0].(map[string]any)
			source := fill["source"].([]any)
			holePoints := rectPoints(layoutBBox{MinX: fence.X - fence.DiameterMil, MinY: fence.Y - fence.DiameterMil, MaxX: fence.X + fence.DiameterMil, MaxY: fence.Y + fence.DiameterMil})
			for left, right := 0, len(holePoints)-1; left < right; left, right = left+1, right-1 {
				holePoints[left], holePoints[right] = holePoints[right], holePoints[left]
			}
			fill["source"] = append(source, moduleCheckPolygonSource(holePoints))
			removedFenceContact = true
		}
	}
	if !removedFenceContact {
		t.Fatal("fixture fence via touches no TOP ring slab")
	}
	if err := verifyCrystalGroundConnectivity(candidate, withoutFenceContact); err == nil || !strings.Contains(err.Error(), "fence via") {
		t.Fatalf("missing materialized contact on one fence via was accepted: %v", err)
	}
	disconnectedGuardCandidate := candidate
	disconnectedGuardBundle := *candidate.Bundle
	disconnectedGuardCandidate.Bundle = &disconnectedGuardBundle
	disconnectedGuardBundle.GroundRoutes = append(append([]pcbModuleRoute(nil), candidate.Bundle.GroundRoutes...), pcbModuleRoute{
		ID: "guard-disconnected", Net: "GND", Layer: 1, WidthMil: 8, Role: "guard",
		Points: [][2]float64{{20, 20}, {40, 20}},
	})
	disconnectedGuardAfter := cloneModuleCheckSnapshot(t, after)
	disconnectedGuardAfter.Copper.Lines = append(disconnectedGuardAfter.Copper.Lines, map[string]any{
		"primitiveId": "readback-guard-disconnected", "net": "GND", "layer": 1.0,
		"startX": 20.0, "startY": 20.0, "endX": 40.0, "endY": 20.0, "lineWidth": 8.0,
	})
	if err := verifyCrystalGroundConnectivity(disconnectedGuardCandidate, disconnectedGuardAfter); err == nil || !strings.Contains(err.Error(), "guard-disconnected") || !strings.Contains(err.Error(), "no listed-copper GND path") {
		t.Fatalf("disconnected but physically present guard segment was accepted: %v", err)
	}
	after.Copper.Vias = append(after.Copper.Vias, map[string]any{
		"primitiveId": "forbidden-osc-via", "net": "OSC_IN", "x": 1000.0, "y": 1000.0, "holeDiameter": 10.0, "diameter": 20.0,
	})
	if _, err := verifyCrystalSignalConnectivity(candidate, after); err == nil || !strings.Contains(err.Error(), "want 0 vias") {
		t.Fatalf("fresh signal-net via did not invalidate TOP/0-via proof: %v", err)
	}
}

func setModuleCheckPadShapes(s *boardSnapshot) {
	for ci := range s.Components {
		for pi := range s.Components[ci].Pads {
			p := &s.Components[ci].Pads[pi]
			p.Shape = []any{"RECT", p.W, p.H, 0.0}
		}
	}
}

func cloneModuleCheckSnapshot(t *testing.T, source *boardSnapshot) *boardSnapshot {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result boardSnapshot
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return &result
}

func cloneModuleCheckValue(t *testing.T, source any) any {
	t.Helper()
	raw, err := json.Marshal(source)
	if err != nil {
		t.Fatal(err)
	}
	var result any
	if err = json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func applyModuleCheckPlacements(s *boardSnapshot, placements []pcbLayoutPlacement) {
	byRef := map[string]pcbLayoutPlacement{}
	for _, placement := range placements {
		byRef[placement.Ref] = placement
	}
	for i := range s.Components {
		placement, ok := byRef[s.Components[i].Designator]
		if !ok {
			continue
		}
		s.Components[i].X = placement.XMil
		s.Components[i].Y = placement.YMil
		s.Components[i].Rotation = placement.RotationDeg
		s.Components[i].Layer = placement.Layer
		s.Components[i].BBox = placement.BBox
		s.Components[i].Pads = placement.Pads
	}
}

func moduleCheckAreaMap(id, net string, layer int, box layoutBBox) map[string]any {
	return map[string]any{
		"primitiveId": id, "net": net, "layer": float64(layer), "geometryAvailable": true,
		"source": moduleCheckRectSource(box), "pourName": nil, "fillMethod": "solid", "priority": 0.0,
		"lineWidth": 8.0, "locked": true,
	}
}

func moduleCheckRegionMap(id string, layer int, box layoutBBox, rules ...string) map[string]any {
	ruleNames := make([]any, len(rules))
	for i, rule := range rules {
		ruleNames[i] = rule
	}
	result := moduleCheckAreaMap(id, "", layer, box)
	result["ruleTypeNames"] = ruleNames
	ruleTypes := make([]any, len(rules))
	for i := range rules {
		ruleTypes[i] = float64(i + 1)
	}
	result["ruleType"] = ruleTypes
	result["regionName"] = nil
	return result
}

func moduleCheckPouredMap(id, pourID, net string, layer int, source any) map[string]any {
	return map[string]any{
		"primitiveId": id, "pourPrimitiveId": pourID, "net": net, "layer": float64(layer),
		"fills": []any{map[string]any{"sourceUnits": "mil", "lineWidthUnits": "mil", "arcSweepUnits": "degree", "id": id + "-fill", "lineWidth": 8.0, "fill": true, "source": source}},
	}
}

func moduleCheckRectSource(box layoutBBox) []any {
	return moduleCheckPolygonSource(rectPoints(box))
}

func moduleCheckPolygonSource(points [][2]float64) []any {
	if len(points) == 0 {
		return nil
	}
	result := []any{points[0][0], points[0][1], "L"}
	for _, point := range points[1:] {
		result = append(result, point[0], point[1])
	}
	result = append(result, points[0][0], points[0][1])
	return result
}

func writeModuleCheckJournal(t *testing.T, candidate pcbLayoutCandidate) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "apply.journal.jsonl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	if err = enc.Encode(journalHeader{PlaybookSha: candidate.ApplySHA256, Name: candidate.Apply.Meta.Name, StartedAt: "2026-09-22T00:00:00Z"}); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	for i, step := range candidate.Apply.Steps {
		captured := map[string]string{}
		for name := range step.Capture {
			captured[name] = "readback-" + step.ID
		}
		if err = enc.Encode(journalEntry{Idx: i + 1, ID: step.ID, Status: "ok(verified)", Captured: captured}); err != nil {
			_ = f.Close()
			t.Fatal(err)
		}
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func escapeFixture() (pcbRoutingIntent, *boardSnapshot) {
	pad := func(id, num, net string, x, y float64) boardPad {
		return boardPad{ID: id, Number: num, Net: net, Layer: 1, X: x, Y: y, W: 10, H: 10, Shape: []any{"RECT", 10.0, 10.0, 0.0}}
	}
	owner := boardComp{ID: "u", Designator: "U1", X: 50, Y: 50, Layer: 1, Pads: []boardPad{pad("u1", "1", "A", 50, 80), pad("u2", "2", "B", 50, 120)}}
	target := boardComp{ID: "j", Designator: "J1", X: 250, Y: 100, Layer: 1, Pads: []boardPad{pad("j1", "1", "A", 250, 80), pad("j2", "2", "B", 250, 120)}}
	available := true
	snap := &boardSnapshot{Components: []boardComp{owner, target}, CopperLayers: 2, Outline: &boardOutline{Source: "polygon", Format: "polyline", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 200}, Points: [][2]float64{{0, 0}, {300, 0}, {300, 200}, {0, 200}}}, Rules: &boardRules{Source: "live", ClearanceMil: 5, TrackWidthMinMil: 4, CopperToEdgeMil: 5, ViaDiameterMil: 16, ViaDrillMil: 8}, Copper: &boardCopperSnapshot{Availability: map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"}, ArcsAvailable: &available, Lines: []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{}}}
	in := pcbRoutingIntent{Owners: []string{"U1"}, StepMil: 10, MaxDetourMil: 50, MaxStates: 30000, Demands: []pcbEscapeDemand{{ID: "a", From: "U1.1", To: "J1.1", Net: "A", WidthMil: 4, Layer: 1}, {ID: "b", From: "U1.2", To: "J1.2", Net: "B", WidthMil: 4, Layer: 1}}}
	return in, snap
}

func cloneEscapeSnapshot(s *boardSnapshot) *boardSnapshot {
	raw, _ := json.Marshal(s)
	var out boardSnapshot
	_ = json.Unmarshal(raw, &out)
	return &out
}

func TestEscapePlanCoversEveryOwnerPadAndRevalidates(t *testing.T) {
	in, s := escapeFixture()
	r, err := planPCBEscape(in, s)
	if err != nil || r.Status != "pass" || len(r.Routes) != 2 {
		t.Fatalf("report=%+v err=%v", r, err)
	}
	if err := verifyPCBEscape(in, r, s); err != nil {
		t.Fatal(err)
	}
	s.CapturedAt = "later"
	if err := verifyPCBEscape(in, r, s); err != nil {
		t.Fatalf("timestamp should not stale proof: %v", err)
	}
	s.Components[0].Pads[0].X++
	if err := verifyPCBEscape(in, r, s); err == nil {
		t.Fatal("stale geometry accepted")
	}
}

func TestEscapePlanRejectsMissingNCAndUnknownGeometry(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*pcbRoutingIntent, *boardSnapshot)
	}{
		{"uncovered", func(in *pcbRoutingIntent, s *boardSnapshot) { in.Demands = in.Demands[:1] }},
		{"NC without reason", func(in *pcbRoutingIntent, s *boardSnapshot) {
			in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true}
		}},
		{"duplicate coverage", func(in *pcbRoutingIntent, s *boardSnapshot) { in.Demands[1].From = "U1.1" }},
		{"unknown pad", func(in *pcbRoutingIntent, s *boardSnapshot) { s.Components[1].Pads[0].Shape = nil }},
		{"unknown copper", func(in *pcbRoutingIntent, s *boardSnapshot) { s.Copper.Availability["poured"] = "unknown" }},
		{"fallback rules", func(in *pcbRoutingIntent, s *boardSnapshot) { s.Rules.Source = "fallback" }},
		{"bbox outline", func(in *pcbRoutingIntent, s *boardSnapshot) { s.Outline.Source = "bbox" }},
		{"same-net different pad", func(in *pcbRoutingIntent, s *boardSnapshot) {
			s.Components[0].Pads[1].Net = "A"
			in.Demands = in.Demands[:1]
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in, s := escapeFixture()
			tc.mutate(&in, s)
			r, err := planPCBEscape(in, s)
			if err == nil || r.Status == "pass" {
				t.Fatalf("false pass %+v", r)
			}
		})
	}
	in, s := escapeFixture()
	in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true, NCReason: "Explicit unused test pin in source requirement"}
	if r, err := planPCBEscape(in, s); err != nil || r.Status != "pass" {
		t.Fatalf("explicit NC failed: %+v %v", r, err)
	}
}

func TestEscapePlanDetectsJointCompetition(t *testing.T) {
	in, s := escapeFixture()
	// A full-height wall has one narrow gap: either net can use it alone, but
	// their common local exit cannot hold both copper widths plus clearance.
	s.Copper.Lines = []any{map[string]any{"primitiveId": "wall-low", "net": "WALL", "layer": float64(1), "startX": 150.0, "startY": 0.0, "endX": 150.0, "endY": 88.0, "lineWidth": 4.0}, map[string]any{"primitiveId": "wall-high", "net": "WALL", "layer": float64(1), "startX": 150.0, "startY": 112.0, "endX": 150.0, "endY": 200.0, "lineWidth": 4.0}}
	in.StepMil = 5
	in.MaxDetourMil = 50
	in.MaxStates = 100000
	for i := range in.Demands {
		in.Demands[i].To = ""
		in.Demands[i].Toward = "J1." + []string{"1", "2"}[i]
		in.Demands[i].Exits = [][2]float64{{170, 100}}
		in.Demands[i].Congested = &layoutBBox{MinX: 20, MinY: 20, MaxX: 155, MaxY: 180}
	}
	for i := range in.Demands {
		solo := in
		solo.Demands = append([]pcbEscapeDemand(nil), in.Demands...)
		j := 1 - i
		solo.Demands[j] = pcbEscapeDemand{ID: in.Demands[j].ID, From: in.Demands[j].From, NC: true, NCReason: "test isolation only"}
		r, err := planPCBEscape(solo, s)
		if err != nil || r.Status != "pass" {
			t.Fatalf("solo %d unavailable %+v %v", i, r, err)
		}
	}
	r, err := planPCBEscape(in, s)
	if err == nil || r.Status == "pass" {
		t.Fatalf("individually routable competing reservations passed %+v", r)
	}
}

func TestEscapePlanChoosesBlockedDemandWithoutPermutingOwners(t *testing.T) {
	in, s := escapeFixture()
	// B is the easy demand and intentionally comes first. A's destination is
	// occupied by exact different-net pad copper, so adding any B reservation
	// can never make A routable. The joint search must detect A's zero-choice
	// state directly instead of trying every B geometry and demand order.
	in.Demands[0], in.Demands[1] = in.Demands[1], in.Demands[0]
	in.MaxStates = 80
	blocker := boardComp{ID: "blocker", Designator: "Z1", X: 250, Y: 80, Layer: 1, Pads: []boardPad{{ID: "z1", Number: "1", Net: "BLOCK", Layer: 1, X: 250, Y: 80, W: 30, H: 30, Shape: []any{"RECT", 30.0, 30.0, 0.0}}}}
	s.Components = append(s.Components, blocker)
	rep, err := planPCBEscape(in, s)
	if err == nil || rep.Status != "incomplete" || rep.Budget.Exhausted {
		t.Fatalf("zero-choice demand should fail without exhausting equivalent orderings: %+v %v", rep, err)
	}
}

func TestEscapePlanAllowsExplicitBoundedPerpendicularFanout(t *testing.T) {
	in, s := escapeFixture()
	s.Components[0].Pads[1].Y = 160
	in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true, NCReason: "isolated local fanout test"}
	in.Demands[0].To = ""
	in.Demands[0].Toward = "J1.1"
	in.Demands[0].Congested = &layoutBBox{MinX: 20, MinY: 20, MaxX: 100, MaxY: 100}
	in.Demands[0].Exits = [][2]float64{{50, 110}}
	if rep, err := planPCBEscape(in, s); err == nil || rep.Status == "pass" {
		t.Fatal("undeclared initial radial detour passed")
	}
	in.Demands[0].MaxAwayMil = 3
	rep, err := planPCBEscape(in, s)
	if err != nil || rep.Status != "pass" || len(rep.Routes) != 1 {
		t.Fatalf("bounded perpendicular pad fanout failed: %+v %v", rep, err)
	}
	if err := verifyPCBEscape(in, rep, s); err != nil {
		t.Fatal(err)
	}
}

func TestEscapePlanFullRouteHonorsForcedBreakoutExit(t *testing.T) {
	in, s := escapeFixture()
	s.Components[0].Pads[1].Y = 160
	s.Components[1].Pads[0].Y = 40
	in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true, NCReason: "isolated forced breakout test"}
	in.Demands[0].Congested = &layoutBBox{MinX: 20, MinY: 70, MaxX: 100, MaxY: 100}
	in.Demands[0].Exits = [][2]float64{{50, 60}}
	rep, err := planPCBEscape(in, s)
	if err != nil || rep.Status != "pass" || len(rep.Routes) != 1 {
		t.Fatalf("forced full breakout failed: report=%+v %v", rep, err)
	}
	if !pcbEscapeRouteContainsPoint(rep.Routes[0].Points, [2]float64{50, 60}) {
		t.Fatalf("route skipped forced exit: %+v", rep.Routes[0])
	}
	bad := rep
	bad.Routes = append([]pcbModuleRoute(nil), rep.Routes...)
	bad.Routes[0].Points = crystal45Path([2]float64{50, 80}, [2]float64{250, 40})
	if err := verifyPCBEscapeGeometry(in, bad, s); err == nil || !strings.Contains(err.Error(), "forced breakout") {
		t.Fatalf("tampered forced exit passed: %v", err)
	}
}

func TestEscapePlanProofRejectsTamperingAndRuleChanges(t *testing.T) {
	in, s := escapeFixture()
	r, err := planPCBEscape(in, s)
	if err != nil {
		t.Fatal(err)
	}
	altered := in
	altered.MaxStates++
	if err := verifyPCBEscape(altered, r, s); err == nil {
		t.Fatal("stale intent accepted")
	}
	bad := r
	bad.Routes = append([]pcbModuleRoute(nil), r.Routes...)
	bad.Routes[1].Points = append([][2]float64(nil), r.Routes[1].Points...)
	bad.Routes[1].Points[1] = [2]float64{250, 80}
	if err := verifyPCBEscape(in, bad, s); err == nil {
		t.Fatal("tampered endpoint accepted")
	}
	changed := cloneEscapeSnapshot(s)
	changed.Rules.ClearanceMil = 35
	if err := verifyPCBEscape(in, r, changed); err == nil {
		t.Fatal("stale rule accepted")
	}
	r2, err := planPCBEscape(in, changed)
	if err == nil || r2.Status == "pass" {
		t.Fatal("new tight rule not enforced")
	}
}

func TestEscapePlanTranslatesAndRotatesMeasuredGeometry(t *testing.T) {
	in, s := escapeFixture()
	for _, transform := range []func(float64, float64) (float64, float64){func(x, y float64) (float64, float64) { return x + 123, y - 45 }, func(x, y float64) (float64, float64) { return -y, x }} {
		b := cloneEscapeSnapshot(s)
		for i := range b.Components {
			c := &b.Components[i]
			c.X, c.Y = transform(c.X, c.Y)
			for j := range c.Pads {
				p := &c.Pads[j]
				p.X, p.Y = transform(p.X, p.Y)
				p.Rotation += 90
			}
		}
		for i, p := range b.Outline.Points {
			x, y := transform(p[0], p[1])
			b.Outline.Points[i] = [2]float64{x, y}
		}
		b.Outline.BBox = pointsBBox(b.Outline.Points)
		r, err := planPCBEscape(in, b)
		if err != nil || r.Status != "pass" {
			t.Fatalf("transformed input failed %+v %v", r, err)
		}
		if err := verifyPCBEscape(in, r, b); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEscapePlanBudgetAndUnknownTransitionsNeverPass(t *testing.T) {
	in, s := escapeFixture()
	in.MaxStates = 1
	r, err := planPCBEscape(in, s)
	if err == nil || r.Status != "incomplete" || !r.Budget.Exhausted {
		t.Fatalf("budget falsely passed %+v %v", r, err)
	}
	in, s = escapeFixture()
	s.Components[1].Pads[0].Layer = 2
	in.Demands[0].MaxVias = 2
	r, err = planPCBEscape(in, s)
	if err == nil || r.Status != "incomplete" || !strings.Contains(err.Error(), "transition") {
		t.Fatalf("unsupported transition falsely passed %+v %v", r, err)
	}
}

func TestEscapePlanUsesTwoMeasuredViasWhenTopIsBlocked(t *testing.T) {
	in, s := escapeFixture()
	in.MaxStates = 200000
	in.Demands[0].MaxVias = 2
	in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true, NCReason: "isolated routing test"}
	s.Copper.Lines = []any{map[string]any{"primitiveId": "wall", "net": "WALL", "layer": float64(1), "startX": 150.0, "startY": 0.0, "endX": 150.0, "endY": 200.0, "lineWidth": 4.0}}
	r, err := planPCBEscape(in, s)
	if err != nil || r.Status != "pass" || len(r.Vias) != 2 || len(r.Routes) != 3 {
		t.Fatalf("two-via witness failed %+v %v", r, err)
	}
	if err := verifyPCBEscape(in, r, s); err != nil {
		t.Fatal(err)
	}
	bad := r
	bad.Vias = append([]pcbModuleVia(nil), r.Vias...)
	bad.Vias[0].X = 50
	bad.Vias[0].Y = 80
	if err := verifyPCBEscapeGeometry(in, bad, s); err == nil {
		t.Fatal("via-in-pad tampering passed")
	}
	in.RequiredTopNets = []string{"A"}
	if r, err := planPCBEscape(in, s); err == nil || r.Status != "fail" {
		t.Fatalf("strict net allowed vias %+v", r)
	}
	// Existing signal vias consume the whole-net budget even if elsewhere.
	in.RequiredTopNets = nil
	s.Copper.Vias = []any{map[string]any{"primitiveId": "existing", "net": "A", "x": 25.0, "y": 25.0, "holeDiameter": 8.0, "diameter": 16.0}}
	r, err = planPCBEscape(in, s)
	if err == nil || r.Status == "pass" {
		t.Fatalf("existing via ignored %+v", r)
	}
}

func TestEscapePlanGroundViaBudgetIsExplicit(t *testing.T) {
	in, s := escapeFixture()
	s.Copper.Vias = []any{map[string]any{"primitiveId": "existing", "net": "A", "x": 25.0, "y": 25.0, "holeDiameter": 8.0, "diameter": 16.0}}
	if r, err := planPCBEscape(in, s); err == nil || r.Status != "fail" {
		t.Fatal("signal default ignored existing net via")
	}
	in.Demands[0].Kind = "ground"
	r, err := planPCBEscape(in, s)
	if err != nil || r.Status != "pass" || r.ExistingViaCounts["A"] != 1 {
		t.Fatalf("explicit ground demand lost existing count %+v %v", r, err)
	}
}

func TestEscapePlanAcceptsIdentityBoundExistingGroundViasAsTheOnlyEscape(t *testing.T) {
	in, s := escapeFixture()
	in.Demands[1] = pcbEscapeDemand{ID: "b", From: "U1.2", NC: true, NCReason: "isolated existing EP via test"}
	in.Demands[0] = pcbEscapeDemand{
		ID: "a", Kind: "ground", From: "U1.1", Net: "A", WidthMil: 4, Layer: 1,
		ExistingViasOnly: true, ExistingViaIDs: []string{"ep-1", "ep-2"},
		ExistingViaInPadAnchors: map[string]string{"ep-1": "U1.1", "ep-2": "U1.1"},
	}
	s.Copper.Vias = []any{
		map[string]any{"primitiveId": "ep-1", "net": "A", "x": 48.0, "y": 80.0, "holeDiameter": 2.0, "diameter": 4.0},
		map[string]any{"primitiveId": "ep-2", "net": "A", "x": 52.0, "y": 80.0, "holeDiameter": 2.0, "diameter": 4.0},
	}
	rep, err := planPCBEscape(in, s)
	if err != nil || rep.Status != "pass" || len(rep.Routes) != 0 || len(rep.Vias) != 0 || len(rep.Witnesses) != 1 || len(rep.Witnesses[0].ExistingViaIDs) != 2 {
		t.Fatalf("existing-vias-only ground escape failed: %+v %v", rep, err)
	}
	if err := verifyPCBEscape(in, rep, s); err != nil {
		t.Fatal(err)
	}
	tampered := rep
	tampered.Witnesses = append([]pcbEscapeWitness(nil), rep.Witnesses...)
	tampered.Witnesses[0].ExistingViaIDs = []string{"ep-1"}
	if err := verifyPCBEscapeGeometry(in, tampered, s); err == nil {
		t.Fatal("missing declared existing EP via passed verification")
	}
	missing := cloneEscapeSnapshot(s)
	missing.Copper.Vias = missing.Copper.Vias[:1]
	if got, err := planPCBEscape(in, missing); err == nil || got.Status == "pass" {
		t.Fatal("missing live existing EP via passed planning")
	}
}

func TestEscapePlanKeepsDuplicateNonEndpointPadsAsObstacles(t *testing.T) {
	in, s := escapeFixture()
	extra := s.Components[1]
	extra.ID = "shield"
	extra.Designator = "SHIELD"
	extra.Pads[0].Net = "CASE"
	extra.Pads[0].Number = "1"
	extra.Pads[0].X = 150
	extra.Pads[0].Y = 30
	extra.Pads[1].Net = "CASE"
	extra.Pads[1].Number = "1"
	extra.Pads[1].X = 150
	extra.Pads[1].Y = 170
	// Deep-copy pads: changing the shielding inventory must not change J1.
	_, s = escapeFixture()
	s.Components = append(s.Components, extra)
	r, err := planPCBEscape(in, s)
	if err != nil || r.Status != "pass" {
		t.Fatalf("unreferenced duplicate pad numbers blocked valid proof: %+v %v", r, err)
	}
	in.Demands[0].To = "SHIELD.1"
	in.Demands[0].Net = "CASE"
	s.Components[0].Pads[0].Net = "CASE"
	if r, err := planPCBEscape(in, s); err == nil || r.Status != "incomplete" {
		t.Fatal("ambiguous endpoint silently selected")
	}
}

func TestEscapePlanTreatsNoWiresAsGeometryButNoPoursAsNonCopper(t *testing.T) {
	in, s := escapeFixture()
	region := map[string]any{"primitiveId": "region", "layer": 1.0, "ruleType": []any{7.0}, "geometryAvailable": true, "source": pointsPolygonSource([][2]float64{{120, 0}, {180, 0}, {180, 200}, {120, 200}})}
	s.Copper.Regions = []any{region}
	if r, err := planPCBEscape(in, s); err != nil || r.Status != "pass" {
		t.Fatalf("no-pours incorrectly blocks explicit track: %+v %v", r, err)
	}
	region["ruleType"] = []any{5.0}
	r, err := planPCBEscape(in, s)
	if err == nil || r.Status == "pass" {
		t.Fatal("no-wires region ignored")
	}
}

func TestEscapeGeometryVerifierAcceptsNewIDsButRejectsMovedCopper(t *testing.T) {
	in, s := escapeFixture()
	r, err := planPCBEscape(in, s)
	if err != nil {
		t.Fatal(err)
	}
	s.Components[0].ID = "new-component-pid"
	s.Components[0].Pads[0].ID = "new-pad-pid"
	if err := verifyPCBEscapeGeometry(in, r, s); err != nil {
		t.Fatalf("fresh unchanged geometry rejected after ID renewal: %v", err)
	}
	s.Copper.Lines = []any{map[string]any{"primitiveId": "new-blocker", "net": "WALL", "layer": 1.0, "startX": 100.0, "startY": 70.0, "endX": 100.0, "endY": 90.0, "lineWidth": 4.0}}
	if err := verifyPCBEscapeGeometry(in, r, s); err == nil {
		t.Fatal("fresh actual copper conflict ignored")
	}
}

func TestEscapePlanBacktracksEarlierPathForJointSolution(t *testing.T) {
	in, s := escapeFixture()
	in.MaxStates = 300000
	s.Components[0].Pads[1].X = 150
	s.Components[0].Pads[1].Y = 50
	s.Components[1].Pads[1].X = 150
	s.Components[1].Pads[1].Y = 110
	// A initially takes y=80 across the board. B's finite x=100..200 escape
	// range cannot pass that stroke; a longer A corridor makes both possible.
	ctx, _, err := preparePCBEscape(in, s)
	if err != nil {
		t.Fatal(err)
	}
	budget := pcbEscapeBudget{MaxStates: in.MaxStates}
	first := pcbEscapeCandidates(ctx, in, in.Demands[0], nil, &budget)
	if len(first) < 2 || len(first[0].Points) != 2 {
		t.Fatalf("fixture lacks direct and alternate A paths %+v", first)
	}
	blocked := pcbEscapeCandidates(ctx, in, in.Demands[1], []pcbModuleRoute{first[0]}, &budget)
	if len(blocked) != 0 {
		t.Fatalf("fixture did not force joint backtracking %+v", blocked)
	}
	r, err := planPCBEscape(in, s)
	if err != nil || r.Status != "pass" {
		t.Fatalf("joint backtracking failed %+v %v", r, err)
	}
	if len(r.Routes[0].Points) <= 2 {
		t.Fatal("A was not rerouted despite direct-route conflict")
	}
	if err := verifyPCBEscape(in, r, s); err != nil {
		t.Fatal(err)
	}
}

func TestEscapeExactRotatedPadDoesNotUseNominalBBoxAsProof(t *testing.T) {
	in, s := escapeFixture()
	in.MaxDetourMil = 0
	s.Components = append(s.Components, boardComp{ID: "obstacle", Designator: "BLOCK", Layer: 1, Pads: []boardPad{{ID: "block-pad", Number: "1", Net: "CASE", Layer: 1, X: 150, Y: 110, W: 100, H: 8, Rotation: 90, Shape: []any{"RECT", 100.0, 8.0, 0.0}}}})
	// Nominal H=8 would incorrectly skip the y=80 path; rotated actual copper
	// spans y=60..160, so both routes are blocked.
	r, err := planPCBEscape(in, s)
	if err == nil || r.Status == "pass" {
		t.Fatal("rotated pad obstacle was skipped using nominal width/height")
	}
}

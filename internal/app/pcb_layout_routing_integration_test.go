package app

import (
	"strings"
	"testing"
)

func routingIntegrationFixture(t *testing.T) (pcbLayoutPlanInput, *boardSnapshot, pcbLayoutCandidate) {
	t.Helper()
	routing, s := escapeFixture()
	s.Components[0].BBox = &layoutBBox{MinX: 40, MinY: 70, MaxX: 60, MaxY: 130}
	s.Components[1].BBox = &layoutBBox{MinX: 240, MinY: 70, MaxX: 260, MaxY: 130}
	s.SemanticSHA256, _ = moduleCheckSemanticSHA256(s)
	in := pcbLayoutPlanInput{SchemaVersion: 3, Units: "mil", CoordinateSemantic: "footprint-anchor", MinGapMil: 5, Routing: &routing, Modules: []pcbLayoutModuleSpec{{ID: "connector", Strategy: "rigid", CopperPolicy: "module-owned", AnchorRef: "J1", Members: []pcbLayoutMemberSpec{{Ref: "J1"}}, Search: pcbLayoutSearchSpec{OffsetsMil: []pcbLayoutOffset{{XMil: 10, YMil: 0}}}}}}
	report, err := planPCBLayoutModule(in, s, "connector", 1)
	if err != nil || len(report.Candidates) != 1 {
		t.Fatalf("routing integration fixture failed %+v %v", report, err)
	}
	return in, s, report.Candidates[0]
}

func TestRoutingLayoutCandidateBindsFinalProjectionAndNeverAppliesReservations(t *testing.T) {
	in, s, c := routingIntegrationFixture(t)
	if c.Escape == nil || c.SchemaVersion != 3 {
		t.Fatal("v3 omitted escape witness")
	}
	projected, err := projectPCBLayoutCandidate(s, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPCBEscape(*in.Routing, *c.Escape, projected); err != nil {
		t.Fatalf("final projected geometry/hash not sealed together: %v", err)
	}
	if err := verifyPCBEscape(*in.Routing, *c.Escape, s); err == nil {
		t.Fatal("unmoved baseline accepted as candidate projection")
	}
	for _, step := range c.Apply.Steps {
		if strings.Contains(step.Action, "line.create") || strings.Contains(step.Action, "via.create") {
			t.Fatalf("planning reservation leaked into executable copper: %s", step.Action)
		}
	}
}

func TestRoutingProjectionIncludesDeclaredNoWiresRegion(t *testing.T) {
	in, s, c := routingIntegrationFixture(t)
	c.Bundle.Regions = []pcbModuleRegion{{ID: "no-route-wall", Layer: 1, RuleTypes: []string{"no-wires"}, Points: [][2]float64{{120, 20}, {180, 20}, {180, 180}, {120, 180}}}}
	projected, err := projectPCBLayoutCandidate(s, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPCBEscapeGeometry(*in.Routing, *c.Escape, projected); err == nil || !strings.Contains(err.Error(), "clearance/board/region") {
		t.Fatalf("new no-wires region must fail for a concrete path conflict, not unknown/missing data: %v", err)
	}
}

func TestRoutingProjectionNoPoursStillAllowsExplicitReservations(t *testing.T) {
	in, s, c := routingIntegrationFixture(t)
	c.Bundle.Regions = []pcbModuleRegion{{ID: "no-pour", Layer: 1, RuleTypes: []string{"no-pours"}, Points: [][2]float64{{120, 20}, {180, 20}, {180, 180}, {120, 180}}}}
	projected, err := projectPCBLayoutCandidate(s, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPCBEscapeGeometry(*in.Routing, *c.Escape, projected); err != nil {
		t.Fatalf("no-pours projection must retain valid region data without prohibiting explicit copper: %v", err)
	}
}

func TestRoutingIndependentDeclarationRejectsUnownedCopperReplacement(t *testing.T) {
	in, s, c := routingIntegrationFixture(t)
	c.Reflow = &pcbReflowCandidate{Copper: pcbReflowCopperPlan{ReplacePrimitiveIDs: []string{"unrelated-existing-copper"}}}
	c.Bundle.ReflowReplaceIDs = []string{"unrelated-existing-copper"}
	if err := verifyPCBReflowDeclaration(c, in, s); err == nil {
		t.Fatal("undeclared copper replacement accepted using candidate as its own authority")
	}
}

func TestRoutingCrystalCandidateRechecksEscapesAfterProtection(t *testing.T) {
	in, s := crystalGuardFixture()
	in.SchemaVersion = 3
	s.Rules.Source = "live"
	s.Rules.TrackWidthMinMil = 8
	s.CopperLayers = 2
	in.Routing = &pcbRoutingIntent{Owners: []string{"U6"}, RequiredTopNets: []string{"OSC_IN", "OSC_OUT"}, StepMil: 10, MaxDetourMil: 160, MaxStates: 200000, Demands: []pcbEscapeDemand{
		{ID: "osc-in", From: "U6.2", To: "X1.1", Net: "OSC_IN", WidthMil: 8, Layer: 1},
		{ID: "osc-out", From: "U6.3", To: "X1.3", Net: "OSC_OUT", WidthMil: 8, Layer: 1},
		{ID: "ground", Kind: "ground", From: "U6.33", To: "X1.2", Net: "GND", WidthMil: 8, Layer: 1},
	}}
	report, err := planPCBLayoutModule(in, s, "crystal-guard", 1)
	if err != nil || len(report.Candidates) != 1 {
		t.Fatalf("crystal routing integration failed: %v; rejections=%+v", err, report.Rejected)
	}
	c := report.Candidates[0]
	p, err := projectPCBLayoutCandidate(s, c)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyPCBEscape(*in.Routing, *c.Escape, p); err != nil {
		t.Fatalf("protected crystal invalidated joint witness: %v", err)
	}
	if len(c.Bundle.SignalRoutes) != 4 || len(c.Bundle.Regions) != 2 || len(c.Bundle.Vias) == 0 {
		t.Fatal("escape integration dropped crystal objects")
	}
}

package app

import (
	"strings"
	"testing"
)

// The `via-bond` ERROR rule (and its tests) were removed after pro-api-sdk#31
// was confirmed to be our misdiagnosis — track↔via DOES register as connected;
// see the header note in pcb_check_bond.go.

// ── floating-track-island ───────────────────────────────────────────────────

func TestFindFloatingTrackIslands(t *testing.T) {
	// Two touching tracks, no pad anywhere near → one island finding.
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6},
		{ID: "t2", Net: "N1", Layer: 1, X1: 100, Y1: 0, X2: 100, Y2: 100, Width: 6},
	}
	got := findFloatingTrackIslands(tracks, nil, nil, nil, nil)
	if len(got) != 1 {
		t.Fatalf("got %d findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Type != "floating-track-island" || f.Level != "WARN" {
		t.Errorf("bad type/level: %+v", f)
	}
	if len(f.Primitives) != 2 {
		t.Errorf("island should list both tracks: %+v", f.Primitives)
	}

	// Same island anchored to a same-net pad at one endpoint → silent.
	pads := []pcbPadP{{Designator: "R1", Net: "N1", Layer: 1, X: 0, Y: 0}}
	if got := findFloatingTrackIslands(tracks, nil, pads, nil, nil); len(got) != 0 {
		t.Errorf("pad-anchored island flagged: %+v", got)
	}

	// Same-net pour on the island's layer → bonded, silent.
	pours := []pcbPourP{{ID: "p1", Net: "N1", Layer: 1}}
	if got := findFloatingTrackIslands(tracks, nil, nil, pours, nil); len(got) != 0 {
		t.Errorf("pour-covered island flagged: %+v", got)
	}
}

func TestFindFloatingTrackIslandsSingleAndViaBridge(t *testing.T) {
	// A single floating track is dangling-end's territory — no island finding.
	single := []pcbTrack{{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6}}
	if got := findFloatingTrackIslands(single, nil, nil, nil, nil); len(got) != 0 {
		t.Errorf("single track must be left to dangling-end: %+v", got)
	}

	// Two tracks on DIFFERENT layers joined only by a via → one island (via
	// bridges); still floating (no pad) → one finding spanning both.
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 100, Y2: 0, Width: 6},
		{ID: "t2", Net: "N1", Layer: 2, X1: 100, Y1: 0, X2: 200, Y2: 0, Width: 6},
	}
	vias := []pcbViaP{{ID: "v1", Net: "N1", X: 100, Y: 0, Dia: 24}}
	got := findFloatingTrackIslands(tracks, vias, nil, nil, nil)
	if len(got) != 1 || len(got[0].Primitives) != 2 {
		t.Fatalf("via-bridged island wrong: %+v", got)
	}

	// Anchoring EITHER side's endpoint to a pad silences the whole island.
	pads := []pcbPadP{{Designator: "U1", Net: "N1", Layer: 2, X: 200, Y: 0}}
	if got := findFloatingTrackIslands(tracks, vias, pads, nil, nil); len(got) != 0 {
		t.Errorf("pad on the far side should anchor the island: %+v", got)
	}
}

// beautify fragments a net into track→arc→track. The arc must bridge the two track
// segments into one island (else each segment reports separately), and a pad anchor
// on either segment must then silence the whole bridged island.
func TestFindFloatingTrackIslandsArcBridge(t *testing.T) {
	// Two segments joined ONLY by an arc (endpoints at 90,0 and 100,10). Neither
	// touches the other directly, so without the arc they'd be two separate groups
	// — but a single floating track is left to dangling-end, so the pre-fix result
	// is 0 island findings, not a false island. With the arc they merge into one.
	tracks := []pcbTrack{
		{ID: "t1", Net: "N1", Layer: 1, X1: 0, Y1: 0, X2: 90, Y2: 0, Width: 6},
		{ID: "t2", Net: "N1", Layer: 1, X1: 100, Y1: 10, X2: 100, Y2: 100, Width: 6},
	}
	arcs := []pcbArc{{ID: "a1", Net: "N1", Layer: 1, X1: 90, Y1: 0, X2: 100, Y2: 10}}

	// Bridged into ONE floating island (no pad anywhere) → a single 2-track finding.
	got := findFloatingTrackIslands(tracks, nil, nil, nil, arcs)
	if len(got) != 1 || len(got[0].Primitives) != 2 {
		t.Fatalf("arc must bridge the two segments into one island: %+v", got)
	}

	// A pad on ONE segment now anchors the WHOLE bridged island → silent.
	pads := []pcbPadP{{Designator: "R1", Net: "N1", Layer: 1, X: 100, Y: 100}}
	if got := findFloatingTrackIslands(tracks, nil, pads, nil, arcs); len(got) != 0 {
		t.Errorf("pad on arc-bridged segment should anchor the island: %+v", got)
	}

	// An arc on a DIFFERENT layer must not bridge (no via = no layer change).
	otherLayer := []pcbArc{{ID: "a1", Net: "N1", Layer: 2, X1: 90, Y1: 0, X2: 100, Y2: 10}}
	if got := findFloatingTrackIslands(tracks, nil, nil, nil, otherLayer); len(got) != 0 {
		t.Errorf("cross-layer arc must not bridge; both stay single (dangling-end territory): %+v", got)
	}
}

// ── dangling-end via-area anchoring upgrade ─────────────────────────────────

func TestDanglingEndViaAreaAnchor(t *testing.T) {
	// Endpoint inside a same-net via's copper but off its center (the round-#1
	// false positive on stubs official DRC accepted) → anchored, no finding.
	tracks := []pcbTrack{{ID: "t1", Net: "GND", Layer: 1, X1: 0, Y1: 0, X2: 192, Y2: 0, Width: 10}}
	vias := []pcbViaP{{ID: "v1", Net: "GND", X: 200, Y: 0, Dia: 24}}
	pads := []pcbPadP{{Designator: "C1", Net: "GND", Layer: 1, X: 0, Y: 0}} // anchors the left end
	got := findDanglingEnds(tracks, vias, pads, nil)
	if len(got) != 0 {
		t.Errorf("same-net endpoint inside via copper must anchor: %+v", got)
	}

	// A FOREIGN-net via at the same off-center spot must NOT anchor.
	foreign := []pcbViaP{{ID: "v1", Net: "+5V", X: 200, Y: 0, Dia: 24}}
	got = findDanglingEnds(tracks, foreign, pads, nil)
	if len(got) != 1 {
		t.Errorf("foreign via off-center must not anchor: %+v", got)
	}
}

func TestDanglingEndAnchorsObservedU2LargeRectPads(t *testing.T) {
	// Saved/reloaded 260919 LDO evidence. The old fixed 30mil center radius
	// falsely marked both U2 endpoints dangling even though they lie in RECT
	// copper (U2.4 is almost 71mil from its center).
	u24 := netPathRectPad("u2-4", "U2", "4", "+3V3", 1, 2061.6, 400, 97.2, 141.7)
	c5 := netPathRectPad("c5-1", "C5", "1", "+3V3", 1, 2050, 587.6, 40, 20)
	output := []pcbTrack{{ID: "3V3-2", Net: "+3V3", Layer: 1, X1: 2050, Y1: 470, X2: 2050, Y2: 587.6, Width: 20}}
	if got := findDanglingEnds(output, nil, []pcbPadP{u24, c5}, nil); len(got) != 0 {
		t.Fatalf("U2.4 endpoint inside large RECT copper must anchor: %+v", got)
	}

	u21 := netPathRectPad("u2-1", "U2", "1", "GND", 1, 1838.4, 490.6, 97.2, 38.6)
	c4 := netPathRectPad("c4-2", "C4", "2", "GND", 1, 1773, 460, 40, 20)
	ground := []pcbTrack{{ID: "GND-I5", Net: "GND", Layer: 1, X1: 1773, Y1: 460, X2: 1803.6, Y2: 490.6, Width: 20}}
	if got := findDanglingEnds(ground, nil, []pcbPadP{u21, c4}, nil); len(got) != 0 {
		t.Fatalf("U2.1 off-center endpoint inside RECT copper must anchor: %+v", got)
	}
}

func TestDanglingEndRotatedRectRejectsAABBCorner(t *testing.T) {
	pad := netPathRectPad("p1", "U1", "1", "SIG", 1, 0, 0, 40, 10)
	pad.Rotation = 45
	contactRadius := 0.1 + pcbCoincEps // 0.2mil track radius + established endpoint tolerance
	if pcbPadAnchorsPoint(pad, 16, 16, 1, "SIG", contactRadius) {
		t.Fatal("rotated RECT AABB corner was treated as pad copper")
	}
	if !pcbPadAnchorsPoint(pad, 14, 14, 1, "SIG", contactRadius) {
		t.Fatal("point on rotated RECT copper did not anchor")
	}
}

func TestDanglingEndLegacyPadExtentIsConservativeAndReported(t *testing.T) {
	// Connector 1.5.1 lacks source shape. Cardinal width/height use an ellipse
	// contained in native RECT/OVAL/ELLIPSE pads; track stroke contact still proves
	// the observed U2.4 landing without accepting an AABB corner.
	legacy := pcbPadP{ID: "u2-4", Designator: "U2", Number: "4", Net: "+3V3", Layer: 1, X: 2061.6, Y: 400, W: 97.2, H: 141.7, Rotation: 180}
	if !pcbPadAnchorsPoint(legacy, 2050, 470, 1, "+3V3", 10+pcbCoincEps) {
		t.Fatal("conservative legacy ellipse did not recognize the observed track-stroke landing")
	}
	legacy.Rotation = 45
	legacy.W, legacy.H = 40, 10
	legacy.X, legacy.Y = 0, 0
	if pcbPadAnchorsPoint(legacy, 16, 16, 1, "+3V3", 0.1+pcbCoincEps) {
		t.Fatal("legacy non-cardinal AABB corner was treated as copper")
	}
	rep := analyzePcbCheck([]pcbPadP{legacy}, nil, nil, 0)
	if !strings.Contains(strings.Join(rep.Limitations, "\n"), "conservative legacy") {
		t.Fatalf("legacy geometry boundary missing from report: %v", rep.Limitations)
	}
}

// beautify rounds a corner into track→arc→track: the two tracks now terminate on the
// arc's endpoints, not on each other. A dangling-end check that ignores arcs would
// fabricate a false stub at every rounded corner — with arcs it stays clean.
func TestDanglingEndArcAnchor(t *testing.T) {
	// Corner at (100,0): horizontal track in from the left, vertical track down —
	// beautify replaces the shared vertex with a small arc whose endpoints sit just
	// before each track end. Both tracks are anchored at their far ends by pads.
	tracks := []pcbTrack{
		{ID: "h", Net: "SIG", Layer: 1, X1: 0, Y1: 0, X2: 90, Y2: 0, Width: 10},
		{ID: "v", Net: "SIG", Layer: 1, X1: 100, Y1: -10, X2: 100, Y2: -100, Width: 10},
	}
	pads := []pcbPadP{
		{Designator: "U1", Net: "SIG", Layer: 1, X: 0, Y: 0},
		{Designator: "U2", Net: "SIG", Layer: 1, X: 100, Y: -100},
	}
	arcs := []pcbArc{{ID: "a", Net: "SIG", Layer: 1, X1: 90, Y1: 0, X2: 100, Y2: -10}}

	// Without the arc, the inner ends (90,0) and (100,-10) dangle → 2 findings.
	if got := findDanglingEnds(tracks, nil, pads, nil); len(got) != 2 {
		t.Fatalf("precondition: without arc want 2 dangling, got %d", len(got))
	}
	// With the arc, both inner ends anchor on its endpoints → 0 findings.
	if got := findDanglingEnds(tracks, nil, pads, arcs); len(got) != 0 {
		t.Errorf("arc endpoints must anchor beautified track ends: %+v", got)
	}

	// A DIFFERENT-layer arc must NOT anchor (no via = no layer transition).
	otherLayer := []pcbArc{{ID: "a", Net: "SIG", Layer: 2, X1: 90, Y1: 0, X2: 100, Y2: -10}}
	if got := findDanglingEnds(tracks, nil, pads, otherLayer); len(got) != 2 {
		t.Errorf("arc on another layer must not anchor: got %d, want 2", len(got))
	}
}

package pcblayout_test

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

func boardFixture() pcbmodel.Board {
	return pcbmodel.Board{SchemaVersion: 1, Units: "mil",
		Outline: pcbmodel.Outline{Source: "polygon", BBox: pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 200}, Points: []pcbmodel.Point{{0, 0}, {300, 0}, {300, 200}, {0, 200}}},
		Stackup: pcbmodel.Stackup{Copper: []pcbmodel.Layer{{ID: 1, Name: "TOP", Role: "signal"}, {ID: 2, Name: "BOTTOM", Role: "signal"}}},
		Rules:   pcbmodel.Rules{Source: "fixture", Clearance: 5, TrackTrack: 5, CopperToEdge: 5, TrackWidthMinimum: 4, ViaDiameter: 16, ViaDrill: 8},
		Components: []pcbmodel.Component{
			{Ref: "U1", Side: 1, Anchor: pcbmodel.Point{50, 100}, BBox: pcbmodel.BBox{MinX: 40, MinY: 80, MaxX: 60, MaxY: 120}, Pads: []pcbmodel.Pad{{Number: "1", Net: "A", Layer: 1, Center: pcbmodel.Point{50, 90}, Width: 10, Height: 10, Shape: "rect"}}},
			{Ref: "C1", Side: 1, Anchor: pcbmodel.Point{80, 100}, BBox: pcbmodel.BBox{MinX: 70, MinY: 90, MaxX: 90, MaxY: 110}, Pads: []pcbmodel.Pad{{Number: "1", Net: "A", Layer: 1, Center: pcbmodel.Point{75, 100}, Width: 10, Height: 10, Shape: "rect"}}},
			{Ref: "J1", Side: 1, Anchor: pcbmodel.Point{250, 100}, Locked: true, BBox: pcbmodel.BBox{MinX: 240, MinY: 80, MaxX: 260, MaxY: 120}, Pads: []pcbmodel.Pad{{Number: "1", Net: "A", Layer: 1, Center: pcbmodel.Point{250, 90}, Width: 10, Height: 10, Shape: "rect"}}},
		},
		Tracks:  []pcbmodel.Track{{ID: "owned", Owner: "core", Net: "A", Layer: 1, Width: 4, Points: []pcbmodel.Point{{50, 90}, {75, 100}}}},
		Regions: []pcbmodel.Region{{ID: "owned-copper", Kind: "copper", Owner: "core", Net: "A", Layer: 1, Points: []pcbmodel.Point{{45, 85}, {55, 85}, {55, 95}, {45, 95}}}},
	}
}

func TestGenerateMovesWholeGroupAndOwnedCopperOnFixedSide(t *testing.T) {
	b := boardFixture()
	req := pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, FixedRefs: []string{"J1"}, Groups: []pcblayout.Group{{ID: "core", AnchorRef: "U1", Refs: []string{"U1", "C1"}, Offsets: []pcblayout.Offset{{}, {X: 0, Y: 40}}, AllowedRotationDeltas: []float64{0}, InternalTrackIDs: []string{"owned"}, InternalRegionIDs: []string{"owned-copper"}}}}
	rep, err := pcblayout.Generate(b, req)
	if err != nil || len(rep.Candidates) != 2 {
		t.Fatalf("generate: %v %+v", err, rep)
	}
	moved := rep.Candidates[1]
	u, _ := moved.Board.Component("U1")
	c, _ := moved.Board.Component("C1")
	j, _ := moved.Board.Component("J1")
	if u.Anchor != (pcbmodel.Point{50, 140}) || c.Anchor != (pcbmodel.Point{80, 140}) || j.Anchor != (pcbmodel.Point{250, 100}) {
		t.Fatalf("group/fixed poses: U=%v C=%v J=%v", u.Anchor, c.Anchor, j.Anchor)
	}
	if u.Side != 1 || c.Side != 1 || moved.Board.Tracks[0].Points[0] != (pcbmodel.Point{50, 130}) || moved.Board.Regions[0].Points[0] != (pcbmodel.Point{45, 125}) {
		t.Fatalf("side/copper changed incorrectly: %+v %+v %+v", u, moved.Board.Tracks[0], moved.Board.Regions[0])
	}
}

func TestGenerateRejectsNewOverlapAndKeepout(t *testing.T) {
	b := boardFixture()
	req := pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, Groups: []pcblayout.Group{{ID: "core", AnchorRef: "U1", Refs: []string{"U1", "C1"}, Offsets: []pcblayout.Offset{{}, {X: 170}}}}, Keepouts: []pcblayout.Keepout{{ID: "opening", BBox: pcbmodel.BBox{MinX: 35, MinY: 130, MaxX: 95, MaxY: 170}, Layers: []int{1}}}}
	rep, err := pcblayout.Generate(b, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 1 || len(rep.Rejected) == 0 {
		t.Fatalf("illegal placement survived: %+v", rep)
	}
}

func TestMovingWholeGroupCannotCarryExistingMemberOverlap(t *testing.T) {
	b := boardFixture()
	// The two members already overlap. A rigid translation preserves that
	// defect, so moving both must not turn it into an accepted layout.
	b.Components[1].Anchor = pcbmodel.Point{65, 100}
	b.Components[1].BBox = pcbmodel.BBox{MinX: 55, MinY: 90, MaxX: 75, MaxY: 110}
	b.Components[1].Pads[0].Center = pcbmodel.Point{60, 100}
	req := pcblayout.Request{MinGap: 5, MaxStates: 20, MaxCandidates: 3,
		Groups: []pcblayout.Group{{ID: "overlapping-module", AnchorRef: "U1", Refs: []string{"U1", "C1"}, Offsets: []pcblayout.Offset{{}, {Y: 40}}}}}
	rep, err := pcblayout.Generate(b, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Candidates) != 1 || len(rep.Candidates[0].Moves) != 0 {
		t.Fatalf("moved group kept its original overlap: %+v", rep.Candidates)
	}
	found := false
	for _, finding := range rep.Rejected {
		if finding.Type == "overlap" && finding.Objects[0] == "C1" && finding.Objects[1] == "U1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing explicit carried-over overlap finding: %+v", rep.Rejected)
	}
}

func TestLayoutOrderingPrefersNoMoveThenMinimumDistance(t *testing.T) {
	b := boardFixture()
	req := pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, Groups: []pcblayout.Group{{ID: "core", AnchorRef: "U1", Refs: []string{"U1", "C1"}, Offsets: []pcblayout.Offset{{X: 0, Y: 40}, {}, {X: 0, Y: 20}}}}}
	rep, err := pcblayout.Generate(b, req)
	if err != nil || len(rep.Candidates) != 3 {
		t.Fatalf("generate: %v %+v", err, rep)
	}
	if rep.Candidates[0].Metrics.MovedGroups != 0 || rep.Candidates[1].Metrics.TotalDistance != 20 || rep.Candidates[2].Metrics.TotalDistance != 40 {
		t.Fatalf("wrong candidate order: %+v", rep.Candidates)
	}
}

func TestOppositeSideComponentsDoNotCreateAssemblyOverlap(t *testing.T) {
	b := boardFixture()
	back := b.Components[0]
	back.Ref = "B1"
	back.Side = pcbmodel.LayerBottom
	b.Components = append(b.Components, back)
	req := pcblayout.Request{MinGap: 5, MaxStates: 10, MaxCandidates: 1}
	for _, finding := range pcblayout.Check(b, req) {
		if finding.Type == "overlap" && (finding.Objects[0] == "B1" || finding.Objects[1] == "B1") {
			t.Fatalf("opposite-side component reported as overlap: %+v", finding)
		}
	}
}

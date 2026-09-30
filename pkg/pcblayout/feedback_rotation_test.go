package pcblayout_test

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

func TestFeedbackCanRotateAfterMovingAwayFromFixedObject(t *testing.T) {
	b := boardFixture()
	b.Components = append(b.Components, pcbmodel.Component{Ref: "F1", Side: 1, Locked: true, Anchor: pcbmodel.Point{50, 145}, BBox: pcbmodel.BBox{MinX: 40, MinY: 135, MaxX: 60, MaxY: 155}})
	g := pcblayout.Group{ID: "core", AnchorRef: "U1", Refs: []string{"U1", "C1"},
		FixedAxes: []string{"y"}, AllowedRotationDeltas: []float64{0, 90},
		TranslationSearch: &pcblayout.TranslationSearch{Step: 20, MaxDistance: 80},
		InternalTrackIDs:  []string{"owned"}, InternalRegionIDs: []string{"owned-copper"}}
	r := pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, FixedRefs: []string{"J1", "F1"}, Groups: []pcblayout.Group{g}}
	initial, err := pcblayout.Generate(b, r)
	if err != nil || len(initial.Candidates) != 1 || len(initial.Candidates[0].Moves) != 0 {
		t.Fatalf("rotation at origin must be rejected: %v %+v", err, initial)
	}
	move := pcblayout.Move{Group: g.ID, Refs: g.Refs, Pivot: pcbmodel.Point{50, 100}, DX: 60}
	shifted, err := pcblayout.Project(b, r, []pcblayout.Move{move})
	if err != nil {
		t.Fatal(err)
	}
	current := pcblayout.Candidate{Board: shifted, Moves: []pcblayout.Move{move}}
	found := false
	for _, c := range pcblayout.Neighbors(b, r, current, []string{"U1"}, "route feedback") {
		if _, err := pcblayout.Project(b, r, c.Moves); err != nil {
			t.Fatalf("neighbor escaped declared transforms: %v", err)
		}
		m := c.Moves[0]
		if m.DX != 60 || m.DY != 0 || m.RotationDelta != 90 {
			continue
		}
		found = true
		if len(pcblayout.ChangedFindings(b, c.Board, r)) != 0 {
			t.Fatal("rotation after translation should clear the fixed object")
		}
		u, _ := c.Board.Component("U1")
		cap, _ := c.Board.Component("C1")
		if u.Anchor != (pcbmodel.Point{110, 100}) || cap.Anchor != (pcbmodel.Point{110, 130}) || u.Rotation != 90 || cap.Rotation != 90 {
			t.Fatalf("must rebuild the whole group from original anchor, not compound transforms: U=%+v C=%+v", u, cap)
		}
		if c.Board.Tracks[0].Points[0] != (pcbmodel.Point{120, 100}) || c.Board.Regions[0].Points[0] != (pcbmodel.Point{125, 95}) {
			t.Fatal("owned internal geometry did not follow the same transform")
		}
	}
	if !found {
		t.Fatal("feedback never tried an allowed rotation at the translated position")
	}
	r.Groups[0].FixedAxes = []string{"y", "rotation"}
	for _, c := range pcblayout.Neighbors(b, r, current, []string{"U1"}, "route feedback") {
		for _, m := range c.Moves {
			if m.RotationDelta != 0 {
				t.Fatal("rotation feedback violated the fixed axis")
			}
		}
	}
	if got := pcblayout.Neighbors(b, r, current, []string{"J1"}, "route feedback"); len(got) != 0 {
		t.Fatal("feedback moved a group unrelated to the observed conflict")
	}
}

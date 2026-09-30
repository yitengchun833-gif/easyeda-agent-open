package pcbmodel_test

import (
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

func TestTransformAsymmetricFootprintMovesPadsAndOwnedCopperTogether(t *testing.T) {
	c := pcbmodel.Component{Ref: "U1", Side: pcbmodel.LayerTop, Anchor: pcbmodel.Point{100, 100}, Rotation: 0,
		BBox: pcbmodel.BBox{MinX: 90, MinY: 80, MaxX: 130, MaxY: 120},
		Pads: []pcbmodel.Pad{{Number: "1", Net: "SIG", Layer: pcbmodel.LayerTop, Center: pcbmodel.Point{120, 100}, Width: 10, Height: 20, Shape: "rect"}}}
	tr := pcbmodel.Transform{Pivot: pcbmodel.Point{100, 100}, DX: 50, DY: 20, RotationDelta: 90}
	got := tr.Component(c)
	if got.Anchor != (pcbmodel.Point{150, 120}) || got.BBox != (pcbmodel.BBox{MinX: 130, MinY: 110, MaxX: 170, MaxY: 150}) {
		t.Fatalf("component transform = %+v", got)
	}
	if got.Pads[0].Center != (pcbmodel.Point{150, 140}) || got.Pads[0].Width != 10 || got.Pads[0].Height != 20 || got.Pads[0].Rotation != 90 {
		t.Fatalf("pad transform = %+v", got.Pads[0])
	}
	track := tr.Track(pcbmodel.Track{Net: "SIG", Layer: 1, Width: 8, Points: []pcbmodel.Point{{120, 100}, {140, 100}}})
	if track.Points[0] != got.Pads[0].Center || track.Points[1] != (pcbmodel.Point{150, 160}) {
		t.Fatalf("owned copper transform = %+v", track.Points)
	}
}

func TestConcaveOutlineRejectsBBoxCrossingNotVisibleAtCorners(t *testing.T) {
	o := pcbmodel.Outline{Source: "polygon", BBox: pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}, Points: []pcbmodel.Point{{0, 0}, {100, 0}, {100, 100}, {60, 100}, {60, 40}, {40, 40}, {40, 100}, {0, 100}}}
	if o.ContainsBBox(pcbmodel.BBox{MinX: 30, MinY: 30, MaxX: 70, MaxY: 90}) {
		t.Fatal("concave notch crossing accepted from corner-only containment")
	}
}

func TestBoardValidateAcceptsMeasuredExistingEdgeConnector(t *testing.T) {
	b := pcbmodel.Board{
		SchemaVersion: 1,
		Units:         "mil",
		Outline: pcbmodel.Outline{
			Source: "polygon",
			BBox:   pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100},
			Points: []pcbmodel.Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}},
		},
		Stackup: pcbmodel.Stackup{Copper: []pcbmodel.Layer{
			{ID: pcbmodel.LayerTop, Name: "TOP", Role: "signal"},
			{ID: pcbmodel.LayerBottom, Name: "BOTTOM", Role: "signal"},
		}},
		Rules: pcbmodel.Rules{Clearance: 5, TrackTrack: 5, CopperToEdge: 5, TrackWidthMinimum: 4, ViaDiameter: 16, ViaDrill: 8, Source: "fixture"},
		Components: []pcbmodel.Component{{
			Ref: "J1", Side: pcbmodel.LayerTop, Anchor: pcbmodel.Point{5, 50},
			BBox: pcbmodel.BBox{MinX: -10, MinY: 40, MaxX: 20, MaxY: 60},
			Pads: []pcbmodel.Pad{{Number: "1", Net: "SIG", Layer: pcbmodel.LayerTop, Center: pcbmodel.Point{5, 50}, Width: 10, Height: 10, Shape: "rect"}},
		}},
	}
	if err := b.Validate(true); err != nil {
		t.Fatalf("measured baseline edge connector was rejected as malformed: %v", err)
	}
}

func TestBoardValidateKeepsRepeatedPhysicalPadsButEndpointIsAmbiguous(t *testing.T) {
	b := pcbmodel.Board{
		SchemaVersion: 1,
		Units:         "mil",
		Outline:       pcbmodel.Outline{Source: "polygon", BBox: pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}, Points: []pcbmodel.Point{{0, 0}, {100, 0}, {100, 100}, {0, 100}}},
		Stackup:       pcbmodel.Stackup{Copper: []pcbmodel.Layer{{ID: pcbmodel.LayerTop, Role: "signal"}, {ID: pcbmodel.LayerBottom, Role: "signal"}}},
		Rules:         pcbmodel.Rules{Clearance: 5, TrackTrack: 5, TrackWidthMinimum: 4, ViaDiameter: 16, ViaDrill: 8, Source: "fixture"},
		Components: []pcbmodel.Component{{Ref: "USB1", Side: pcbmodel.LayerTop, Anchor: pcbmodel.Point{50, 50}, BBox: pcbmodel.BBox{MinX: 40, MinY: 40, MaxX: 60, MaxY: 60}, Pads: []pcbmodel.Pad{
			{Number: "13", Net: "GND", Layer: pcbmodel.LayerMulti, Center: pcbmodel.Point{42, 45}, Width: 8, Height: 10, Shape: "oval"},
			{Number: "13", Net: "GND", Layer: pcbmodel.LayerMulti, Center: pcbmodel.Point{42, 55}, Width: 8, Height: 10, Shape: "oval"},
		}}},
	}
	if err := b.Validate(true); err != nil {
		t.Fatalf("repeated physical pad number was rejected: %v", err)
	}
	if _, _, ok := b.Pad("USB1.13"); ok {
		t.Fatal("ambiguous repeated pad unexpectedly resolved as a routing endpoint")
	}
}

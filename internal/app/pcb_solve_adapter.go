package app

import (
	"fmt"
	"math"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

// boardSnapshotToModel is the fail-closed host adapter for the public solver.
// It accepts only a fresh, self-identifying, complete two-layer snapshot. Four
// or more copper layers need an explicit ordered stackup contract; the legacy
// copperLayers count cannot tell signal layers from power/ground planes.
func boardSnapshotToModel(snap *boardSnapshot) (pcbmodel.Board, error) {
	if snap == nil {
		return pcbmodel.Board{}, fmt.Errorf("board snapshot is missing")
	}
	semantic, err := boardSnapshotSemanticSHA256(snap)
	if err != nil {
		return pcbmodel.Board{}, fmt.Errorf("compute board semantic hash: %w", err)
	}
	if strings.TrimSpace(snap.SemanticSHA256) == "" || snap.SemanticSHA256 != semantic {
		return pcbmodel.Board{}, fmt.Errorf("board snapshot semanticSha256 is missing or stale; take a fresh pcb dump --include-copper")
	}
	if snap.CopperLayers != 2 {
		if snap.CopperLayers > 2 {
			return pcbmodel.Board{}, fmt.Errorf("snapshot reports %d copper layers but does not declare ordered layer roles; four-layer joint solve is planned, not supported", snap.CopperLayers)
		}
		return pcbmodel.Board{}, fmt.Errorf("two-layer solve requires a measured copperLayers value of 2")
	}
	geometry, _, err := preparePCBRouteGeometry(snap)
	if err != nil {
		return pcbmodel.Board{}, err
	}

	board := pcbmodel.Board{
		SchemaVersion: 1,
		Units:         "mil",
		SemanticHash:  semantic,
		Outline: pcbmodel.Outline{
			Source: "polygon",
			BBox: pcbmodel.BBox{MinX: snap.Outline.BBox.MinX, MinY: snap.Outline.BBox.MinY,
				MaxX: snap.Outline.BBox.MaxX, MaxY: snap.Outline.BBox.MaxY},
		},
		Stackup: pcbmodel.Stackup{Copper: []pcbmodel.Layer{
			{ID: pcbmodel.LayerTop, Name: "TOP", Role: "signal"},
			{ID: pcbmodel.LayerBottom, Name: "BOTTOM", Role: "signal"},
		}},
		Rules: pcbmodel.Rules{
			Clearance:         snap.Rules.ClearanceMil,
			TrackTrack:        snap.Rules.ClearanceTrackTrackMil,
			CopperToEdge:      snap.Rules.CopperToEdgeMil,
			TrackWidthMinimum: snap.Rules.TrackWidthMinMil,
			ViaDiameter:       snap.Rules.ViaDiameterMil,
			ViaDrill:          snap.Rules.ViaDrillMil,
			Source:            snap.Rules.Source,
		},
	}
	for _, p := range snap.Outline.Points {
		board.Outline.Points = append(board.Outline.Points, pcbmodel.Point{p[0], p[1]})
	}

	padsByRef := map[string][]pcbmodel.Pad{}
	for _, pad := range geometry.allPads {
		shape, err := solvePadShape(pad)
		if err != nil {
			return pcbmodel.Board{}, err
		}
		padsByRef[pad.Designator] = append(padsByRef[pad.Designator], pcbmodel.Pad{
			ID: pad.ID, Number: pad.Number, Net: pad.Net, Layer: pad.Layer,
			Center: pcbmodel.Point{pad.X, pad.Y}, Width: pad.ShapeW, Height: pad.ShapeH,
			Rotation: pad.Rotation, Shape: shape, CornerRadius: pad.ShapeRound,
		})
	}
	seenRefs := map[string]bool{}
	for _, c := range snap.Components {
		if strings.TrimSpace(c.Designator) == "" || seenRefs[c.Designator] {
			return pcbmodel.Board{}, fmt.Errorf("component designator is empty or duplicated: %q", c.Designator)
		}
		seenRefs[c.Designator] = true
		if c.BBox == nil {
			return pcbmodel.Board{}, fmt.Errorf("component %s has no measured bbox", c.Designator)
		}
		board.Components = append(board.Components, pcbmodel.Component{
			ID: c.ID, Ref: c.Designator, Side: c.Layer, Anchor: pcbmodel.Point{c.X, c.Y},
			Rotation: c.Rotation, Locked: c.Locked,
			BBox: pcbmodel.BBox{MinX: c.BBox.MinX, MinY: c.BBox.MinY, MaxX: c.BBox.MaxX, MaxY: c.BBox.MaxY},
			Pads: padsByRef[c.Designator],
		})
	}
	for _, track := range geometry.tracks {
		board.Tracks = append(board.Tracks, pcbmodel.Track{ID: track.ID, Net: track.Net, Layer: track.Layer, Width: track.Width,
			Points: []pcbmodel.Point{{track.X1, track.Y1}, {track.X2, track.Y2}}})
	}
	for _, arc := range geometry.arcs {
		points, _, flattenErr := flattenNetPathArc(arc)
		if flattenErr != nil {
			return pcbmodel.Board{}, fmt.Errorf("existing arc %s geometry is unknown: %w", arc.ID, flattenErr)
		}
		for i := 0; i+1 < len(points); i++ {
			board.Tracks = append(board.Tracks, pcbmodel.Track{ID: fmt.Sprintf("arc:%s:%d", arc.ID, i), Net: arc.Net, Layer: arc.Layer, Width: arc.Width,
				Points: []pcbmodel.Point{{points[i].x, points[i].y}, {points[i+1].x, points[i+1].y}}})
		}
	}
	for _, via := range geometry.vias {
		board.Vias = append(board.Vias, pcbmodel.Via{ID: via.ID, Net: via.Net, At: pcbmodel.Point{via.X, via.Y}, Diameter: via.Dia, Drill: via.Hole, From: pcbmodel.LayerTop, To: pcbmodel.LayerBottom})
	}
	for _, area := range geometry.areas {
		board.Regions = append(board.Regions, pcbmodel.Region{ID: area.ID, Kind: "copper", Net: area.Net, Layer: area.Layer, Contours: solveContours(area.Contours)})
	}
	for _, ban := range geometry.bans {
		board.Regions = append(board.Regions, pcbmodel.Region{ID: ban.ID, Kind: "rule", Layer: ban.Layer, Rules: []string{"no-wires"}, Contours: solveContours(ban.Contours)})
	}
	if err := board.Validate(true); err != nil {
		return pcbmodel.Board{}, fmt.Errorf("board snapshot is incomplete for joint solve: %w", err)
	}
	return board, nil
}

func solvePadShape(p pcbPadP) (string, error) {
	switch p.Shape {
	case "RECT":
		if p.ShapeRound > netPathGeomEps {
			return "roundrect", nil
		}
		return "rect", nil
	case "OVAL":
		return "oval", nil
	case "ELLIPSE":
		if math.Abs(p.ShapeW-p.ShapeH) <= netPathGeomEps {
			return "circle", nil
		}
		return "", fmt.Errorf("pad %s.%s uses an elliptical shape not supported by the two-layer solver", p.Designator, p.Number)
	default:
		return "", fmt.Errorf("pad %s.%s uses unsupported shape %s", p.Designator, p.Number, p.Shape)
	}
}

func solveContours(in [][][2]float64) [][]pcbmodel.Point {
	out := make([][]pcbmodel.Point, len(in))
	for i, contour := range in {
		out[i] = make([]pcbmodel.Point, len(contour))
		for j, p := range contour {
			out[i][j] = pcbmodel.Point{p[0], p[1]}
		}
	}
	return out
}

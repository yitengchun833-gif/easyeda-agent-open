package app

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func renderPCBSolveSVG(w io.Writer, baseline pcbmodel.Board, candidate *pcbsolve.Candidate, note string, conflicts ...pcbsolve.Conflict) error {
	return renderPCBSolveSVGViewport(w, baseline, candidate, note, baseline.Outline.BBox, nil, conflicts...)
}

func renderPCBSolveRequestSVG(w io.Writer, baseline pcbmodel.Board, request pcbsolve.Request, candidate *pcbsolve.Candidate, note string, conflicts ...pcbsolve.Conflict) error {
	return renderPCBSolveSVGViewport(w, baseline, candidate, note, baseline.Outline.BBox, request.Layout.Keepouts, conflicts...)
}

func renderPCBSolveLocalSVG(w io.Writer, baseline pcbmodel.Board, request pcbsolve.Request, candidate *pcbsolve.Candidate, note string, conflicts ...pcbsolve.Conflict) error {
	shown := baseline
	if candidate != nil {
		shown = candidate.Board
	}
	return renderPCBSolveSVGViewport(w, baseline, candidate, note, solveFocusBBox(baseline, shown, request, candidate), request.Layout.Keepouts, conflicts...)
}

func renderPCBSolveSVGViewport(w io.Writer, baseline pcbmodel.Board, candidate *pcbsolve.Candidate, note string, box pcbmodel.BBox, keepouts []pcblayout.Keepout, conflicts ...pcbsolve.Conflict) error {
	if !box.Valid() || box.Width() <= 0 || box.Height() <= 0 {
		return fmt.Errorf("cannot render board without a valid outline")
	}
	shown := baseline
	if candidate != nil {
		shown = candidate.Board
		if note == "" && len(candidate.MoveReasons) > 0 {
			note = strings.Join(candidate.MoveReasons, "; ")
		}
	}
	pad := 30.0
	// Reserve enough header space for every reason, including recursive moves.
	// The drawing coordinates remain unchanged when the header grows.
	var noteLines []string
	limit := max(1, int(box.Width()/7))
	for runes := []rune(note); len(runes) > 0; {
		n := min(limit, len(runes))
		noteLines = append(noteLines, string(runes[:n]))
		runes = runes[n:]
	}
	header := max(pad, float64(len(noteLines))*11+10)
	width, height := box.Width()+2*pad, box.Height()+pad+header
	flipY := func(y float64) float64 { return box.MinY + box.MaxY - y }
	if _, err := fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="%.3f %.3f %.3f %.3f" width="%.0f" height="%.0f">`, box.MinX-pad, box.MinY-header, width, height, width*2, height*2); err != nil {
		return err
	}
	fmt.Fprint(w, `<style>text{font:10px ui-monospace,SFMono-Regular,monospace}.base{fill:none;stroke:#8a94a6;stroke-dasharray:4 3}.top{stroke:#d9463e}.bottom{stroke:#2563eb}.pad{fill:#e9a23b;stroke:#7c4a03}.route{fill:none;stroke-linecap:round;stroke-linejoin:round}.move{stroke:#7c3aed;stroke-width:1.2;marker-end:url(#arrow)}.label{fill:#172033}</style>`)
	fmt.Fprint(w, `<defs><marker id="arrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto"><path d="M0,0 L7,3.5 L0,7 z" fill="#7c3aed"/></marker></defs>`)
	fmt.Fprintf(w, `<rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="#fbfcfe"/>`, box.MinX-pad, box.MinY-header, width, height)
	outline := svgPoints(baseline.Outline.Points, flipY)
	fmt.Fprintf(w, `<polygon points="%s" fill="#fff" stroke="#172033" stroke-width="2"/>`, outline)
	if note != "" {
		fmt.Fprintf(w, `<desc>%s</desc>`, html.EscapeString(note))
	}
	for _, k := range keepouts {
		b := k.BBox
		fmt.Fprintf(w, `<g data-layout-keepout="%s"><title>Mechanical keepout %s; layers %v</title><rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="#f59e0b" fill-opacity=".12" stroke="#b45309" stroke-width=".8" stroke-dasharray="3 2"/></g>`, html.EscapeString(k.ID), html.EscapeString(k.ID), k.Layers, b.MinX, flipY(b.MaxY), b.Width(), b.Height())
	}

	for _, region := range shown.Regions {
		for _, contour := range regionContours(region) {
			fill := "#f3c4c4"
			opacity := .28
			if region.Kind == "copper" {
				fill, opacity = layerColor(region.Layer), .18
			}
			fmt.Fprintf(w, `<polygon points="%s" fill="%s" fill-opacity="%.2f" stroke="%s" stroke-opacity=".55" stroke-width=".8"/>`, svgPoints(contour, flipY), fill, opacity, fill)
		}
	}
	for _, track := range shown.Tracks {
		fmt.Fprintf(w, `<polyline class="route" points="%s" stroke="%s" stroke-width="%.3f" opacity=".55"/>`, svgPoints(track.Points, flipY), layerColor(track.Layer), track.Width)
	}
	for _, via := range shown.Vias {
		fmt.Fprintf(w, `<circle cx="%.3f" cy="%.3f" r="%.3f" fill="#e5e7eb" stroke="#374151" stroke-width="1"/><circle cx="%.3f" cy="%.3f" r="%.3f" fill="#fff" stroke="#374151" stroke-width=".6"/>`, via.At[0], flipY(via.At[1]), via.Diameter/2, via.At[0], flipY(via.At[1]), via.Drill/2)
	}

	if candidate != nil {
		for _, c := range baseline.Components {
			r := c.BBox
			fmt.Fprintf(w, `<rect class="base" x="%.3f" y="%.3f" width="%.3f" height="%.3f"/>`, r.MinX, flipY(r.MaxY), r.Width(), r.Height())
		}
	}
	for _, c := range shown.Components {
		r := c.BBox
		stroke := layerColor(c.Side)
		fmt.Fprintf(w, `<rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="%s" fill-opacity=".07" stroke="%s" stroke-width="1.2"/>`, r.MinX, flipY(r.MaxY), r.Width(), r.Height(), stroke, stroke)
		for _, p := range c.Pads {
			renderSolvePad(w, p, flipY)
		}
		fmt.Fprintf(w, `<text class="label" x="%.3f" y="%.3f">%s</text>`, r.MinX+2, flipY(r.MaxY)-2, html.EscapeString(c.Ref))
	}

	if candidate != nil {
		for _, move := range candidate.Moves {
			if len(move.Refs) == 0 {
				continue
			}
			base, okA := baseline.Component(move.Refs[0])
			projected, okB := candidate.Board.Component(move.Refs[0])
			if okA && okB && base.Anchor != projected.Anchor {
				fmt.Fprintf(w, `<line class="move" x1="%.3f" y1="%.3f" x2="%.3f" y2="%.3f"/>`, base.Anchor[0], flipY(base.Anchor[1]), projected.Anchor[0], flipY(projected.Anchor[1]))
			}
		}
		for _, joint := range candidate.Routes {
			for _, route := range joint.Routes {
				points := svgRoutePoints(route, flipY)
				clearance := max(baseline.Rules.Clearance, baseline.Rules.TrackTrack)
				fmt.Fprintf(w, `<g data-reservation="%s"><title>Planned %s; layer %d; copper width %.3f mil; clearance %.3f mil</title>`, html.EscapeString(joint.DemandID), html.EscapeString(route.Net), route.Layer, route.Width, clearance)
				fmt.Fprintf(w, `<polyline class="route" points="%s" stroke="%s" stroke-width="%.3f" opacity=".13"/>`, points, layerColor(route.Layer), route.Width+2*clearance)
				fmt.Fprintf(w, `<polyline class="route" points="%s" stroke="%s" stroke-width="%.3f"/>`, points, layerColor(route.Layer), route.Width)
				fmt.Fprint(w, `</g>`)
			}
			for _, via := range joint.Vias {
				fmt.Fprintf(w, `<circle data-reservation-via="%s" cx="%.3f" cy="%.3f" r="%.3f" fill="#7c3aed" opacity=".13"/>`, html.EscapeString(joint.DemandID), via.At[0], flipY(via.At[1]), via.Diameter/2+baseline.Rules.Clearance)
				fmt.Fprintf(w, `<circle cx="%.3f" cy="%.3f" r="%.3f" fill="#fff" stroke="#7c3aed" stroke-width="1.5"/><circle cx="%.3f" cy="%.3f" r="%.3f" fill="#fff" stroke="#7c3aed" stroke-width=".7"/>`, via.At[0], flipY(via.At[1]), via.Diameter/2, via.At[0], flipY(via.At[1]), via.Drill/2)
			}
		}
		if len(candidate.Routes) > 0 {
			fmt.Fprintf(w, `<text class="label" x="%.3f" y="%.3f">Trial paths + clearance (planning only)</text>`, box.MinX, box.MaxY+18)
		}
	}
	for _, conflict := range conflicts {
		a, z := conflict.Segment[0], conflict.Segment[1]
		fmt.Fprintf(w, `<g data-conflict="%s"><title>%s / %s: %s %s (net %s), layer %d</title><line x1="%.3f" y1="%.3f" x2="%.3f" y2="%.3f" stroke="#a21caf" stroke-width="1.4" stroke-dasharray="3 2"/></g>`, html.EscapeString(conflict.Object), html.EscapeString(conflict.DemandID), html.EscapeString(conflict.Net), html.EscapeString(conflict.Kind), html.EscapeString(conflict.Object), html.EscapeString(conflict.ConflictNet), conflict.Layer, a[0], flipY(a[1]), z[0], flipY(z[1]))
	}
	if len(noteLines) > 0 {
		fmt.Fprintf(w, `<rect x="%.3f" y="%.3f" width="%.3f" height="%.3f" fill="#fbfcfe"/>`, box.MinX-pad, box.MinY-header, width, header)
		for row, line := range noteLines {
			fmt.Fprintf(w, `<text class="label" data-review-note="true" font-family="monospace" font-size="10" x="%.3f" y="%.3f">%s</text>`, box.MinX, box.MinY-header+14+float64(row)*11, html.EscapeString(line))
		}
	}
	fmt.Fprint(w, `</svg>`)
	return nil
}

// solveFocusBBox derives a review crop from the declared problem and the exact
// projected candidate. It never invents a second geometry representation: the
// local SVG uses the same components, routes and vias as the full-board SVG.
func solveFocusBBox(baseline, shown pcbmodel.Board, request pcbsolve.Request, candidate *pcbsolve.Candidate) pcbmodel.BBox {
	refs := map[string]bool{}
	for _, group := range request.Layout.Groups {
		for _, ref := range group.Refs {
			refs[ref] = true
		}
	}
	for _, demand := range request.Routing.Demands {
		for _, endpoint := range []string{demand.From, demand.To} {
			if part := strings.SplitN(endpoint, ".", 2); len(part) == 2 {
				refs[part[0]] = true
			}
		}
	}
	box, have := pcbmodel.BBox{}, false
	includePoint := func(p pcbmodel.Point) {
		if !have {
			box = pcbmodel.BBox{MinX: p[0], MinY: p[1], MaxX: p[0], MaxY: p[1]}
			have = true
			return
		}
		if p[0] < box.MinX {
			box.MinX = p[0]
		}
		if p[0] > box.MaxX {
			box.MaxX = p[0]
		}
		if p[1] < box.MinY {
			box.MinY = p[1]
		}
		if p[1] > box.MaxY {
			box.MaxY = p[1]
		}
	}
	includeBBox := func(b pcbmodel.BBox) {
		includePoint(pcbmodel.Point{b.MinX, b.MinY})
		includePoint(pcbmodel.Point{b.MaxX, b.MaxY})
	}
	for ref := range refs {
		if c, ok := baseline.Component(ref); ok {
			includeBBox(c.BBox)
		}
		if c, ok := shown.Component(ref); ok {
			includeBBox(c.BBox)
		}
	}
	if candidate != nil {
		for _, joint := range candidate.Routes {
			for _, route := range joint.Routes {
				for _, p := range route.Points {
					includePoint(p)
				}
			}
			for _, via := range joint.Vias {
				includeBBox(pcbmodel.BBox{MinX: via.At[0] - via.Diameter/2, MinY: via.At[1] - via.Diameter/2, MaxX: via.At[0] + via.Diameter/2, MaxY: via.At[1] + via.Diameter/2})
			}
		}
	}
	if !have || box.Width() <= 0 || box.Height() <= 0 {
		return baseline.Outline.BBox
	}
	margin := 40.0
	if rulesMargin := 4 * baseline.Rules.Clearance; rulesMargin > margin {
		margin = rulesMargin
	}
	return pcbmodel.BBox{MinX: box.MinX - margin, MinY: box.MinY - margin, MaxX: box.MaxX + margin, MaxY: box.MaxY + margin}
}

func renderSolvePad(w io.Writer, p pcbmodel.Pad, flipY func(float64) float64) {
	cx, cy := p.Center[0], flipY(p.Center[1])
	rotation := -p.Rotation
	switch p.Shape {
	case "circle":
		fmt.Fprintf(w, `<circle class="pad" cx="%.3f" cy="%.3f" r="%.3f" fill-opacity=".75"/>`, cx, cy, p.Width/2)
	case "oval":
		radius := p.Width / 2
		if p.Height < p.Width {
			radius = p.Height / 2
		}
		fmt.Fprintf(w, `<rect class="pad" x="%.3f" y="%.3f" width="%.3f" height="%.3f" rx="%.3f" fill-opacity=".75" transform="rotate(%.3f %.3f %.3f)"/>`, cx-p.Width/2, cy-p.Height/2, p.Width, p.Height, radius, rotation, cx, cy)
	default:
		radius := 0.0
		if p.Shape == "roundrect" {
			radius = p.CornerRadius
		}
		fmt.Fprintf(w, `<rect class="pad" x="%.3f" y="%.3f" width="%.3f" height="%.3f" rx="%.3f" fill-opacity=".75" transform="rotate(%.3f %.3f %.3f)"/>`, cx-p.Width/2, cy-p.Height/2, p.Width, p.Height, radius, rotation, cx, cy)
	}
}

func regionContours(region pcbmodel.Region) [][]pcbmodel.Point {
	if len(region.Contours) > 0 {
		return region.Contours
	}
	if len(region.Points) > 0 {
		return [][]pcbmodel.Point{region.Points}
	}
	return nil
}

func svgPoints(points []pcbmodel.Point, flipY func(float64) float64) string {
	parts := make([]string, 0, len(points))
	for _, p := range points {
		parts = append(parts, fmt.Sprintf("%.3f,%.3f", p[0], flipY(p[1])))
	}
	return strings.Join(parts, " ")
}

func svgRoutePoints(route pcbrouting.LayerRoute, flipY func(float64) float64) string {
	return svgPoints(route.Points, flipY)
}

func layerColor(layer int) string {
	if layer == pcbmodel.LayerBottom {
		return "#2563eb"
	}
	if layer == pcbmodel.LayerMulti {
		return "#7c3aed"
	}
	return "#d9463e"
}

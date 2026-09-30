package app

import (
	"fmt"
	"html"
	"io"
	"math"
	"sort"
	"strings"
)

// renderPCBRouteSVG renders the exact snapshot consumed by solve/check. It is a
// review aid: the JSON report and geometric checker remain the machine evidence.
func renderPCBRouteSVG(w io.Writer, snap *boardSnapshot, in pcbRouteRequest, report pcbRouteReport) error {
	const width, height, header, footer, side = 1100.0, 720.0, 92.0, 54.0, 46.0
	view := pcbRouteRenderBounds(snap, report.Candidate)
	if view.MaxX <= view.MinX || view.MaxY <= view.MinY || !allFinite(view.MinX, view.MinY, view.MaxX, view.MaxY) {
		view = layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	}
	margin := math.Max(10, math.Max(view.MaxX-view.MinX, view.MaxY-view.MinY)*0.035)
	view = expandLayoutBBox(view, margin)
	scale := math.Min((width-2*side)/(view.MaxX-view.MinX), (height-header-footer)/(view.MaxY-view.MinY))
	xp := func(x float64) float64 { return side + (x-view.MinX)*scale }
	yp := func(y float64) float64 { return height - footer - (y-view.MinY)*scale }
	statusColor := map[string]string{"pass": "#22c55e", "fail": "#ef4444", "incomplete": "#f59e0b"}[report.Status]
	if statusColor == "" {
		statusColor = "#94a3b8"
	}

	fmt.Fprintf(w, `<svg xmlns="http://www.w3.org/2000/svg" width="%.0f" height="%.0f" viewBox="0 0 %.0f %.0f">`+"\n", width, height, width, height)
	fmt.Fprintln(w, `<defs><pattern id="route-grid" width="18" height="18" patternUnits="userSpaceOnUse"><path d="M 18 0 L 0 0 0 18" fill="none" stroke="#1e293b" stroke-width=".7"/></pattern><pattern id="route-ban" width="9" height="9" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line x1="0" y1="0" x2="0" y2="9" stroke="#ef4444" stroke-width="2"/></pattern></defs>`)
	fmt.Fprintln(w, `<rect width="100%" height="100%" fill="#07111f"/><rect x="0" y="92" width="100%" height="574" fill="url(#route-grid)"/>`)
	if snap != nil && snap.Outline != nil {
		renderPCBRouteOutline(w, snap.Outline, xp, yp, scale)
	}

	// Component extents are context only; pads and copper below carry rule meaning.
	if snap != nil {
		comps := append([]boardComp(nil), snap.Components...)
		sort.Slice(comps, func(i, j int) bool { return comps[i].Designator < comps[j].Designator })
		for _, c := range comps {
			if c.BBox == nil {
				continue
			}
			fmt.Fprintf(w, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="2" fill="#334155" fill-opacity=".18" stroke="#64748b" stroke-opacity=".28" stroke-width=".7"><title>%s</title></rect>`+"\n", xp(c.BBox.MinX), yp(c.BBox.MaxY), (c.BBox.MaxX-c.BBox.MinX)*scale, (c.BBox.MaxY-c.BBox.MinY)*scale, html.EscapeString(c.Designator))
		}
		renderPCBRouteAreas(w, snap, in, xp, yp)
		renderPCBRouteExistingCopper(w, snap, in, xp, yp, scale)
		renderPCBRoutePads(w, snap, in, xp, yp, scale)
	}

	fromPad, fromOK := pcbRouteRenderPad(snap, in.From)
	toPad, toOK := pcbRouteRenderPad(snap, in.To)
	if fromOK && toOK {
		minX, maxX := math.Min(fromPad.X, toPad.X), math.Max(fromPad.X, toPad.X)
		minY, maxY := math.Min(fromPad.Y, toPad.Y), math.Max(fromPad.Y, toPad.Y)
		d := math.Max(0, in.MaxDetourMil)
		fmt.Fprintf(w, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" fill="%s" fill-opacity=".045" stroke="%s" stroke-opacity=".65" stroke-width="1.5" stroke-dasharray="7 5"><title>bounded search envelope: %.3f mil detour</title></rect>`+"\n", xp(minX-d), yp(maxY+d), (maxX-minX+2*d)*scale, (maxY-minY+2*d)*scale, d*scale, statusColor, statusColor, d)
		fmt.Fprintf(w, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="#94a3b8" stroke-width="1" stroke-dasharray="3 5" opacity=".7"/>`+"\n", xp(fromPad.X), yp(fromPad.Y), xp(toPad.X), yp(toPad.Y))
	}

	if report.Candidate != nil && len(report.Candidate.Points) > 0 {
		candidateColor := "#38bdf8"
		if report.Status != "pass" {
			candidateColor = "#fb7185"
		}
		clearance := 0.0
		if snap != nil && snap.Rules != nil {
			clearance = snap.Rules.ClearanceMil
		}
		renderPCBRoutePolyline(w, report.Candidate.Points, xp, yp, candidateColor, math.Max(2, (report.Candidate.WidthMil+2*clearance)*scale), .16, "candidate clearance envelope")
		renderPCBRoutePolyline(w, report.Candidate.Points, xp, yp, candidateColor, math.Max(2.5, report.Candidate.WidthMil*scale), 1, "candidate route")
		for i, p := range report.Candidate.Points {
			fmt.Fprintf(w, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="#f8fafc" stroke="%s" stroke-width="1.5"><title>candidate point %d: %.3f, %.3f mil</title></circle>`+"\n", xp(p[0]), yp(p[1]), math.Max(2.2, report.Candidate.WidthMil*scale/2), candidateColor, i, p[0], p[1])
		}
	}
	if fromOK {
		renderPCBRouteEndpoint(w, fromPad, in.From, "FROM", xp, yp, scale, "#facc15")
	}
	if toOK {
		renderPCBRouteEndpoint(w, toPad, in.To, "TO", xp, yp, scale, "#a3e635")
	}

	reason := strings.Join(report.Reasons, "; ")
	if len(reason) > 150 {
		reason = reason[:147] + "..."
	}
	metric := fmt.Sprintf("layer %d · width %.3f mil", in.Layer, in.WidthMil)
	if report.Candidate != nil {
		metric += fmt.Sprintf(" · length %.3f mil · bends %d", report.LengthMil, report.Bends)
	}
	if report.Search != nil {
		metric += fmt.Sprintf(" · states %d/%d", report.Search.States, in.MaxStates)
	}
	fmt.Fprintf(w, `<rect x="24" y="18" width="1052" height="58" rx="12" fill="#0f172a" stroke="#334155"/><circle cx="48" cy="42" r="7" fill="%s"/><text x="64" y="47" font-family="Arial,PingFang SC,sans-serif" font-size="20" font-weight="700" fill="#f8fafc">%s · %s → %s</text><text x="64" y="68" font-family="Arial,PingFang SC,sans-serif" font-size="12" fill="#94a3b8">%s</text>`+"\n", statusColor, html.EscapeString(strings.ToUpper(report.Status)), html.EscapeString(in.From), html.EscapeString(in.To), html.EscapeString(metric))
	if reason != "" {
		fmt.Fprintf(w, `<text x="1072" y="68" font-family="Arial,PingFang SC,sans-serif" font-size="11" text-anchor="end" fill="%s">%s</text>`+"\n", statusColor, html.EscapeString(reason))
	}
	fmt.Fprintln(w, `<g font-family="Arial,PingFang SC,sans-serif" font-size="11" fill="#cbd5e1"><line x1="46" y1="690" x2="78" y2="690" stroke="#38bdf8" stroke-width="4"/><text x="86" y="694">candidate</text><line x1="177" y1="690" x2="209" y2="690" stroke="#14b8a6" stroke-width="3"/><text x="217" y="694">same-net copper</text><line x1="356" y1="690" x2="388" y2="690" stroke="#64748b" stroke-width="3"/><text x="396" y="694">other copper</text><rect x="500" y="683" width="30" height="14" fill="url(#route-ban)" stroke="#ef4444"/><text x="538" y="694">no-wires</text><text x="1060" y="694" text-anchor="end">planning preview · y-up · units mil</text></g>`)
	_, err := fmt.Fprintln(w, `</svg>`)
	return err
}

func pcbRouteRenderBounds(snap *boardSnapshot, candidate *pcbRouteCandidate) layoutBBox {
	var b layoutBBox
	set := false
	add := func(x, y float64) {
		if !allFinite(x, y) {
			return
		}
		if !set {
			b = layoutBBox{MinX: x, MaxX: x, MinY: y, MaxY: y}
			set = true
			return
		}
		b.MinX, b.MaxX = math.Min(b.MinX, x), math.Max(b.MaxX, x)
		b.MinY, b.MaxY = math.Min(b.MinY, y), math.Max(b.MaxY, y)
	}
	if snap != nil && snap.Outline != nil && snap.Outline.BBox.MaxX > snap.Outline.BBox.MinX && snap.Outline.BBox.MaxY > snap.Outline.BBox.MinY {
		return snap.Outline.BBox
	}
	if snap != nil {
		for _, c := range snap.Components {
			if c.BBox != nil {
				add(c.BBox.MinX, c.BBox.MinY)
				add(c.BBox.MaxX, c.BBox.MaxY)
			}
			for _, p := range c.Pads {
				add(p.X-p.W/2, p.Y-p.H/2)
				add(p.X+p.W/2, p.Y+p.H/2)
			}
		}
	}
	if candidate != nil {
		for _, p := range candidate.Points {
			add(p[0], p[1])
		}
	}
	if !set || b.MaxX <= b.MinX || b.MaxY <= b.MinY {
		return layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}
	}
	return b
}

func renderPCBRouteOutline(w io.Writer, outline *boardOutline, xp, yp func(float64) float64, scale float64) {
	if len(outline.Points) >= 3 {
		fmt.Fprint(w, `<polygon points="`)
		for _, p := range outline.Points {
			fmt.Fprintf(w, `%.2f,%.2f `, xp(p[0]), yp(p[1]))
		}
		fmt.Fprintln(w, `" fill="#0b1c2e" stroke="#e2e8f0" stroke-width="2.2"/>`)
		return
	}
	b := outline.BBox
	fmt.Fprintf(w, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" fill="#0b1c2e" stroke="#e2e8f0" stroke-width="2.2"/>`+"\n", xp(b.MinX), yp(b.MaxY), (b.MaxX-b.MinX)*scale, (b.MaxY-b.MinY)*scale)
}

func renderPCBRouteExistingCopper(w io.Writer, snap *boardSnapshot, in pcbRouteRequest, xp, yp func(float64) float64, scale float64) {
	tracks, arcs, vias, err := crystalSnapshotRouting(snap)
	if err != nil {
		return
	}
	for _, t := range tracks {
		if t.Layer != in.Layer {
			continue
		}
		color := "#64748b"
		opacity := .8
		if t.Net == in.Net {
			color, opacity = "#14b8a6", 1
		}
		fmt.Fprintf(w, `<line x1="%.2f" y1="%.2f" x2="%.2f" y2="%.2f" stroke="%s" stroke-width="%.2f" stroke-linecap="round" opacity="%.2f"><title>%s · layer %d · %.3f mil</title></line>`+"\n", xp(t.X1), yp(t.Y1), xp(t.X2), yp(t.Y2), color, math.Max(.8, t.Width*scale), opacity, html.EscapeString(t.Net), t.Layer, t.Width)
	}
	for _, a := range arcs {
		if a.Layer != in.Layer {
			continue
		}
		curve, _, err := flattenNetPathArc(a)
		if err != nil {
			continue
		}
		points := make([][2]float64, 0, len(curve))
		for _, p := range curve {
			points = append(points, [2]float64{p.x, p.y})
		}
		color := "#64748b"
		if a.Net == in.Net {
			color = "#14b8a6"
		}
		renderPCBRoutePolyline(w, points, xp, yp, color, math.Max(.8, a.Width*scale), .8, "existing arc "+a.Net)
	}
	for _, v := range vias {
		color := "#64748b"
		if v.Net == in.Net {
			color = "#14b8a6"
		}
		fmt.Fprintf(w, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="%s" fill-opacity=".55" stroke="%s" stroke-width="1"><title>via %s · %.3f/%.3f mil</title></circle><circle cx="%.2f" cy="%.2f" r="%.2f" fill="#07111f"/>`+"\n", xp(v.X), yp(v.Y), math.Max(1.5, v.Dia*scale/2), color, color, html.EscapeString(v.Net), v.Dia, v.Hole, xp(v.X), yp(v.Y), math.Max(.7, v.Hole*scale/2))
	}
}

func renderPCBRoutePads(w io.Writer, snap *boardSnapshot, in pcbRouteRequest, xp, yp func(float64) float64, scale float64) {
	for _, c := range snap.Components {
		for _, p := range c.Pads {
			if !padLayerMatches(p.Layer, in.Layer) || p.W <= 0 || p.H <= 0 {
				continue
			}
			ref := c.Designator + "." + p.Number
			color := "#475569"
			opacity := .75
			if p.Net == in.Net {
				color, opacity = "#14b8a6", 1
			}
			if ref == in.From {
				color, opacity = "#facc15", 1
			} else if ref == in.To {
				color, opacity = "#a3e635", 1
			}
			shape := pcbRoutePadShape(p.Shape)
			rx := 1.0
			if shape == "OVAL" || shape == "ELLIPSE" {
				rx = math.Min(p.W, p.H) * scale / 2
			}
			fmt.Fprintf(w, `<rect x="%.2f" y="%.2f" width="%.2f" height="%.2f" rx="%.2f" transform="rotate(%.3f %.2f %.2f)" fill="%s" fill-opacity="%.2f" stroke="#e2e8f0" stroke-opacity=".45" stroke-width=".6"><title>%s · %s · layer %d</title></rect>`+"\n", xp(p.X)-p.W*scale/2, yp(p.Y)-p.H*scale/2, math.Max(1, p.W*scale), math.Max(1, p.H*scale), rx, -p.Rotation, xp(p.X), yp(p.Y), color, opacity, html.EscapeString(ref), html.EscapeString(p.Net), p.Layer)
		}
	}
}

func pcbRoutePadShape(raw any) string {
	if items, ok := raw.([]any); ok && len(items) > 0 {
		return strings.ToUpper(asString(items[0]))
	}
	return strings.ToUpper(asString(raw))
}

func renderPCBRouteAreas(w io.Writer, snap *boardSnapshot, in pcbRouteRequest, xp, yp func(float64) float64) {
	if snap.Copper == nil {
		return
	}
	for _, raw := range snap.Copper.Fills {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		layer := int(asFloat(m["layer"]))
		if layer != in.Layer && layer != pcbLayerMulti {
			continue
		}
		contours, err := polygonSourceContours(m["source"])
		if err != nil {
			continue
		}
		fill, stroke := "#fb923c", "#fdba74"
		opacity := .16
		if layer == pcbLayerMulti {
			fill, stroke, opacity = "#020617", "#f8fafc", .88
		}
		renderPCBRouteContours(w, contours, xp, yp, fill, stroke, opacity, "static fill / cutout")
	}
	for _, raw := range snap.Copper.Poured {
		m, ok := raw.(map[string]any)
		if !ok || int(asFloat(m["layer"])) != in.Layer {
			continue
		}
		fills, _ := m["fills"].([]any)
		for _, item := range fills {
			fill, ok := item.(map[string]any)
			if !ok {
				continue
			}
			contours, err := polygonSourceContours(fill["source"])
			if err != nil {
				continue
			}
			color := "#a855f7"
			if asString(m["net"]) == in.Net {
				color = "#14b8a6"
			}
			renderPCBRouteContours(w, contours, xp, yp, color, color, .12, "poured copper "+asString(m["net"]))
		}
	}
	for _, raw := range snap.Copper.Regions {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		layer := int(asFloat(m["layer"]))
		if layer != in.Layer && layer != pcbLayerMulti {
			continue
		}
		noWires := false
		for _, rule := range toFloatSlice(m["ruleType"]) {
			noWires = noWires || rule == 5
		}
		if !noWires {
			continue
		}
		contours, err := polygonSourceContours(m["source"])
		if err != nil {
			continue
		}
		renderPCBRouteContours(w, contours, xp, yp, "url(#route-ban)", "#ef4444", .5, "no-wires region")
	}
}

func renderPCBRouteContours(w io.Writer, contours [][][2]float64, xp, yp func(float64) float64, fill, stroke string, opacity float64, title string) {
	if len(contours) == 0 {
		return
	}
	fmt.Fprint(w, `<path d="`)
	for _, contour := range contours {
		if len(contour) == 0 {
			continue
		}
		fmt.Fprintf(w, `M %.2f %.2f `, xp(contour[0][0]), yp(contour[0][1]))
		for _, p := range contour[1:] {
			fmt.Fprintf(w, `L %.2f %.2f `, xp(p[0]), yp(p[1]))
		}
		fmt.Fprint(w, `Z `)
	}
	fmt.Fprintf(w, `" fill="%s" fill-rule="evenodd" fill-opacity="%.2f" stroke="%s" stroke-width="1"><title>%s</title></path>`+"\n", fill, opacity, stroke, html.EscapeString(title))
}

func renderPCBRoutePolyline(w io.Writer, points [][2]float64, xp, yp func(float64) float64, color string, width, opacity float64, title string) {
	if len(points) == 0 {
		return
	}
	fmt.Fprint(w, `<polyline points="`)
	for _, p := range points {
		fmt.Fprintf(w, `%.2f,%.2f `, xp(p[0]), yp(p[1]))
	}
	fmt.Fprintf(w, `" fill="none" stroke="%s" stroke-width="%.2f" stroke-linejoin="round" stroke-linecap="round" opacity="%.2f"><title>%s</title></polyline>`+"\n", color, width, opacity, html.EscapeString(title))
}

func pcbRouteRenderPad(snap *boardSnapshot, ref string) (boardPad, bool) {
	if snap == nil {
		return boardPad{}, false
	}
	var found boardPad
	count := 0
	for _, c := range snap.Components {
		for _, p := range c.Pads {
			if c.Designator+"."+p.Number == ref {
				found, count = p, count+1
			}
		}
	}
	return found, count == 1
}

func renderPCBRouteEndpoint(w io.Writer, p boardPad, ref, role string, xp, yp func(float64) float64, scale float64, color string) {
	r := math.Max(5, math.Max(p.W, p.H)*scale/2+4)
	fmt.Fprintf(w, `<circle cx="%.2f" cy="%.2f" r="%.2f" fill="none" stroke="%s" stroke-width="2.5"/><text x="%.2f" y="%.2f" font-family="Arial,PingFang SC,sans-serif" font-size="11" font-weight="700" text-anchor="middle" fill="%s">%s %s</text>`+"\n", xp(p.X), yp(p.Y), r, color, xp(p.X), yp(p.Y)-r-6, color, role, html.EscapeString(ref))
}

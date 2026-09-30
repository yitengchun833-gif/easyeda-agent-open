package pcbsolve

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

func routeCandidates(ctx context.Context, board pcbmodel.Board, req RoutingRequest, d Demand, selected []pcbrouting.JointCandidate, remaining int, observe func(Conflict)) ([]pcbrouting.JointCandidate, int, string, error) {
	from, _, _ := board.Pad(d.From)
	to, _, _ := board.Pad(d.To)
	var out []pcbrouting.JointCandidate
	used := 0
	limited := ""
	for _, layer := range d.Layers {
		if !padOnLayer(from, layer) || !padOnLayer(to, layer) {
			continue
		}
		clear := segmentClearObserved(board, d, layer, selected, observe)
		budget := minInt(remaining-used, pcbrouting.MaxSearchStates)
		if budget < 1 {
			return out, used, "state-budget", nil
		}
		result, err := pcbrouting.Solve(ctx, pcbrouting.Request{From: from.Center, To: to.Center, Step: req.Step, MaxDetour: req.MaxDetour, MaxStates: budget}, clear)
		used += result.States
		if result.Reason == "state-budget" || result.Reason == "grid-limit" {
			limited = result.Reason
		}
		if err != nil {
			return nil, used, result.Reason, err
		}
		if result.Status == pcbrouting.Found {
			out = append(out, pcbrouting.JointCandidate{DemandID: d.ID, Routes: []pcbrouting.LayerRoute{{ID: d.ID, Net: d.Net, Layer: layer, Width: d.Width, Points: result.Points}}})
		}
	}
	if d.MaxVias >= 2 && len(d.Layers) == 2 && len(board.Stackup.Copper) == 2 {
		first, second := d.Layers[0], d.Layers[1]
		if padOnLayer(from, first) && padOnLayer(to, first) {
			viaCandidates, viaUsed, viaLimit := twoLayerCandidates(ctx, board, req, d, selected, from.Center, to.Center, first, second, remaining-used, observe)
			used += viaUsed
			out = append(out, viaCandidates...)
			if viaLimit != "" {
				limited = viaLimit
			}
			if ctx.Err() != nil {
				return nil, used, "canceled", ctx.Err()
			}
		}
	}
	// Stable preference: zero vias, then shorter route and fewer bends.
	sort.SliceStable(out, func(i, j int) bool {
		if len(out[i].Vias) != len(out[j].Vias) {
			return len(out[i].Vias) < len(out[j].Vias)
		}
		li, bi := jointMetrics(out[i])
		lj, bj := jointMetrics(out[j])
		if li != lj {
			return li < lj
		}
		return bi < bj
	})
	if limited != "" {
		return out, used, limited, nil
	}
	if len(out) == 0 {
		return nil, used, "no route for demand " + d.ID + " within declared layers/via budget", nil
	}
	return out, used, "", nil
}

func twoLayerCandidates(ctx context.Context, board pcbmodel.Board, req RoutingRequest, d Demand, selected []pcbrouting.JointCandidate, from, to pcbmodel.Point, first, second, remaining int, observe func(Conflict)) ([]pcbrouting.JointCandidate, int, string) {
	if board.Rules.ViaDiameter <= board.Rules.ViaDrill || board.Rules.ViaDrill <= 0 {
		return nil, 0, ""
	}
	sites := func(at, toward pcbmodel.Point) []pcbmodel.Point {
		var out []pcbmodel.Point
		dirs := []pcbmodel.Point{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
		for ring := 1; ring <= 2 && len(out) < 8; ring++ {
			for _, dir := range dirs {
				p := pcbmodel.Point{at[0] + dir[0]*req.Step*float64(ring), at[1] + dir[1]*req.Step*float64(ring)}
				if viaClear(board, d, p, selected, nil) {
					out = append(out, p)
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return pointDistance(out[i], toward) < pointDistance(out[j], toward) })
		return out
	}
	left, right := sites(from, to), sites(to, from)
	var out []pcbrouting.JointCandidate
	used := 0
	limited := ""
	for _, a := range left {
		for _, z := range right {
			if used >= remaining {
				return out, used, "state-budget"
			}
			if len(out) >= 8 || !viaClear(board, d, z, selected, []pcbmodel.Point{a}) {
				continue
			}
			vias := []pcbrouting.RouteVia{{ID: d.ID + "/via-1", Net: d.Net, At: a, Diameter: board.Rules.ViaDiameter, Drill: board.Rules.ViaDrill, From: first, To: second}, {ID: d.ID + "/via-2", Net: d.Net, At: z, Diameter: board.Rules.ViaDiameter, Drill: board.Rules.ViaDrill, From: second, To: first}}
			provisional := appendSelected(selected, pcbrouting.JointCandidate{DemandID: d.ID, Vias: vias})
			segments := []struct {
				id       string
				layer    int
				from, to pcbmodel.Point
			}{{d.ID + "/entry", first, from, a}, {d.ID + "/middle", second, a, z}, {d.ID + "/exit", first, z, to}}
			candidate := pcbrouting.JointCandidate{DemandID: d.ID, Vias: vias}
			ok := true
			for _, segment := range segments {
				budget := minInt(remaining-used, pcbrouting.MaxSearchStates)
				if budget < 1 {
					return out, used, "state-budget"
				}
				result, err := pcbrouting.Solve(ctx, pcbrouting.Request{From: segment.from, To: segment.to, Step: req.Step, MaxDetour: req.MaxDetour, MaxStates: budget}, segmentClearObserved(board, d, segment.layer, provisional, observe))
				used += result.States
				if result.Reason == "state-budget" || result.Reason == "grid-limit" {
					limited = result.Reason
				}
				if err != nil || result.Status != pcbrouting.Found {
					ok = false
					break
				}
				candidate.Routes = append(candidate.Routes, pcbrouting.LayerRoute{ID: segment.id, Net: d.Net, Layer: segment.layer, Width: d.Width, Points: result.Points})
				provisional[len(provisional)-1].Routes = append(provisional[len(provisional)-1].Routes, candidate.Routes[len(candidate.Routes)-1])
			}
			if ok {
				out = append(out, candidate)
			}
		}
	}
	return out, used, limited
}

func routeChecker(board pcbmodel.Board, req RoutingRequest, demands map[string]Demand) pcbrouting.CandidateChecker {
	return func(ctx context.Context, generic pcbrouting.Demand, candidate pcbrouting.JointCandidate, selected []pcbrouting.JointCandidate) error {
		d := demands[generic.ID]
		if candidate.DemandID != d.ID || len(candidate.Routes) == 0 || len(candidate.Vias) > d.MaxVias {
			return fmt.Errorf("candidate identity/route/via count mismatch")
		}
		for _, route := range candidate.Routes {
			if len(route.Points) < 2 {
				return fmt.Errorf("candidate contains an empty route")
			}
		}
		from, _, okFrom := board.Pad(d.From)
		to, _, okTo := board.Pad(d.To)
		if !okFrom || !okTo || candidate.Routes[0].Points[0] != from.Center || candidate.Routes[len(candidate.Routes)-1].Points[len(candidate.Routes[len(candidate.Routes)-1].Points)-1] != to.Center {
			return fmt.Errorf("candidate endpoints differ from measured pads")
		}
		if !padOnLayer(from, candidate.Routes[0].Layer) || !padOnLayer(to, candidate.Routes[len(candidate.Routes)-1].Layer) {
			return fmt.Errorf("candidate endpoint layer does not meet its pad")
		}
		allowed := map[int]bool{}
		for _, layer := range d.Layers {
			allowed[layer] = true
		}
		vias := make([]pcbmodel.Point, 0, len(candidate.Vias))
		for _, via := range candidate.Vias {
			if via.Net != d.Net || !allowed[via.From] || !allowed[via.To] || via.Diameter != board.Rules.ViaDiameter || via.Drill != board.Rules.ViaDrill || !viaClear(board, d, via.At, selected, vias) {
				return fmt.Errorf("candidate via violates net, span, size or clearance")
			}
			vias = append(vias, via.At)
		}
		withSelf := appendSelected(selected, candidate)
		for i, route := range candidate.Routes {
			if route.Net != d.Net || route.Width != d.Width || !allowed[route.Layer] || len(route.Points) < 2 {
				return fmt.Errorf("candidate route metadata mismatch")
			}
			clearSelected := withoutDemand(withSelf, d.ID)
			if err := pcbrouting.Check(ctx, pcbrouting.Request{From: route.Points[0], To: route.Points[len(route.Points)-1], Step: req.Step, MaxDetour: req.MaxDetour, MaxStates: req.MaxStates}, route.Points, segmentClear(board, d, route.Layer, clearSelected, candidate.Vias)); err != nil {
				return fmt.Errorf("route %d: %w", i, err)
			}
			if i > 0 {
				prior := candidate.Routes[i-1]
				join := route.Points[0]
				if prior.Points[len(prior.Points)-1] != join || !hasVia(candidate.Vias, join, prior.Layer, route.Layer) {
					return fmt.Errorf("layer transition lacks an exact via witness")
				}
			}
		}
		return nil
	}
}

func segmentClear(board pcbmodel.Board, d Demand, layer int, selected []pcbrouting.JointCandidate, ownVias any) pcbrouting.SegmentClear {
	return segmentClearObserved(board, d, layer, selected, nil)
}

func segmentClearObserved(board pcbmodel.Board, d Demand, layer int, selected []pcbrouting.JointCandidate, observe func(Conflict)) pcbrouting.SegmentClear {
	return func(a, z pcbrouting.Point) bool {
		reject := func(kind, object, ref, net, demandID string) bool {
			if observe != nil {
				observe(Conflict{DemandID: d.ID, Net: d.Net, Layer: layer, Kind: kind, Object: object, Ref: ref, ConflictNet: net, ConflictDemandID: demandID, Segment: [2]pcbmodel.Point{a, z}})
			}
			return false
		}
		margin := d.Width/2 + board.Rules.Clearance
		if !segmentInsideOutline(board.Outline, a, z, d.Width/2+board.Rules.CopperToEdge) {
			return reject("outline", "board-outline", "", "", "")
		}
		for _, c := range board.Components {
			for _, pad := range c.Pads {
				if pad.Net == d.Net || !padOnLayer(pad, layer) {
					continue
				}
				if padSegmentDistance(pad, a, z) < margin-1e-7 {
					return reject("pad", c.Ref+"."+pad.Number, c.Ref, pad.Net, "")
				}
			}
		}
		for _, track := range board.Tracks {
			if track.Net == d.Net || track.Layer != layer {
				continue
			}
			for i := 1; i < len(track.Points); i++ {
				if segmentDistance(a, z, track.Points[i-1], track.Points[i]) < d.Width/2+track.Width/2+math.Max(board.Rules.Clearance, board.Rules.TrackTrack)-1e-7 {
					return reject("track", track.ID, "", track.Net, "")
				}
			}
		}
		for _, via := range board.Vias {
			if via.Net != d.Net && pointSegmentDistance(via.At, a, z) < d.Width/2+via.Diameter/2+board.Rules.Clearance-1e-7 {
				return reject("via", via.ID, "", via.Net, "")
			}
		}
		for _, region := range board.Regions {
			if region.Layer != layer && region.Layer != pcbmodel.LayerMulti {
				continue
			}
			blocked := region.Kind == "rule" && containsString(region.Rules, "no-wires") || region.Kind == "copper" && region.Net != d.Net
			if !blocked {
				continue
			}
			if polylineHitsRegion(a, z, region, margin) {
				return reject("region", region.ID, "", region.Net, "")
			}
		}
		for _, candidate := range selected {
			for _, route := range candidate.Routes {
				if route.Net == d.Net || route.Layer != layer {
					continue
				}
				for i := 1; i < len(route.Points); i++ {
					if segmentDistance(a, z, route.Points[i-1], route.Points[i]) < d.Width/2+route.Width/2+math.Max(board.Rules.Clearance, board.Rules.TrackTrack)-1e-7 {
						return reject("reservation", route.ID, "", route.Net, candidate.DemandID)
					}
				}
			}
			for _, via := range candidate.Vias {
				if via.Net != d.Net && pointSegmentDistance(via.At, a, z) < d.Width/2+via.Diameter/2+board.Rules.Clearance-1e-7 {
					return reject("reservation-via", via.ID, "", via.Net, candidate.DemandID)
				}
			}
		}
		return true
	}
}

func viaClear(board pcbmodel.Board, d Demand, at pcbmodel.Point, selected []pcbrouting.JointCandidate, own []pcbmodel.Point) bool {
	r := board.Rules.ViaDiameter/2 + board.Rules.Clearance
	if !board.Outline.Contains(at) || distanceToOutline(board.Outline, at) < board.Rules.ViaDiameter/2+board.Rules.CopperToEdge-1e-7 {
		return false
	}
	for _, c := range board.Components {
		for _, pad := range c.Pads {
			if pad.Net != d.Net && padPointDistance(pad, at) < r-1e-7 {
				return false
			}
		}
	}
	for _, track := range board.Tracks {
		if track.Net == d.Net {
			continue
		}
		for i := 1; i < len(track.Points); i++ {
			if pointSegmentDistance(at, track.Points[i-1], track.Points[i]) < board.Rules.ViaDiameter/2+track.Width/2+board.Rules.Clearance-1e-7 {
				return false
			}
		}
	}
	for _, via := range board.Vias {
		if via.Net != d.Net && pointDistance(at, via.At) < board.Rules.ViaDiameter/2+via.Diameter/2+board.Rules.Clearance-1e-7 {
			return false
		}
	}
	for _, region := range board.Regions {
		blocked := region.Kind == "rule" && containsString(region.Rules, "no-wires") || region.Kind == "copper" && region.Net != d.Net
		if blocked && polylineHitsRegion(at, at, region, r) {
			return false
		}
	}
	for _, candidate := range selected {
		for _, via := range candidate.Vias {
			if via.Net != d.Net && pointDistance(at, via.At) < board.Rules.ViaDiameter/2+via.Diameter/2+board.Rules.Clearance-1e-7 {
				return false
			}
		}
		for _, route := range candidate.Routes {
			if route.Net == d.Net {
				continue
			}
			for i := 1; i < len(route.Points); i++ {
				if pointSegmentDistance(at, route.Points[i-1], route.Points[i]) < board.Rules.ViaDiameter/2+route.Width/2+board.Rules.Clearance-1e-7 {
					return false
				}
			}
		}
	}
	for _, p := range own {
		if pointDistance(at, p) < board.Rules.ViaDiameter+board.Rules.Clearance-1e-7 {
			return false
		}
	}
	return true
}

func jointMetrics(c pcbrouting.JointCandidate) (float64, int) {
	var length float64
	var bends int
	for _, r := range c.Routes {
		l, b := pcbrouting.Metrics(r.Points)
		length += l
		bends += b
	}
	return length, bends
}

func padOnLayer(p pcbmodel.Pad, layer int) bool {
	return p.Layer == pcbmodel.LayerMulti || p.Layer == layer
}
func hasVia(vias []pcbrouting.RouteVia, at pcbmodel.Point, from, to int) bool {
	for _, via := range vias {
		if via.At == at && (via.From == from && via.To == to || via.From == to && via.To == from) {
			return true
		}
	}
	return false
}

func appendSelected(in []pcbrouting.JointCandidate, value pcbrouting.JointCandidate) []pcbrouting.JointCandidate {
	out := append([]pcbrouting.JointCandidate(nil), in...)
	return append(out, value)
}

func withoutDemand(in []pcbrouting.JointCandidate, id string) []pcbrouting.JointCandidate {
	var out []pcbrouting.JointCandidate
	for _, c := range in {
		if c.DemandID != id {
			out = append(out, c)
		}
	}
	return out
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Geometry helpers intentionally operate on exact measured centers/extents.
func padSegmentDistance(p pcbmodel.Pad, a, z pcbmodel.Point) float64 {
	angle := -p.Rotation * math.Pi / 180
	local := func(q pcbmodel.Point) pcbmodel.Point {
		x, y := q[0]-p.Center[0], q[1]-p.Center[1]
		return pcbmodel.Point{x*math.Cos(angle) - y*math.Sin(angle), x*math.Sin(angle) + y*math.Cos(angle)}
	}
	a, z = local(a), local(z)
	switch strings.ToLower(p.Shape) {
	case "circle":
		return math.Max(0, pointSegmentDistance(pcbmodel.Point{}, a, z)-p.Width/2)
	case "oval":
		if p.Width >= p.Height {
			half := (p.Width - p.Height) / 2
			return math.Max(0, segmentDistance(a, z, pcbmodel.Point{-half, 0}, pcbmodel.Point{half, 0})-p.Height/2)
		}
		half := (p.Height - p.Width) / 2
		return math.Max(0, segmentDistance(a, z, pcbmodel.Point{0, -half}, pcbmodel.Point{0, half})-p.Width/2)
	case "roundrect":
		radius := math.Max(0, math.Min(p.CornerRadius, math.Min(p.Width, p.Height)/2))
		return math.Max(0, segmentRectDistance(a, z, -p.Width/2+radius, -p.Height/2+radius, p.Width/2-radius, p.Height/2-radius)-radius)
	default:
		return segmentRectDistance(a, z, -p.Width/2, -p.Height/2, p.Width/2, p.Height/2)
	}
}

func padPointDistance(p pcbmodel.Pad, at pcbmodel.Point) float64 {
	return padSegmentDistance(p, at, at)
}

func segmentRectDistance(a, b pcbmodel.Point, x0, y0, x1, y1 float64) float64 {
	if pointInRect(a, x0, y0, x1, y1) || pointInRect(b, x0, y0, x1, y1) {
		return 0
	}
	edges := [][2]pcbmodel.Point{{{x0, y0}, {x1, y0}}, {{x1, y0}, {x1, y1}}, {{x1, y1}, {x0, y1}}, {{x0, y1}, {x0, y0}}}
	best := math.Inf(1)
	for _, e := range edges {
		best = math.Min(best, segmentDistance(a, b, e[0], e[1]))
	}
	return best
}

func pointInRect(p pcbmodel.Point, x0, y0, x1, y1 float64) bool {
	return p[0] >= x0 && p[0] <= x1 && p[1] >= y0 && p[1] <= y1
}

func pointSegmentDistance(p, a, b pcbmodel.Point) float64 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	if dx == 0 && dy == 0 {
		return pointDistance(p, a)
	}
	t := ((p[0]-a[0])*dx + (p[1]-a[1])*dy) / (dx*dx + dy*dy)
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(p[0]-(a[0]+t*dx), p[1]-(a[1]+t*dy))
}

func segmentDistance(a, b, c, d pcbmodel.Point) float64 {
	if segmentsIntersect(a, b, c, d) {
		return 0
	}
	return math.Min(math.Min(pointSegmentDistance(a, c, d), pointSegmentDistance(b, c, d)), math.Min(pointSegmentDistance(c, a, b), pointSegmentDistance(d, a, b)))
}

func segmentsIntersect(a, b, c, d pcbmodel.Point) bool {
	orient := func(p, q, r pcbmodel.Point) float64 { return (q[0]-p[0])*(r[1]-p[1]) - (q[1]-p[1])*(r[0]-p[0]) }
	o1, o2, o3, o4 := orient(a, b, c), orient(a, b, d), orient(c, d, a), orient(c, d, b)
	return o1*o2 <= 1e-12 && o3*o4 <= 1e-12 && math.Max(math.Min(a[0], b[0]), math.Min(c[0], d[0])) <= math.Min(math.Max(a[0], b[0]), math.Max(c[0], d[0]))+1e-7 && math.Max(math.Min(a[1], b[1]), math.Min(c[1], d[1])) <= math.Min(math.Max(a[1], b[1]), math.Max(c[1], d[1]))+1e-7
}

func segmentInsideOutline(o pcbmodel.Outline, a, b pcbmodel.Point, margin float64) bool {
	if !o.Contains(a) || !o.Contains(b) {
		return false
	}
	for i, p := range o.Points {
		q := o.Points[(i+1)%len(o.Points)]
		if segmentDistance(a, b, p, q) < margin-1e-7 {
			return false
		}
	}
	// Concave polygons can reject a segment whose endpoints are both inside.
	for i := 1; i < 16; i++ {
		t := float64(i) / 16
		if !o.Contains(pcbmodel.Point{a[0] + (b[0]-a[0])*t, a[1] + (b[1]-a[1])*t}) {
			return false
		}
	}
	return true
}

func distanceToOutline(o pcbmodel.Outline, p pcbmodel.Point) float64 {
	best := math.Inf(1)
	for i, a := range o.Points {
		best = math.Min(best, pointSegmentDistance(p, a, o.Points[(i+1)%len(o.Points)]))
	}
	return best
}

func polylineHitsRegion(a, b pcbmodel.Point, region pcbmodel.Region, margin float64) bool {
	contours := region.Contours
	if len(contours) == 0 && len(region.Points) > 0 {
		contours = [][]pcbmodel.Point{region.Points}
	}
	if len(contours) == 0 {
		return true
	}
	if pointInCompound(a, contours) || pointInCompound(b, contours) || pointInCompound(pcbmodel.Point{(a[0] + b[0]) / 2, (a[1] + b[1]) / 2}, contours) {
		return true
	}
	for _, poly := range contours {
		for i, p := range poly {
			if segmentDistance(a, b, p, poly[(i+1)%len(poly)]) < margin-1e-7 {
				return true
			}
		}
	}
	return false
}

func pointInCompound(p pcbmodel.Point, contours [][]pcbmodel.Point) bool {
	winding := 0
	for _, poly := range contours {
		for i, a := range poly {
			b := poly[(i+1)%len(poly)]
			if pointSegmentDistance(p, a, b) <= 1e-7 {
				return true
			}
			cross := (b[0]-a[0])*(p[1]-a[1]) - (p[0]-a[0])*(b[1]-a[1])
			if a[1] <= p[1] && b[1] > p[1] && cross > 0 {
				winding++
			} else if a[1] > p[1] && b[1] <= p[1] && cross < 0 {
				winding--
			}
		}
	}
	return winding != 0
}

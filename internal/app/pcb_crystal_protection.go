package app

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Protection search keeps the sensitive no-pour envelope independent from the
// local component guard. Every proposed copper segment is checked against the
// measured board; search coordinates are seeds, never clearance evidence.
func assembleCrystalObstacleProtection(bundle *pcbLayoutModuleBundle, g *pcbCrystalGuardSpec, moved, all map[string]boardComp, snap *boardSnapshot, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea, keepout layoutBBox) error {
	pads, err := crystalCandidatePads(snap, moved)
	if err != nil {
		return err
	}
	if err := validateCrystalRoutes(&pcbLayoutModuleBundle{SignalRoutes: bundle.SignalRoutes}, snap, pads, tracks, arcs, vias, areas); err != nil {
		return fmt.Errorf("signal prerequisites before protection: %w", err)
	}
	var memberBounds *layoutBBox
	for _, c := range moved {
		memberBounds = unionLayoutBBox(memberBounds, c.BBox)
	}
	hole, dia := g.ViaHoleMil, g.ViaDiameterMil
	if hole == 0 {
		hole = snap.Rules.ViaDrillMil
	}
	if dia == 0 {
		dia = snap.Rules.ViaDiameterMil
	}
	if hole <= 0 || dia <= hole {
		return fmt.Errorf("invalid protection via dimensions")
	}
	var failures []string
	// Search different outward offsets. It is intentionally finite and keeps the
	// fixed MCU and every non-member component untouched.
	offsets := g.ProtectionSearch.GuardOffsetsMil
	if len(offsets) == 0 {
		for extra := 0.0; extra <= g.ProtectionSearch.MaxDetourMil+netPathGeomEps; extra += g.ProtectionSearch.StepMil {
			offsets = append(offsets, extra)
		}
	}
	for _, extra := range offsets {
		trial := *bundle
		trial.Metrics = map[string]float64{}
		for k, v := range bundle.Metrics {
			trial.Metrics[k] = v
		}
		guard := expandLayoutBBox(*memberBounds, g.GuardGapMil+g.GuardWidthMil/2+extra)
		trial.Envelope = &guard
		protected, regionErr := crystalSensitiveContour(moved, bundle.SignalRoutes, g.KeepoutMarginMil, g.SignalWidthMil/2+math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil))
		if regionErr != nil {
			return regionErr
		}
		trial.Regions = []pcbModuleRegion{{ID: "crystal-no-pours-top", Layer: 1, RuleTypes: []string{"no-pours"}, Points: protected}, {ID: "crystal-no-pours-bottom", Layer: 2, RuleTypes: []string{"no-pours"}, Points: protected}}

		err = buildCrystalProtectionTrial(&trial, g, moved, all, snap, pads, tracks, arcs, vias, areas, guard, hole, dia)
		if err == nil {
			err = verifyCrystalProtectionOffline(&trial, g, snap, pads, tracks, arcs, vias, areas)
		}
		if err == nil {
			*bundle = trial
			return nil
		}
		failures = append(failures, fmt.Sprintf("offset %.3fmil: %v", extra, err))
	}
	return fmt.Errorf("crystal protection search exhausted: %s", strings.Join(failures, "; "))
}

func buildCrystalProtectionTrial(b *pcbLayoutModuleBundle, g *pcbCrystalGuardSpec, moved, all map[string]boardComp, snap *boardSnapshot, pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea, guard layoutBBox, hole, dia float64) error {
	obstacles := append([]pcbTrack(nil), tracks...)
	for _, r := range b.SignalRoutes {
		for i := 0; i+1 < len(r.Points); i++ {
			a, z := r.Points[i], r.Points[i+1]
			obstacles = append(obstacles, pcbTrack{ID: r.ID, Net: r.Net, Layer: r.Layer, Width: r.WidthMil, X1: a[0], Y1: a[1], X2: z[0], Y2: z[1]})
		}
	}
	clearLayer := map[int]func([2]float64, [2]float64) bool{}
	for _, layer := range []int{1, 2} {
		c, err := crystalRouteClear(g.GroundNet, layer, g.GuardWidthMil, snap, pads, obstacles, arcs, vias, areas)
		if err != nil {
			return err
		}
		clearLayer[layer] = c
	}
	clear := func(route pcbModuleRoute) bool {
		for i := 0; i+1 < len(route.Points); i++ {
			if !clearLayer[route.Layer](route.Points[i], route.Points[i+1]) {
				return false
			}
		}
		return true
	}
	add := func(id, role, from string, layer int, a, z [2]float64) error {
		route := pcbModuleRoute{ID: id, Role: role, From: from, Net: g.GroundNet, Layer: layer, WidthMil: g.GuardWidthMil}
		points, ok := crystalProtectionPath(a, z, g.ProtectionSearch, func(p [][2]float64) bool { route.Points = p; return clear(route) })
		if !ok {
			points, err := crystalGridRoute(a, z, g.ProtectionSearch.StepMil, g.ProtectionSearch.MaxDetourMil, clearLayer[layer])
			if err == nil {
				route.Points = points
				b.GroundRoutes = append(b.GroundRoutes, route)
				return nil
			}
			return fmt.Errorf("%s has no clear bounded path (%.3f,%.3f)-(%.3f,%.3f)", id, a[0], a[1], z[0], z[1])
		}
		route.Points = points
		b.GroundRoutes = append(b.GroundRoutes, route)
		return nil
	}
	// The closed guard contour is solved before opening signal entrances.
	// Conceptual contour construction ignores planned signals (which must cross
	// its eventual openings), but avoids all measured pads/baseline copper and
	// the three local member envelopes. A blocked rectangle seed is movable.
	guardAreas := append([]pcbCopperArea(nil), areas...)
	for _, ref := range sortedCrystalMemberRefs(moved) {
		guardAreas = append(guardAreas, pcbCopperArea{ID: ref, Net: "__guard_member__", Layer: 1, Kind: "member-envelope", Contours: [][][2]float64{rectPoints(*moved[ref].BBox)}})
	}
	guardClear, err := crystalRouteClear(g.GroundNet, 1, g.GuardWidthMil, snap, pads, tracks, arcs, vias, guardAreas)
	if err != nil {
		return err
	}
	outline, err := crystalObstacleContour(rectPoints(guard), g.ProtectionSearch, guardClear)
	if err != nil {
		return fmt.Errorf("guard contour: %w", err)
	}
	for _, ref := range sortedCrystalMemberRefs(moved) {
		for _, p := range rectPoints(*moved[ref].BBox) {
			if compoundWinding([][][2]float64{outline}, p) == 0 {
				return fmt.Errorf("guard contour does not enclose member %s", ref)
			}
		}
	}
	owner := all[g.OwnerRef]
	if compoundWinding([][][2]float64{outline}, [2]float64{owner.X, owner.Y}) != 0 {
		return fmt.Errorf("guard contour must not enclose fixed owner %s", g.OwnerRef)
	}
	b.GuardContour = outline
	for side, a := range outline {
		z := outline[(side+1)%len(outline)]
		segments := crystalGuardOpenSegments(a, z, b.SignalRoutes, g.SignalWidthMil/2+g.GuardWidthMil/2+snap.Rules.ClearanceMil)
		for i, segment := range segments {
			r := pcbModuleRoute{ID: fmt.Sprintf("guard-side-%d-%d", side, i), Role: "guard", Net: g.GroundNet, Layer: 1, WidthMil: g.GuardWidthMil, Points: [][2]float64{segment[0], segment[1]}}
			if !clear(r) {
				return fmt.Errorf("guard contour signal entrance %d/%d violates clearance", side, i)
			}
			b.GroundRoutes = append(b.GroundRoutes, r)
		}
	}
	// Route all four mandatory local ground pads to reachable guard segments.
	var local []string
	for _, p := range g.Ports {
		local = append(local, p.CapacitorRef+"."+p.CapacitorGroundPad)
	}
	for _, p := range g.CrystalGroundPads {
		local = append(local, g.CrystalRef+"."+p)
	}
	for _, ref := range local {
		r, n, _ := splitPadRef(ref)
		pad, _ := findBoardPadExact(moved[r], n, "")
		start := [2]float64{pad.X, pad.Y}
		targets := crystalGuardTargets(b.GroundRoutes, start)
		ok := false
		for _, target := range targets {
			if add("ground-"+strings.ToLower(ref), "ground-spoke", ref, 1, start, target) == nil {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("ground pad %s cannot reach guard", ref)
		}
	}
	fence := expandLayoutBBox(guard, g.FenceMarginMil)
	fenceTracks := append([]pcbTrack(nil), tracks...)
	for _, r := range b.SignalRoutes {
		for i := 0; i+1 < len(r.Points); i++ {
			a, z := r.Points[i], r.Points[i+1]
			fenceTracks = append(fenceTracks, pcbTrack{ID: r.ID, Net: r.Net, Layer: r.Layer, Width: r.WidthMil, X1: a[0], Y1: a[1], X2: z[0], Y2: z[1]})
		}
	}
	// Route a closed via-center contour around measured pads and baseline copper.
	// Signal entrances may cross this non-copper contour; exact candidate-site
	// checks below enforce signal clearance and the maximum remaining pitch.
	fencePads := append([]pcbPadP(nil), pads...)
	for i := range fencePads {
		fencePads[i].Net = "__fence_pad_obstacle__"
	}
	var fenceAreas []pcbCopperArea
	for _, ref := range sortedCrystalMemberRefs(moved) {
		envelope := expandLayoutBBox(*moved[ref].BBox, g.KeepoutMarginMil)
		fenceAreas = append(fenceAreas, pcbCopperArea{ID: ref, Net: "__local_sensitive__", Layer: 1, Kind: "local-sensitive-envelope", Contours: [][][2]float64{rectPoints(envelope)}})
	}
	// A no-pours region is not copper: the conceptual contour must stay
	// outside it, but no trace-width/clearance halo is imposed on that line.
	// Via annuli may straddle no-pours (explicit copper is permitted there);
	// actual pad/copper/edge clearance is still checked at every selected site.
	fenceClear := func(a, z [2]float64) bool {
		if !snap.Outline.containsPoint(a[0], a[1]) || !snap.Outline.containsPoint(z[0], z[1]) {
			return false
		}
		for _, area := range fenceAreas {
			if copperAreaSegmentDistance(area, a, z) <= netPathGeomEps {
				return false
			}
		}
		for i, p := range snap.Outline.Points {
			q := snap.Outline.Points[(i+1)%len(snap.Outline.Points)]
			if segSegDist(a[0], a[1], z[0], z[1], p[0], p[1], q[0], q[1]) < netPathGeomEps {
				return false
			}
		}
		return true
	}

	fc := rectPoints(fence)
	// Rectangle corners are only seeds. Relocate a blocked seed to the nearest
	// measured-clear center; otherwise a pad under one arbitrary corner would
	// reject an otherwise traversable perimeter.
	for i, p := range fc {
		if fenceClear(p, p) {
			continue
		}
		var alternatives [][2]float64
		for dx := -g.ProtectionSearch.MaxDetourMil; dx <= g.ProtectionSearch.MaxDetourMil; dx += g.ProtectionSearch.StepMil {
			for dy := -g.ProtectionSearch.MaxDetourMil; dy <= g.ProtectionSearch.MaxDetourMil; dy += g.ProtectionSearch.StepMil {
				if math.Hypot(dx, dy) > g.ProtectionSearch.MaxDetourMil {
					continue
				}
				q := [2]float64{p[0] + dx, p[1] + dy}
				if fenceClear(q, q) {
					alternatives = append(alternatives, q)
				}
			}
		}
		sort.SliceStable(alternatives, func(a, b int) bool {
			return math.Hypot(alternatives[a][0]-p[0], alternatives[a][1]-p[1]) < math.Hypot(alternatives[b][0]-p[0], alternatives[b][1]-p[1])
		})
		if len(alternatives) == 0 {
			return fmt.Errorf("fence corner %d has no legal site within %.3fmil", i, g.ProtectionSearch.MaxDetourMil)
		}
		fc[i] = alternatives[0]
	}
	var contour [][2]float64
	for i, a := range fc {
		z := fc[(i+1)%len(fc)]
		p, err := crystalGridRoute(a, z, g.ProtectionSearch.StepMil, g.ProtectionSearch.MaxDetourMil, fenceClear)
		if err != nil {
			return fmt.Errorf("fence contour side %d (%.3f,%.3f)->(%.3f,%.3f), endpointClear=%t/%t: %w", i, a[0], a[1], z[0], z[1], fenceClear(a, a), fenceClear(z, z), err)
		}
		contour = append(contour, p[:len(p)-1]...)
	}
	if err := validateCrystalFenceContour(contour); err != nil {
		return err
	}
	for _, ref := range sortedCrystalMemberRefs(moved) {
		for _, p := range rectPoints(*moved[ref].BBox) {
			if compoundWinding([][][2]float64{contour}, p) == 0 {
				return fmt.Errorf("fence contour does not enclose member %s", ref)
			}
		}
	}
	siteClear := func(p [2]float64) bool {
		if compoundWinding([][][2]float64{b.Regions[0].Points}, p) != 0 {
			return false
		}
		v := preflightViaFence([][2]float64{p}, g.GroundNet, hole, dia, snap.Rules.ClearanceMil, snap.Rules.CopperToEdgeMil, snap.Outline, pads, fenceTracks, arcs, vias, areas)
		return len(v.Problems) == 0 && len(v.Create) == 1
	}
	var points [][2]float64
	var maxGap float64
	for attempt := 0; attempt < 16; attempt++ {
		points, maxGap, err = crystalFenceSelectContour(contour, g.FencePitchMil, g.ProtectionSearch.StepMil, dia+snap.Rules.ClearanceMil, siteClear)
		if err == nil {
			break
		}
		gap, ok := err.(*crystalFenceGapError)
		if !ok {
			return err
		}
		detour, e := crystalFenceSitePath(gap.From, gap.To, g.ProtectionSearch.StepMil, g.ProtectionSearch.MaxDetourMil, g.FencePitchMil, dia+snap.Rules.ClearanceMil, fenceClear, siteClear)
		if e != nil {
			return fmt.Errorf("%w; blocked-interval detour: %v", err, e)
		}
		contour = crystalReplaceContourInterval(contour, gap.Start, gap.End, detour)
		if e := validateCrystalFenceContour(contour); e != nil {
			return e
		}
	}
	if err != nil {
		return err
	}

	for _, ref := range sortedCrystalMemberRefs(moved) {
		for _, p := range rectPoints(*moved[ref].BBox) {
			if compoundWinding([][][2]float64{contour}, p) == 0 {
				return fmt.Errorf("replanned fence contour does not enclose member %s", ref)
			}
		}
	}
	if compoundWinding([][][2]float64{contour}, [2]float64{owner.X, owner.Y}) != 0 {
		return fmt.Errorf("local fence contour must not enclose fixed owner %s", g.OwnerRef)
	}
	b.FenceContour = contour
	b.FencePitchMil = g.FencePitchMil
	b.Metrics["fenceMaxPitchMil"] = maxGap
	b.Metrics["fencePitchLimitMil"] = g.FencePitchMil
	for i, p := range points {
		b.Vias = append(b.Vias, pcbModuleVia{ID: fmt.Sprintf("fence-%02d", i+1), Role: "fence", Net: g.GroundNet, X: p[0], Y: p[1], HoleMil: hole, DiameterMil: dia})
	}
	var anchors [][2]float64
	for i, ref := range g.GroundAnchors {
		r, n, _ := splitPadRef(ref)
		pad, _ := findBoardPadExact(all[r], n, "")
		offset := math.Min(dia*.75, (pad.W-dia)/2)
		if offset <= 0 || pad.H < dia {
			return fmt.Errorf("anchor %s cannot fit two vias", ref)
		}
		for j, x := range []float64{pad.X - offset, pad.X + offset} {
			p := [2]float64{x, pad.Y}
			anchors = append(anchors, p)
			b.Vias = append(b.Vias, pcbModuleVia{ID: fmt.Sprintf("ground-anchor-%02d-%d", i, j), Role: "ground-anchor", Net: g.GroundNet, X: x, Y: pad.Y, HoleMil: hole, DiameterMil: dia})
			if err := add(fmt.Sprintf("anchor-stub-%d-%d", i, j), "ground-anchor-stub", ref, 1, [2]float64{pad.X, pad.Y}, p); err != nil {
				return err
			}
		}
	}
	used := map[int]bool{}
	for i := 0; i < 2; i++ {
		connected := false
		order := make([]int, len(points))
		for j := range order {
			order[j] = j
		}
		sort.SliceStable(order, func(a, c int) bool {
			return math.Hypot(points[order[a]][0]-anchors[i][0], points[order[a]][1]-anchors[i][1]) < math.Hypot(points[order[c]][0]-anchors[i][0], points[order[c]][1]-anchors[i][1])
		})
		for _, j := range order {
			if used[j] {
				continue
			}
			old := len(b.GroundRoutes)
			targets := crystalGuardTargets(b.GroundRoutes, points[j])
			for _, target := range targets {
				if add(fmt.Sprintf("guard-ground-top-%d", i), "ground-entry-top", "", 1, target, points[j]) != nil {
					continue
				}
				if add(fmt.Sprintf("guard-ground-bottom-%d", i), "ground-entry-bottom", "", 2, points[j], anchors[i]) == nil {
					connected = true
					used[j] = true
					break
				}
				b.GroundRoutes = b.GroundRoutes[:old]
			}
			if connected {
				break
			}
		}
		if !connected {
			return fmt.Errorf("cannot allocate separated guard ground entry %d", i)
		}
	}
	if g.GroundImplementation == "legacy-local-pours" {
		pour := fence
		for _, p := range anchors {
			pour = *unionLayoutBBox(&pour, &layoutBBox{MinX: p[0], MaxX: p[0], MinY: p[1], MaxY: p[1]})
		}
		pour = expandLayoutBBox(pour, g.LocalPourMarginMil)
		pourClearance := math.Max(snap.Rules.ClearanceMil, snap.Rules.ClearanceTrackTrackMil)
		b.Pours, err = crystalRingPours(pour, b.Regions[0].Points, pourClearance, g.GroundNet)
		if err != nil {
			return fmt.Errorf("local ring pours: %w", err)
		}
		b.Metrics["localPourKeepoutClearanceMil"] = pourClearance
	}
	return nil
}

func crystalGuardTargets(routes []pcbModuleRoute, p [2]float64) [][2]float64 {
	var out [][2]float64
	for _, r := range routes {
		if r.Role != "guard" {
			continue
		}
		for i := 0; i+1 < len(r.Points); i++ {
			a, z := r.Points[i], r.Points[i+1]
			dx, dy := z[0]-a[0], z[1]-a[1]
			d := dx*dx + dy*dy
			if d == 0 {
				continue
			}
			t := math.Max(0, math.Min(1, ((p[0]-a[0])*dx+(p[1]-a[1])*dy)/d))
			out = append(out, [2]float64{a[0] + t*dx, a[1] + t*dy}, a, z)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		return math.Hypot(out[i][0]-p[0], out[i][1]-p[1]) < math.Hypot(out[j][0]-p[0], out[j][1]-p[1])
	})
	return out
}

func crystalProtectionPath(a, z [2]float64, s *pcbCrystalProtectionSearch, valid func([][2]float64) bool) ([][2]float64, bool) {
	direct := crystal45Path(a, z)
	if valid(direct) {
		return direct, true
	}
	for d := s.StepMil; d <= s.MaxDetourMil+netPathGeomEps; d += s.StepMil {
		for _, delta := range [][2]float64{{d, 0}, {-d, 0}, {0, d}, {0, -d}, {d, d}, {d, -d}, {-d, d}, {-d, -d}} {
			if math.Hypot(delta[0], delta[1]) > s.MaxDetourMil+netPathGeomEps {
				continue
			}
			p := [2]float64{a[0] + delta[0], a[1] + delta[1]}
			q := [2]float64{z[0] + delta[0], z[1] + delta[1]}
			route := crystal45Path(a, p)
			route = append(route, crystal45Path(p, q)[1:]...)
			route = append(route, crystal45Path(q, z)[1:]...)
			if valid(route) {
				smooth, ok := crystalBevelRoute(crystalCompressRoute(route), func(a, z [2]float64) bool { return valid([][2]float64{a, z}) })
				if ok {
					return smooth, true
				}
			}
		}
	}
	return nil, false
}

// Select along the entire closed perimeter. No blocked point is simply dropped:
// each retained interval must meet pitch, including the closing interval.
func crystalFenceSelect(rect layoutBBox, pitch, step, minDistance float64, clear func([2]float64) bool) ([][2]float64, float64, error) {
	return crystalFenceSelectContour(rectPoints(rect), pitch, step, minDistance, clear)
}
func crystalFenceSelectContour(contour [][2]float64, pitch, step, minDistance float64, clear func([2]float64) bool) ([][2]float64, float64, error) {
	length := 0.0
	for i, p := range contour {
		q := contour[(i+1)%len(contour)]
		length += math.Hypot(q[0]-p[0], q[1]-p[1])
	}
	count := int(math.Ceil(length / step))
	if count < 4 || count > 10000 {
		return nil, 0, fmt.Errorf("fence sampling limit exceeded: %d", count)
	}
	point := func(t float64) [2]float64 {
		for i, p := range contour {
			q := contour[(i+1)%len(contour)]
			l := math.Hypot(q[0]-p[0], q[1]-p[1])
			if t <= l {
				return [2]float64{p[0] + t/l*(q[0]-p[0]), p[1] + t/l*(q[1]-p[1])}
			}
			t -= l
		}
		return contour[0]
	}
	type event struct {
		t float64
		p [2]float64
	}
	var events []event
	for i := 0; i < count; i++ {
		t := float64(i) * length / float64(count)
		p := point(t)
		if clear(p) {
			events = append(events, event{t, p})
		}
	}
	// Keep proven legal contour vertices as sampling events. A narrow legal
	// site introduced by a detour must not disappear on a re-spaced grid.
	vertexT := 0.0
	for i, p := range contour {
		if clear(p) {
			events = append(events, event{vertexT, p})
		}
		q := contour[(i+1)%len(contour)]
		vertexT += math.Hypot(q[0]-p[0], q[1]-p[1])
	}
	sort.Slice(events, func(i, j int) bool { return events[i].t < events[j].t })
	uniqueEvents := events[:0]
	for _, e := range events {
		if len(uniqueEvents) == 0 || e.t-uniqueEvents[len(uniqueEvents)-1].t > 1e-6 {
			uniqueEvents = append(uniqueEvents, e)
		}
	}
	events = uniqueEvents
	maxBlocked := 0.0
	var blockedEnds [2][2]float64
	var blockedStart, blockedEnd float64
	if len(events) > 1 {
		for i, e := range events {
			next := events[(i+1)%len(events)].t
			if i == len(events)-1 {
				next += length
			}
			if next-e.t > maxBlocked {
				maxBlocked = next - e.t
				blockedEnds = [2][2]float64{e.p, events[(i+1)%len(events)].p}
				blockedStart, blockedEnd = e.t, next
			}
		}
	}
	if len(events) < 2 {
		return nil, 0, fmt.Errorf("fence has fewer than two legal perimeter sites")
	}
	// Try every clear origin; greedy farthest legal advancement bounds total work.
	for origin := range events {
		selected := []event{events[origin]}
		last := events[origin]
		limit := last.t + length
		ok := true
		for limit-last.t > pitch+netPathGeomEps {
			best := -1
			var next event
			for k := 1; k < len(events); k++ {
				e := events[(origin+k)%len(events)]
				if e.t <= events[origin].t {
					e.t += length
				}
				if e.t <= last.t || e.t-last.t > pitch {
					continue
				}
				if math.Hypot(e.p[0]-last.p[0], e.p[1]-last.p[1]) < minDistance {
					continue
				}
				if best < 0 || e.t > next.t {
					best = k
					next = e
				}
			}
			if best < 0 {
				ok = false
				break
			}
			selected = append(selected, next)
			last = next
		}
		if !ok || len(selected) < 2 || math.Hypot(last.p[0]-selected[0].p[0], last.p[1]-selected[0].p[1]) < minDistance {
			continue
		}
		out := make([][2]float64, len(selected))
		maxGap := limit - last.t
		for i, e := range selected {
			out[i] = e.p
			if i > 0 {
				maxGap = math.Max(maxGap, e.t-selected[i-1].t)
			}
		}
		return out, round4(maxGap), nil
	}
	return nil, 0, &crystalFenceGapError{Pitch: pitch, Length: length, MaxGap: maxBlocked, Start: blockedStart, End: blockedEnd, From: blockedEnds[0], To: blockedEnds[1], Sites: len(events)}
}

func verifyCrystalFencePitch(candidate pcbLayoutCandidate) error {
	b := candidate.Bundle
	if b == nil || len(b.FenceContour) == 0 {
		return nil
	}
	if err := validateCrystalFenceContour(b.FenceContour); err != nil {
		return err
	}
	if len(b.FenceContour) < 3 || !allFinite(b.FencePitchMil) || b.FencePitchMil <= 0 {
		return fmt.Errorf("invalid fence contour/pitch contract")
	}
	var positions []float64
	perimeter := 0.0
	for _, v := range b.Vias {
		if v.Role != "fence" {
			continue
		}
		distance := 0.0
		found := false
		for i, a := range b.FenceContour {
			z := b.FenceContour[(i+1)%len(b.FenceContour)]
			length := math.Hypot(z[0]-a[0], z[1]-a[1])
			if segPtDist(v.X, v.Y, a[0], a[1], z[0], z[1]) <= 1e-4 {
				positions = append(positions, distance+math.Hypot(v.X-a[0], v.Y-a[1]))
				found = true
				break
			}
			distance += length
		}
		if !found {
			return fmt.Errorf("fence via %s is off declared contour", v.ID)
		}
	}
	for i, a := range b.FenceContour {
		z := b.FenceContour[(i+1)%len(b.FenceContour)]
		perimeter += math.Hypot(z[0]-a[0], z[1]-a[1])
	}
	if len(positions) < 2 {
		return fmt.Errorf("fence contains fewer than two sites")
	}
	sort.Float64s(positions)
	for i, p := range positions {
		next := positions[(i+1)%len(positions)]
		if i == len(positions)-1 {
			next += perimeter
		}
		if next-p > b.FencePitchMil+1e-4 {
			return fmt.Errorf("fence perimeter gap %.4fmil exceeds declared pitch %.4fmil", next-p, b.FencePitchMil)
		}
	}
	return nil
}

// Conservative one-mil interval clipping of all signal entry capsules. Segment
// endpoints are outside the closed clearance capsule, so sampling cannot invent
// clearance at an entrance.
func crystalGuardOpenSegments(a, z [2]float64, routes []pcbModuleRoute, margin float64) [][2][2]float64 {
	length := math.Hypot(z[0]-a[0], z[1]-a[1])
	count := int(math.Ceil(length))
	if count < 1 {
		return nil
	}
	point := func(i int) [2]float64 {
		t := float64(i) / float64(count)
		return [2]float64{a[0] + t*(z[0]-a[0]), a[1] + t*(z[1]-a[1])}
	}
	var out [][2][2]float64
	start := -1
	for i := 0; i < count; i++ {
		p, q := point(i), point(i+1)
		safe := true
		for _, r := range routes {
			for j := 0; j+1 < len(r.Points); j++ {
				u, v := r.Points[j], r.Points[j+1]
				if segSegDist(p[0], p[1], q[0], q[1], u[0], u[1], v[0], v[1]) <= margin+1e-6 {
					safe = false
					break
				}
			}
			if !safe {
				break
			}
		}
		if safe && start < 0 {
			start = i
		}
		if !safe && start >= 0 {
			out = append(out, [2][2]float64{point(start), p})
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, [2][2]float64{point(start), z})
	}
	return out
}

func verifyCrystalProtectionOffline(b *pcbLayoutModuleBundle, g *pcbCrystalGuardSpec, snap *boardSnapshot, pads []pcbPadP, tracks []pcbTrack, arcs []pcbArc, vias []pcbViaP, areas []pcbCopperArea) error {
	allTracks := append([]pcbTrack(nil), tracks...)
	allVias := append([]pcbViaP(nil), vias...)
	for _, r := range append(append([]pcbModuleRoute(nil), b.SignalRoutes...), b.GroundRoutes...) {
		for i := 0; i+1 < len(r.Points); i++ {
			a, z := r.Points[i], r.Points[i+1]
			allTracks = append(allTracks, pcbTrack{ID: fmt.Sprintf("planned:%s:%d", r.ID, i), Net: r.Net, Layer: r.Layer, Width: r.WidthMil, X1: a[0], Y1: a[1], X2: z[0], Y2: z[1]})
		}
	}
	for _, v := range b.Vias {
		allVias = append(allVias, pcbViaP{ID: "planned:" + v.ID, Net: v.Net, X: v.X, Y: v.Y, Hole: v.HoleMil, Dia: v.DiameterMil})
	}
	for _, r := range append(append([]pcbModuleRoute(nil), b.SignalRoutes...), b.GroundRoutes...) {
		clear, err := crystalRouteClear(r.Net, r.Layer, r.WidthMil, snap, pads, allTracks, arcs, allVias, areas)
		if err != nil {
			return err
		}
		for i := 0; i+1 < len(r.Points); i++ {
			if !clear(r.Points[i], r.Points[i+1]) {
				return fmt.Errorf("exact planned route %s segment %d violates copper clearance", r.ID, i)
			}
		}
	}
	for i, v := range b.Vias {
		for j := 0; j < i; j++ {
			u := b.Vias[j]
			if math.Hypot(v.X-u.X, v.Y-u.Y) < (v.DiameterMil+u.DiameterMil)/2+snap.Rules.ClearanceMil-netPathGeomEps {
				return fmt.Errorf("planned vias %s/%s violate separation", v.ID, u.ID)
			}
		}
	}
	if err := verifyCrystalFencePitch(pcbLayoutCandidate{Bundle: b}); err != nil {
		return err
	}
	if err := verifyCrystalGuardSegmentsConnected(b, pads, allTracks, arcs, allVias, g.GroundAnchors[0], g.GroundNet); err != nil {
		return err
	}
	for _, r := range b.GroundRoutes {
		if r.Role != "ground-spoke" {
			continue
		}
		rep, err := analyzePcbNetPath(pads, allTracks, arcs, allVias, pcbNetPathOptions{From: r.From, To: g.GroundAnchors[0], Net: g.GroundNet})
		if err != nil || !rep.Connected {
			return fmt.Errorf("ground spoke %s is not connected to anchor: %v; route=%v", r.From, err, r.Points)
		}
	}
	return nil
}

func validateCrystalFenceContour(points [][2]float64) error {
	if len(points) < 3 {
		return fmt.Errorf("fence contour requires at least three vertices")
	}
	for i, a := range points {
		b := points[(i+1)%len(points)]
		if !allFinite(a[0], a[1]) || math.Hypot(b[0]-a[0], b[1]-a[1]) < 1e-6 {
			return fmt.Errorf("fence contour has invalid or zero-length edge %d", i)
		}
		for j := i + 1; j < len(points); j++ {
			if j == i+1 || i == 0 && j == len(points)-1 {
				continue
			}
			c, d := points[j], points[(j+1)%len(points)]
			if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) < 1e-6 {
				return fmt.Errorf("fence contour self-intersects at edges %d/%d: %v-%v / %v-%v", i, j, a, b, c, d)
			}
		}
	}
	return nil
}

// A contour seed has no copper semantics. Migrate an obstructed seed, route
// between measured-clear sites, then validate the resulting closed geometry.
func crystalObstacleContour(seeds [][2]float64, s *pcbCrystalProtectionSearch, clear func([2]float64, [2]float64) bool) ([][2]float64, error) {
	seeds = append([][2]float64(nil), seeds...)
	for i, p := range seeds {
		if clear(p, p) {
			continue
		}
		var alternatives [][2]float64
		for dx := -s.MaxDetourMil; dx <= s.MaxDetourMil; dx += s.StepMil {
			for dy := -s.MaxDetourMil; dy <= s.MaxDetourMil; dy += s.StepMil {
				if math.Hypot(dx, dy) > s.MaxDetourMil {
					continue
				}
				q := [2]float64{p[0] + dx, p[1] + dy}
				if clear(q, q) {
					alternatives = append(alternatives, q)
				}
			}
		}
		sort.SliceStable(alternatives, func(a, b int) bool {
			return math.Hypot(alternatives[a][0]-p[0], alternatives[a][1]-p[1]) < math.Hypot(alternatives[b][0]-p[0], alternatives[b][1]-p[1])
		})
		if len(alternatives) == 0 {
			return nil, fmt.Errorf("seed %d at %.3f,%.3f has no legal site within %.3fmil", i, p[0], p[1], s.MaxDetourMil)
		}
		seeds[i] = alternatives[0]
	}
	var contour [][2]float64
	for i, a := range seeds {
		z := seeds[(i+1)%len(seeds)]
		// Avoid previously routed contour edges, allowing only the declared
		// shared corner and the final closing vertex. Otherwise independent
		// shortest paths can form a spur at a migrated seed.
		edgeClear := func(u, v [2]float64) bool {
			if !clear(u, v) {
				return false
			}
			for j := 0; j+1 < len(contour); j++ {
				allowed := [][2]float64{a}
				if i == len(seeds)-1 {
					allowed = append(allowed, seeds[0])
				}
				if !crystalContourSegmentsApart(u, v, contour[j], contour[j+1], allowed) {
					return false
				}
			}
			if len(contour) > 0 {
				allowed := [][2]float64{a}
				if i == len(seeds)-1 {
					allowed = append(allowed, seeds[0])
				}
				if !crystalContourSegmentsApart(u, v, contour[len(contour)-1], a, allowed) {
					return false
				}
			}
			return true
		}
		p, err := crystalGridRoute(a, z, s.StepMil, s.MaxDetourMil, edgeClear)
		if err != nil {
			return nil, fmt.Errorf("side %d (%.3f,%.3f)->(%.3f,%.3f), endpointClear=%t/%t: %w", i, a[0], a[1], z[0], z[1], clear(a, a), clear(z, z), err)
		}
		contour = append(contour, p[:len(p)-1]...)
	}
	if err := validateCrystalFenceContour(contour); err != nil {
		return nil, err
	}
	return contour, nil
}

func crystalContourSegmentsApart(a, b, c, d [2]float64, allowed [][2]float64) bool {
	if segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) > 1e-6 {
		return true
	}
	near := func(p, q [2]float64) bool { return math.Hypot(p[0]-q[0], p[1]-q[1]) < 1e-6 }
	trim := func(p, q [2]float64) [2]float64 {
		l := math.Hypot(q[0]-p[0], q[1]-p[1])
		if l < 1e-6 {
			return q
		}
		t := math.Min(.001/l, .25)
		return [2]float64{p[0] + t*(q[0]-p[0]), p[1] + t*(q[1]-p[1])}
	}
	for _, p := range allowed {
		if (near(a, p) || near(b, p)) && (near(c, p) || near(d, p)) {
			if near(a, b) {
				return true
			}
			if near(c, d) {
				return true
			}
			if near(a, p) {
				a = trim(a, b)
			}
			if near(b, p) {
				b = trim(b, a)
			}
			if near(c, p) {
				c = trim(c, d)
			}
			if near(d, p) {
				d = trim(d, c)
			}
			return segSegDist(a[0], a[1], b[0], b[1], c[0], c[1], d[0], d[1]) > 1e-6
		}
	}
	return false
}

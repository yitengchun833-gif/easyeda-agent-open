package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
)

// A replacement preview reuses the ordinary route report on the exact projected
// board after removing only the explicitly selected, independent track chain.
type pcbRouteReplaceReceipt struct {
	SchemaVersion   int                   `json:"schemaVersion"`
	Status          string                `json:"status"`
	DryRun          bool                  `json:"dryRun"`
	Target          actionContext         `json:"target"`
	BoardSHA256     string                `json:"boardSha256"`
	RequestSHA256   string                `json:"requestSha256"`
	PlanSHA256      string                `json:"planSha256,omitempty"`
	Remove          []map[string]any      `json:"remove"`
	Route           pcbRouteReport        `json:"route"`
	DeleteAttempted bool                  `json:"deleteAttempted"`
	DeleteRequestID string                `json:"deleteRequestId,omitempty"`
	DeleteResult    map[string]any        `json:"deleteResult,omitempty"`
	DeletedIDs      []string              `json:"deletedIds"`
	RemainingIDs    []string              `json:"remainingDeleteIds"`
	Addition        *pcbRouteApplyReceipt `json:"addition,omitempty"`
	Partial         bool                  `json:"partial"`
	Persistence     string                `json:"persistence"`
	Error           string                `json:"error,omitempty"`
}

func preparePCBRouteReplacement(before *boardSnapshot, in pcbRouteRequest, ids []string) (*boardSnapshot, []map[string]any, error) {
	if len(ids) == 0 {
		return nil, nil, fmt.Errorf("explicit track IDs are required")
	}
	projected, err := clonePCBLayoutSnapshot(before)
	if err != nil || projected.Copper == nil {
		return nil, nil, fmt.Errorf("complete copper baseline required: %v", err)
	}
	lines, err := routeApplyLineMaps(projected.Copper.Lines)
	if err != nil {
		return nil, nil, err
	}
	selected := map[string]bool{}
	var removed []map[string]any
	for _, id := range ids {
		line := lines[id]
		locked, lockKnown := line["locked"].(bool)
		if id == "" || selected[id] || line == nil || !lockKnown || locked || asString(line["net"]) != in.Net || asFloat(line["layer"]) != float64(in.Layer) {
			return nil, nil, fmt.Errorf("selected track %q is missing, repeated, locked/unknown or outside the requested net/layer", id)
		}
		for _, c := range before.Components {
			if c.ID == "" || len(id) > len(c.ID) && id[:len(c.ID)] == c.ID {
				return nil, nil, fmt.Errorf("selected track %s may be footprint-embedded", id)
			}
		}
		selected[id] = true
		removed = append(removed, line)
	}
	projected.Copper.Lines = []any{}
	for _, raw := range before.Copper.Lines {
		if !selected[asString(raw.(map[string]any)["primitiveId"])] {
			projected.Copper.Lines = append(projected.Copper.Lines, raw)
		}
	}
	// Validate supported pad shapes, layers, outline, rules and every obstacle before deletion.
	if _, _, _, err := preparePCBRouteRequest(in, projected); err != nil {
		return nil, nil, err
	}
	pads, err := crystalCandidatePads(before, nil)
	if err != nil {
		return nil, nil, err
	}
	tracks, _, vias, err := crystalSnapshotRouting(before)
	if err != nil {
		return nil, nil, err
	}
	var chain []pcbTrack
	for _, t := range tracks {
		if selected[t.ID] {
			chain = append(chain, t)
		}
	}
	if len(chain) != len(ids) {
		return nil, nil, fmt.Errorf("selected track geometry is incomplete")
	}
	if err := validatePCBReplacementChain(in, before, pads, tracks, chain, vias, selected); err != nil {
		return nil, nil, err
	}
	sort.Slice(removed, func(i, j int) bool { return asString(removed[i]["primitiveId"]) < asString(removed[j]["primitiveId"]) })
	return projected, removed, nil
}

func validatePCBReplacementChain(in pcbRouteRequest, before *boardSnapshot, pads []pcbPadP, tracks, chain []pcbTrack, vias []pcbViaP, selected map[string]bool) error {
	var nodes [][2]float64
	var edges [][2]int
	var adjacent [][]int
	node := func(p [2]float64) int {
		for i, q := range nodes {
			if routeApplyPointsNear(p, q) {
				return i
			}
		}
		nodes = append(nodes, p)
		adjacent = append(adjacent, nil)
		return len(nodes) - 1
	}
	for i, t := range chain {
		a, z := node([2]float64{t.X1, t.Y1}), node([2]float64{t.X2, t.Y2})
		if a == z {
			return fmt.Errorf("degenerate selected track %s", t.ID)
		}
		edges = append(edges, [2]int{a, z})
		adjacent[a] = append(adjacent[a], i)
		adjacent[z] = append(adjacent[z], i)
	}
	from, to := -1, -1
	for _, p := range pads {
		key := p.Designator + "." + p.Number
		if key == in.From || key == in.To {
			if p.Layer != in.Layer {
				return fmt.Errorf("replacement requires same-layer SMD endpoints; no through-hole/layer transitions")
			}
			for i, q := range nodes {
				if routeApplyPointsNear(q, [2]float64{p.X, p.Y}) {
					if key == in.From {
						from = i
					} else {
						to = i
					}
				}
			}
		}
	}
	if from < 0 || to < 0 || from == to {
		return fmt.Errorf("selected chain must end at both measured pad centers")
	}
	for i, a := range adjacent {
		want := 2
		if i == from || i == to {
			want = 1
		}
		if len(a) != want {
			return fmt.Errorf("selected copper is branched, incomplete or cyclic")
		}
	}
	visited := map[int]bool{}
	queue := []int{from}
	for len(queue) > 0 {
		i := queue[0]
		queue = queue[1:]
		if visited[i] {
			continue
		}
		visited[i] = true
		for _, e := range adjacent[i] {
			for _, j := range edges[e] {
				if !visited[j] {
					queue = append(queue, j)
				}
			}
		}
	}
	if len(visited) != len(nodes) {
		return fmt.Errorf("selected copper includes a disconnected chain or cycle")
	}
	for i, t := range chain {
		a, z := [2]float64{t.X1, t.Y1}, [2]float64{t.X2, t.Y2}
		for j, u := range chain {
			if j <= i {
				continue
			}
			shared := edges[i][0] == edges[j][0] || edges[i][0] == edges[j][1] || edges[i][1] == edges[j][0] || edges[i][1] == edges[j][1]
			if routeApplyCollinearOverlap(t, u) > netPathGeomEps || !shared && segSegDist(t.X1, t.Y1, t.X2, t.Y2, u.X1, u.Y1, u.X2, u.Y2) <= (t.Width+u.Width)/2+netPathGeomEps {
				return fmt.Errorf("selected chain overlaps or touches itself")
			}
		}
		for _, u := range tracks {
			if !selected[u.ID] && u.Layer == in.Layer && segSegDist(t.X1, t.Y1, t.X2, t.Y2, u.X1, u.Y1, u.X2, u.Y2) <= (t.Width+u.Width)/2+netPathGeomEps {
				return fmt.Errorf("selected track %s touches unselected copper %s", t.ID, u.ID)
			}
		}
		for _, v := range vias {
			if segPointDist(t.X1, t.Y1, t.X2, t.Y2, v.X, v.Y) <= (t.Width+v.Dia)/2+netPathGeomEps {
				return fmt.Errorf("selected track %s touches via %s", t.ID, v.ID)
			}
		}
		for _, p := range pads {
			if !padLayerMatches(p.Layer, in.Layer) {
				continue
			}
			gap, err := pcbExactPadSegmentGap(p, a, z)
			if err != nil {
				return err
			}
			if gap > t.Width/2+netPathGeomEps {
				continue
			}
			key := p.Designator + "." + p.Number
			if (key != in.From && key != in.To) || !routeApplyPointsNear(a, [2]float64{p.X, p.Y}) && !routeApplyPointsNear(z, [2]float64{p.X, p.Y}) {
				return fmt.Errorf("selected chain touches a third pad or its terminal away from the chain endpoint")
			}
		}
	}
	// Same-net areas can hide branches. First version refuses that unsupported case.
	for _, rows := range [][]any{before.Copper.Pours, before.Copper.Poured, before.Copper.Fills} {
		for _, raw := range rows {
			m, ok := raw.(map[string]any)
			if !ok {
				return fmt.Errorf("area inventory unavailable")
			}
			if asString(m["net"]) == in.Net && (asFloat(m["layer"]) == float64(in.Layer) || asFloat(m["layer"]) == pcbLayerMulti) {
				return fmt.Errorf("same-net copper areas on the replacement layer are not supported")
			}
		}
	}
	return nil
}

func nativeTrackDeleteComplete(result map[string]any, selected []map[string]any) bool {
	ids := make([]string, len(selected))
	for i, line := range selected {
		ids[i] = asString(line["primitiveId"])
	}
	if result == nil || result["ok"] == false {
		return false
	}
	deleted, ok := result["deleted"].(map[string]any)
	if !ok {
		return false
	}
	tracks, ok := deleted["tracks"].(map[string]any)
	if !ok || tracks["ok"] != true || asFloat(tracks["requested"]) != float64(len(ids)) || asFloat(result["count"]) != float64(len(ids)) {
		return false
	}
	for _, name := range []string{"notFound", "skippedLocked", "notDeleted", "notDeletable"} {
		if raw, present := result[name]; present {
			items, ok := raw.([]any)
			if !ok || len(items) != 0 {
				return false
			}
		}
	}
	removed, ok := result["removed"].([]any)
	if !ok || len(removed) != len(ids) {
		return false
	}
	want := make(map[string]map[string]any, len(ids))
	for _, line := range selected {
		want[asString(line["primitiveId"])] = line
	}
	for _, raw := range removed {
		item, ok := raw.(map[string]any)
		id := asString(item["primitiveId"])
		original := want[id]
		if !ok || original == nil || asString(item["kind"]) != "track" {
			return false
		}
		for _, field := range []string{"net", "layer", "startX", "startY", "endX", "endY", "lineWidth"} {
			if canonicalJSON(item[field]) != canonicalJSON(original[field]) {
				return false
			}
		}
		delete(want, id)
	}
	return len(want) == 0
}

func executePCBRouteReplacement(ctx context.Context, in pcbRouteRequest, before, projected *boardSnapshot, r *pcbRouteReplaceReceipt, call pcbRouteApplyCall, snapshot func() (*boardSnapshot, error), persist func() error) error {
	stop := func(status string, err error) error {
		r.Status, r.Error = status, err.Error()
		r.Partial = r.DeleteAttempted
		if e := persist(); e != nil {
			return fmt.Errorf("%w; receipt: %v", err, e)
		}
		return err
	}
	fresh, err := snapshot()
	if err != nil {
		return stop("not_executed", err)
	}
	want, err := routeApplyFacts(before, false)
	if err != nil {
		return stop("not_executed", err)
	}
	got, err := routeApplyFacts(fresh, false)
	if err != nil || want != got {
		return stop("not_executed", fmt.Errorf("fresh replacement baseline differs: %v", err))
	}
	if err := ctx.Err(); err != nil {
		return stop("not_executed", err)
	}
	ids := make([]string, len(r.Remove))
	for i, m := range r.Remove {
		ids[i] = asString(m["primitiveId"])
	}
	r.DeleteAttempted, r.Status = true, "deleting"
	if err := persist(); err != nil {
		r.DeleteAttempted = false
		return stop("not_executed", err)
	}
	if err := ctx.Err(); err != nil {
		r.DeleteAttempted = false
		return stop("not_executed", err)
	}
	res, deleteErr := call("pcb.route.delete", map[string]any{"kind": "track", "primitiveIds": ids, "expectedTracks": r.Remove})
	if res != nil {
		r.DeleteRequestID, r.DeleteResult = res.ID, res.Result
	}
	// Even a failed/unknown delete may have changed copper. Read once, record the
	// observed missing IDs, and never continue into additions after such an error.
	after, err := snapshot()
	if err != nil {
		return stop("unknown", fmt.Errorf("delete outcome unavailable: %v; readback: %w", deleteErr, err))
	}
	if after == nil || after.Copper == nil {
		return stop("unknown", fmt.Errorf("post-delete copper inventory unavailable"))
	}
	lines, err := routeApplyLineMaps(after.Copper.Lines)
	if err != nil {
		return stop("unknown", err)
	}
	r.DeletedIDs, r.RemainingIDs = []string{}, []string{}
	for _, id := range ids {
		if lines[id] == nil {
			r.DeletedIDs = append(r.DeletedIDs, id)
		} else {
			r.RemainingIDs = append(r.RemainingIDs, id)
		}
	}
	if deleteErr != nil {
		return stop("unknown", deleteErr)
	}
	if res == nil || res.Result == nil || res.Result["ok"] == false {
		return stop("partial", fmt.Errorf("native delete did not report success"))
	}
	if !nativeTrackDeleteComplete(res.Result, r.Remove) {
		return stop("partial", fmt.Errorf("native track delete did not confirm the exact selected scope; no additions dispatched"))
	}
	if len(r.RemainingIDs) > 0 {
		return stop("partial", fmt.Errorf("selected tracks survived; no replacement copper dispatched"))
	}
	want, err = routeApplyFacts(projected, false)
	if err != nil {
		return stop("partial", err)
	}
	got, err = routeApplyFacts(after, false)
	if err != nil || want != got {
		return stop("partial", fmt.Errorf("unexpected board changes after selected deletion: %v", err))
	}
	r.Addition = &pcbRouteApplyReceipt{SchemaVersion: 1, Status: "not_executed", Target: r.Target, BoardSHA256: r.Route.BoardSHA256, RequestSHA256: r.RequestSHA256, PlanSHA256: r.PlanSHA256, Candidate: *r.Route.Candidate, Steps: []pcbRouteApplyStep{}, Remaining: []int{}, Connectivity: map[string]any{"status": "not_checked"}, DRC: map[string]any{"status": "not_checked"}, Persistence: "unverified"}
	for i := 0; i+1 < len(r.Route.Candidate.Points); i++ {
		r.Addition.Remaining = append(r.Addition.Remaining, i)
	}
	// Hand off the just-verified post-delete observation; do not reread the whole
	// board between this boundary and the existing additive executor's preflight.
	first := after
	applySnapshot := func() (*boardSnapshot, error) {
		if first != nil {
			s := first
			first = nil
			return s, nil
		}
		return snapshot()
	}
	createNotDispatched := false
	applyCall := func(action string, payload map[string]any) (*actionResult, error) {
		if action == "pcb.line.create" {
			if err := ctx.Err(); err != nil {
				createNotDispatched = true
				return nil, err
			}
		}
		return call(action, payload)
	}
	if err := executePCBRouteApply(ctx, in, after, r.Addition, applyCall, applySnapshot, persist); err != nil {
		if createNotDispatched && len(r.Addition.Steps) > 0 {
			step := &r.Addition.Steps[len(r.Addition.Steps)-1]
			if step.Status == "unknown" && step.RequestID == "" {
				step.Status = "not_executed"
				r.Addition.Remaining = append([]int{step.Index}, r.Addition.Remaining...)
				r.Addition.Status, r.Addition.Partial = "not_executed", false
				if r.Addition.Applied {
					r.Addition.Status, r.Addition.Partial = "partial", true
				}
			}
		}
		status := "partial"
		if r.Addition.Status == "unknown" {
			status = "unknown"
		}
		return stop(status, err)
	}
	r.Status, r.Partial = "replaced", false
	if err := persist(); err != nil {
		r.Status, r.Partial, r.Error = "unknown", true, "receipt persistence failed: "+err.Error()
		return err
	}
	return nil
}

func marshalPCBRouteReplacement(r *pcbRouteReplaceReceipt) ([]byte, error) {
	b, e := json.MarshalIndent(r, "", "  ")
	return append(b, '\n'), e
}

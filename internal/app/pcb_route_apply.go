package app

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
)

type pcbRouteApplyStep struct {
	Index       int      `json:"index"`
	Status      string   `json:"status"`
	RequestID   string   `json:"requestId,omitempty"`
	PrimitiveID string   `json:"primitiveId,omitempty"`
	ObservedIDs []string `json:"observedIds,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type pcbRouteApplyReceipt struct {
	SchemaVersion    int                 `json:"schemaVersion"`
	Status           string              `json:"status"`
	Target           actionContext       `json:"target"`
	Window           string              `json:"window"`
	BoardSHA256      string              `json:"boardSha256"`
	RequestSHA256    string              `json:"requestSha256"`
	PlanSHA256       string              `json:"planSha256"`
	Candidate        pcbRouteCandidate   `json:"candidate"`
	DryRun           bool                `json:"dryRun"`
	Applied          bool                `json:"applied"`
	GeometryVerified bool                `json:"geometryVerified"`
	Verified         bool                `json:"verified"`
	Partial          bool                `json:"partial"`
	Steps            []pcbRouteApplyStep `json:"steps"`
	Remaining        []int               `json:"remaining"`
	Connectivity     any                 `json:"connectivity"`
	DRC              map[string]any      `json:"drc"`
	SaveRequested    bool                `json:"saveRequested"`
	Persistence      string              `json:"persistence"`
	Error            string              `json:"error,omitempty"`
}

type pcbRouteApplyCall func(string, map[string]any) (*actionResult, error)

// Compare ID-keyed facts, not capture timestamps, display labels or API ordering.
func routeApplyFacts(s *boardSnapshot, omitLines bool) (string, error) {
	if s == nil {
		return "", fmt.Errorf("board snapshot missing")
	}
	copy, err := clonePCBLayoutSnapshot(s)
	if err != nil {
		return "", err
	}
	copy.CapturedAt, copy.Project, copy.SemanticSHA256 = "", "", ""
	copy.RoutedLines = nil
	sort.Slice(copy.Components, func(i, j int) bool { return copy.Components[i].ID < copy.Components[j].ID })
	seen := map[string]bool{}
	for i := range copy.Components {
		c := &copy.Components[i]
		if c.ID == "" || seen[c.ID] {
			return "", fmt.Errorf("missing or duplicate component identity")
		}
		seen[c.ID] = true
		sort.Slice(c.Pads, func(i, j int) bool { return c.Pads[i].ID < c.Pads[j].ID })
		for _, p := range c.Pads {
			if p.ID == "" || seen[p.ID] {
				return "", fmt.Errorf("missing or duplicate pad identity")
			}
			seen[p.ID] = true
		}
	}
	sort.Slice(copy.Silk, func(i, j int) bool { return canonicalJSON(copy.Silk[i]) < canonicalJSON(copy.Silk[j]) })
	if copy.Copper == nil {
		return "", fmt.Errorf("copper inventory missing")
	}
	if omitLines {
		copy.Copper.Lines = []any{}
	}
	for _, items := range [][]any{copy.Copper.Lines, copy.Copper.Arcs, copy.Copper.Vias, copy.Copper.Pours, copy.Copper.Poured, copy.Copper.Regions, copy.Copper.Fills} {
		sort.Slice(items, func(i, j int) bool { return canonicalJSON(items[i]) < canonicalJSON(items[j]) })
	}
	return canonicalJSON(copy), nil
}

func routeApplyLineMaps(raw []any) (map[string]map[string]any, error) {
	if raw == nil {
		return nil, fmt.Errorf("line inventory unavailable")
	}
	byID := map[string]map[string]any{}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		id := asString(m["primitiveId"])
		if !ok || id == "" || byID[id] != nil {
			return nil, fmt.Errorf("missing or duplicate line identity")
		}
		byID[id] = m
	}
	return byID, nil
}

func verifyRouteApplyLines(before *boardSnapshot, result map[string]any, c pcbRouteCandidate, prefix int) ([]string, error) {
	tracks, arcs, err := parseNetPathLines(result)
	if err != nil {
		return nil, err
	}
	if len(arcs) != len(before.Copper.Arcs) {
		return nil, fmt.Errorf("arc inventory changed")
	}
	base, err := routeApplyLineMaps(before.Copper.Lines)
	if err != nil {
		return nil, err
	}
	raw, ok := result["lines"].([]any)
	if !ok {
		return nil, fmt.Errorf("line inventory missing")
	}
	after, err := routeApplyLineMaps(raw)
	if err != nil {
		return nil, err
	}
	for id, original := range base {
		if canonicalJSON(original) != canonicalJSON(after[id]) {
			return nil, fmt.Errorf("existing track %s changed or disappeared", id)
		}
	}
	var ids []string
	for _, t := range tracks {
		if base[t.ID] != nil {
			continue
		}
		owned := false
		for i := 0; i < prefix; i++ {
			owned = owned || trackIsSubsetOfSegment(t, c.Net, c.Layer, c.WidthMil, c.Points[i], c.Points[i+1])
		}
		if !owned {
			return nil, fmt.Errorf("unexpected track %s outside the attempted route", t.ID)
		}
		ids = append(ids, t.ID)
	}
	for _, a := range tracks {
		if base[a.ID] != nil {
			continue
		}
		for _, b := range tracks {
			if a.ID == b.ID || base[b.ID] == nil && a.ID > b.ID {
				continue
			}
			if routeApplyCollinearOverlap(a, b) > netPathGeomEps {
				return nil, fmt.Errorf("new duplicate copper coverage from tracks %s and %s", a.ID, b.ID)
			}
		}
	}
	for i := 0; i < prefix; i++ {
		if _, covered := matchingTrackCoverage(tracks, c.Net, c.Layer, c.WidthMil, c.Points[i], c.Points[i+1]); !covered {
			return nil, fmt.Errorf("segment %d has no exact continuous fresh copper coverage", i)
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func routeApplyCollinearOverlap(a, b pcbTrack) float64 {
	if a.Net != b.Net || a.Layer != b.Layer {
		return 0
	}
	dx, dy := a.X2-a.X1, a.Y2-a.Y1
	length := math.Hypot(dx, dy)
	if length <= netPathGeomEps {
		return 0
	}
	ux, uy := dx/length, dy/length
	if math.Abs((b.X1-a.X1)*uy-(b.Y1-a.Y1)*ux) > netPathGeomEps || math.Abs((b.X2-a.X1)*uy-(b.Y2-a.Y1)*ux) > netPathGeomEps {
		return 0
	}
	t0 := (b.X1-a.X1)*ux + (b.Y1-a.Y1)*uy
	t1 := (b.X2-a.X1)*ux + (b.Y2-a.Y1)*uy
	return math.Max(0, math.Min(length, math.Max(t0, t1))-math.Max(0, math.Min(t0, t1)))
}

func routeApplyNoExistingSplit(s *boardSnapshot, c pcbRouteCandidate) error {
	tracks, _, _, err := crystalSnapshotRouting(s)
	if err != nil {
		return err
	}
	for i := 0; i+1 < len(c.Points); i++ {
		a, b := c.Points[i], c.Points[i+1]
		if _, covered := matchingTrackCoverage(tracks, c.Net, c.Layer, c.WidthMil, a, b); covered {
			continue
		}
		for _, t := range tracks {
			if t.Layer != c.Layer || segSegDist(a[0], a[1], b[0], b[1], t.X1, t.Y1, t.X2, t.Y2) > netPathGeomEps {
				continue
			}
			// Only a shared endpoint is permitted; overlapping/crossing old copper is not owned.
			shared := false
			for _, p := range [][2]float64{a, b} {
				for _, q := range [][2]float64{{t.X1, t.Y1}, {t.X2, t.Y2}} {
					shared = shared || routeApplyPointsNear(p, q)
				}
			}
			if !shared || trackIsSubsetOfSegment(t, c.Net, c.Layer, c.WidthMil, a, b) || routeApplyPointOnSegmentInterior(a, b, [2]float64{t.X1, t.Y1}) || routeApplyPointOnSegmentInterior(a, b, [2]float64{t.X2, t.Y2}) || routeApplyPointOnSegmentInterior([2]float64{t.X1, t.Y1}, [2]float64{t.X2, t.Y2}, a) || routeApplyPointOnSegmentInterior([2]float64{t.X1, t.Y1}, [2]float64{t.X2, t.Y2}, b) {
				return fmt.Errorf("segment %d would split or overlap existing track %s", i, t.ID)
			}
		}
	}
	return nil
}

func routeApplyPointsNear(a, b [2]float64) bool {
	return math.Hypot(a[0]-b[0], a[1]-b[1]) <= netPathGeomEps
}

func routeApplyPointOnSegmentInterior(a, b, p [2]float64) bool {
	return segPointDist(a[0], a[1], b[0], b[1], p[0], p[1]) <= netPathGeomEps && !routeApplyPointsNear(a, p) && !routeApplyPointsNear(b, p)
}

func executePCBRouteApply(ctx context.Context, in pcbRouteRequest, before *boardSnapshot, receipt *pcbRouteApplyReceipt, call pcbRouteApplyCall, snapshot func() (*boardSnapshot, error), persist func() error) error {
	c := receipt.Candidate
	stop := func(status string, err error) error {
		receipt.Status, receipt.Error = status, err.Error()
		receipt.Partial = len(receipt.Steps) > 0
		if saveErr := persist(); saveErr != nil {
			return fmt.Errorf("%w; receipt: %v", err, saveErr)
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
		return stop("not_executed", fmt.Errorf("fresh board differs from the candidate baseline: %v", err))
	}
	if _, _, _, err := preparePCBRouteRequest(in, fresh); err != nil {
		return stop("not_executed", err)
	}
	if err := routeApplyNoExistingSplit(fresh, c); err != nil {
		return stop("not_executed", err)
	}
	if receipt.DryRun {
		receipt.Status = "ready"
		return persist()
	}
	for i := 0; i+1 < len(c.Points); i++ {
		if err := ctx.Err(); err != nil {
			return stop("partial", err)
		}
		latest, err := call("pcb.line.list", nil)
		if err != nil {
			status := "not_executed"
			if receipt.Applied {
				status = "unknown"
			}
			return stop(status, err)
		}
		if _, err := verifyRouteApplyLines(before, latest.Result, c, i); err != nil {
			status := "not_executed"
			if receipt.Applied {
				status = "partial"
			}
			return stop(status, err)
		}
		currentTracks, _, err := parseNetPathLines(latest.Result)
		if err != nil {
			return stop("unknown", err)
		}
		if _, covered := matchingTrackCoverage(currentTracks, c.Net, c.Layer, c.WidthMil, c.Points[i], c.Points[i+1]); covered {
			receipt.Steps = append(receipt.Steps, pcbRouteApplyStep{Index: i, Status: "already_present"})
			receipt.Remaining = receipt.Remaining[1:]
			continue
		}
		step := pcbRouteApplyStep{Index: i, Status: "unknown"}
		receipt.Steps = append(receipt.Steps, step)
		receipt.Remaining = receipt.Remaining[1:]
		receipt.Status, receipt.Partial = "in_progress", true
		if err := persist(); err != nil {
			receipt.Steps[len(receipt.Steps)-1].Status = "not_executed"
			receipt.Remaining = append([]int{i}, receipt.Remaining...)
			receipt.Status = "not_executed"
			if receipt.Applied {
				receipt.Status = "partial"
			}
			receipt.Error = "receipt persistence failed before dispatch: " + err.Error()
			return err
		}
		if err := ctx.Err(); err != nil {
			receipt.Steps[len(receipt.Steps)-1].Status = "not_executed"
			receipt.Remaining = append([]int{i}, receipt.Remaining...)
			receipt.Status, receipt.Partial = "not_executed", false
			if receipt.Applied {
				receipt.Status, receipt.Partial = "partial", true
			}
			receipt.Error = err.Error()
			if saveErr := persist(); saveErr != nil {
				return fmt.Errorf("%w; receipt: %v", err, saveErr)
			}
			return err
		}
		p, q := c.Points[i], c.Points[i+1]
		res, err := call("pcb.line.create", map[string]any{"net": c.Net, "layer": c.Layer, "lineWidth": c.WidthMil, "startX": p[0], "startY": p[1], "endX": q[0], "endY": q[1]})
		current := &receipt.Steps[len(receipt.Steps)-1]
		if res != nil {
			current.RequestID = res.ID
			current.PrimitiveID = asString(res.Result["primitiveId"])
		}
		if err != nil {
			current.Error = err.Error()
			return stop("unknown", err)
		}
		if current.PrimitiveID == "" {
			return stop("unknown", fmt.Errorf("line create returned no primitive identity"))
		}
		lines, err := call("pcb.line.list", nil)
		if err != nil {
			return stop("unknown", err)
		}
		if _, _, err := parseNetPathLines(lines.Result); err != nil {
			return stop("unknown", err)
		}
		ids, err := verifyRouteApplyLines(before, lines.Result, c, i+1)
		if err != nil {
			current.Error = err.Error()
			return stop("partial", err)
		}
		current.Status, current.ObservedIDs = "applied", ids
		receipt.Applied = true
		if err := persist(); err != nil {
			receipt.Status, receipt.Error = "unknown", "receipt persistence failed after write: "+err.Error()
			return err
		}
	}
	after, err := snapshot()
	if err != nil {
		return stop("unknown", err)
	}
	bf, err := routeApplyFacts(before, true)
	if err != nil {
		return stop("partial", err)
	}
	af, err := routeApplyFacts(after, true)
	if err != nil || bf != af {
		return stop("partial", fmt.Errorf("non-route board facts changed: %v", err))
	}
	if _, err := verifyRouteApplyLines(before, map[string]any{"lines": after.Copper.Lines, "arcs": after.Copper.Arcs, "arcsAvailable": after.Copper.ArcsAvailable != nil && *after.Copper.ArcsAvailable}, c, len(c.Points)-1); err != nil {
		return stop("partial", err)
	}
	receipt.GeometryVerified = true
	pads, err := crystalCandidatePads(after, nil)
	if err != nil {
		return stop("partial", err)
	}
	tracks, arcs, vias, err := crystalSnapshotRouting(after)
	if err != nil {
		return stop("partial", err)
	}
	path, pathErr := analyzePcbNetPath(pads, tracks, arcs, vias, pcbNetPathOptions{From: in.From, To: in.To, Net: in.Net, Layer: &in.Layer})
	receipt.Connectivity = path
	if pathErr != nil || !path.Connected {
		return stop("partial", fmt.Errorf("fresh pad connectivity is unverified: %v", pathErr))
	}
	receipt.Status, receipt.Partial = "applied", false
	if !receipt.Applied {
		receipt.Status = "already_present"
	}
	drc, drcErr := call("pcb.drc.check", map[string]any{"strict": true})
	if drcErr != nil {
		receipt.DRC = map[string]any{"status": "unknown", "error": drcErr.Error()}
	} else if passed, ok := drc.Result["passed"].(bool); !ok {
		receipt.DRC = map[string]any{"status": "unknown", "result": drc.Result}
	} else {
		status := "fail"
		if passed {
			status = "pass"
		}
		receipt.DRC = map[string]any{"status": status, "result": drc.Result, "requestId": drc.ID}
		receipt.Verified = passed
	}
	return persist()
}

func marshalPCBRouteApplyReceipt(r *pcbRouteApplyReceipt) ([]byte, error) {
	b, err := json.MarshalIndent(r, "", "  ")
	return append(b, '\n'), err
}

// Package pcbsolve coordinates bounded PCB placement and routing. It consumes
// only pcbmodel data and never performs file, network or editor I/O.
package pcbsolve

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

const (
	Pass       = "pass"
	Incomplete = "incomplete"
	Fail       = "fail"
)

type Demand struct {
	ID      string  `json:"id"`
	Net     string  `json:"net"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Width   float64 `json:"width"`
	Layers  []int   `json:"layers"`
	MaxVias int     `json:"maxVias"`
}

type RoutingRequest struct {
	Demands   []Demand `json:"demands"`
	Step      float64  `json:"step"`
	MaxDetour float64  `json:"maxDetour"`
	MaxStates int      `json:"maxStates"`
}

type Request struct {
	SchemaVersion int               `json:"schemaVersion"`
	Units         string            `json:"units"`
	Layout        pcblayout.Request `json:"layout"`
	Routing       RoutingRequest    `json:"routing"`
	MaxStates     int               `json:"maxStates"`
	MaxCandidates int               `json:"maxCandidates"`
}

type Metrics struct {
	MovedGroups   int     `json:"movedGroups"`
	TotalDistance float64 `json:"totalDistance"`
	Rotations     int     `json:"rotations"`
	NewVias       int     `json:"newVias"`
	RouteLength   float64 `json:"routeLength"`
	Bends         int     `json:"bends"`
}

type Candidate struct {
	ID               string                      `json:"id"`
	BaseSemanticHash string                      `json:"baseSemanticHash,omitempty"`
	RequestHash      string                      `json:"requestHash"`
	Board            pcbmodel.Board              `json:"board"`
	Moves            []pcblayout.Move            `json:"moves,omitempty"`
	Routes           []pcbrouting.JointCandidate `json:"routes"`
	Reservations     []Reservation               `json:"reservations,omitempty"`
	Metrics          Metrics                     `json:"metrics"`
	MoveReasons      []string                    `json:"moveReasons,omitempty"`
}

type Attempt struct {
	LayoutID      string                      `json:"layoutId"`
	Status        string                      `json:"status"`
	Reason        string                      `json:"reason,omitempty"`
	Blockers      []string                    `json:"blockers,omitempty"`
	Conflicts     []Conflict                  `json:"conflicts,omitempty"`
	PartialRoutes []pcbrouting.JointCandidate `json:"partialRoutes,omitempty"`
	States        int                         `json:"states"`
}

type Report struct {
	SchemaVersion  int              `json:"schemaVersion"`
	Status         string           `json:"status"`
	Reason         string           `json:"reason,omitempty"`
	RequestHash    string           `json:"requestHash"`
	Layout         pcblayout.Report `json:"layout"`
	Attempts       []Attempt        `json:"attempts"`
	Candidates     []Candidate      `json:"candidates,omitempty"`
	States         int              `json:"states"`
	Budget         int              `json:"budget"`
	Exhausted      bool             `json:"exhausted,omitempty"`
	SearchComplete bool             `json:"searchComplete"`
}

func (r Request) Validate(board pcbmodel.Board) error {
	if r.SchemaVersion != 1 || r.Units != "mil" || r.MaxCandidates < 1 || r.MaxCandidates > 3 || r.MaxStates < 1 || r.MaxStates > pcbrouting.MaxSearchStates {
		return fmt.Errorf("solve requires schemaVersion=1, units=mil, 1..3 output candidates and a bounded total maxStates")
	}
	if err := r.Layout.Validate(board); err != nil {
		return err
	}
	if r.Routing.Step <= 0 || r.Routing.MaxDetour < 0 || r.Routing.MaxStates < 1 || r.Routing.MaxStates > pcbrouting.MaxSearchStates {
		return fmt.Errorf("routing requires step > 0, maxDetour >= 0 and a bounded maxStates")
	}
	if len(r.Routing.Demands) == 0 {
		return fmt.Errorf("routing requires at least one demand")
	}
	ids := map[string]bool{}
	for _, d := range r.Routing.Demands {
		if d.ID == "" || d.Net == "" || ids[d.ID] || d.Width < board.Rules.TrackWidthMinimum || d.MaxVias < 0 || d.MaxVias > 2 {
			return fmt.Errorf("invalid or duplicate routing demand %q", d.ID)
		}
		ids[d.ID] = true
		from, _, okFrom := board.Pad(d.From)
		to, _, okTo := board.Pad(d.To)
		if !okFrom || !okTo || from.Net != d.Net || to.Net != d.Net {
			return fmt.Errorf("demand %s endpoints are missing, ambiguous or on another net", d.ID)
		}
		if len(d.Layers) == 0 {
			return fmt.Errorf("demand %s requires explicit allowed layers", d.ID)
		}
		seen := map[int]bool{}
		for _, layer := range d.Layers {
			l, ok := board.Stackup.Layer(layer)
			if !ok || l.Role != "signal" || seen[layer] {
				return fmt.Errorf("demand %s uses missing, plane or duplicate layer %d", d.ID, layer)
			}
			seen[layer] = true
		}
	}
	return validateExternalCoverage(board, r)
}

func Solve(ctx context.Context, board pcbmodel.Board, req Request) (Report, error) {
	rep := Report{SchemaVersion: 1, Status: Incomplete, RequestHash: hashRequest(req), Budget: req.MaxStates}
	if err := req.Validate(board); err != nil {
		rep.Status, rep.Reason = Fail, err.Error()
		return rep, err
	}
	// The coordinator needs enough mechanically legal alternatives for routing
	// feedback even though only three final candidates are emitted.
	layoutReq := req.Layout
	if layoutReq.MaxStates > req.MaxStates {
		layoutReq.MaxStates = req.MaxStates
	}
	if layoutReq.MaxCandidates < 32 {
		layoutReq.MaxCandidates = 32
	}
	if layoutReq.MaxCandidates > 100 {
		layoutReq.MaxCandidates = 100
	}
	layout, err := pcblayout.Generate(board, layoutReq)
	rep.Layout = layout
	rep.States = layout.States
	rep.Exhausted = layout.Exhausted
	if err != nil {
		rep.Status, rep.Reason = Fail, err.Error()
		return rep, err
	}
	pending := append([]pcblayout.Candidate(nil), layout.Candidates...)
	visited := map[string]bool{}
	for _, c := range pending {
		visited[pcblayout.StateKey(c)] = true
	}
	nextID := len(pending)
	expand := func(current pcblayout.Candidate, refs []string, reason string) {
		for _, c := range pcblayout.Neighbors(board, req.Layout, current, refs, reason) {
			key := pcblayout.StateKey(c)
			if visited[key] {
				continue
			}
			if rep.Layout.States >= req.Layout.MaxStates || rep.States >= req.MaxStates {
				rep.Layout.Exhausted, rep.Exhausted = true, true
				return
			}
			visited[key] = true
			rep.Layout.States++
			rep.States++
			nextID++
			c.ID = fmt.Sprintf("layout-%02d", nextID)
			pending = append(pending, c)
		}
	}
	feedback := map[string]bool{}
	for len(pending) > 0 && len(rep.Candidates) < req.MaxCandidates {
		remaining := req.MaxStates - rep.States
		if remaining <= 0 {
			rep.Exhausted = true
			break
		}
		if len(feedback) > 0 {
			sort.SliceStable(pending, func(i, j int) bool {
				a, b := movesFeedbackRefs(pending[i].Moves, feedback), movesFeedbackRefs(pending[j].Moves, feedback)
				if a != b {
					return a > b
				}
				return pcblayout.Less(pending[i], pending[j])
			})
		}
		layoutCandidate := pending[0]
		pending = pending[1:]
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		if findings := pcblayout.ChangedFindings(board, layoutCandidate.Board, req.Layout); len(findings) > 0 {
			rep.Layout.Rejected = append(rep.Layout.Rejected, findings...)
			expand(layoutCandidate, mechanicalRefs(findings), "make room for a displaced complete group")
			continue
		}
		known := false
		for _, c := range rep.Layout.Candidates {
			known = known || c.ID == layoutCandidate.ID
		}
		if !known {
			rep.Layout.Candidates = append(rep.Layout.Candidates, layoutCandidate)
		}
		routeReq := req.Routing
		if routeReq.MaxStates > remaining {
			routeReq.MaxStates = remaining
		}
		routed, conflicts, routeErr := solveRoutes(ctx, layoutCandidate.Board, routeReq)
		rep.States += routed.States
		rep.Exhausted = rep.Exhausted || routed.Exhausted
		if routed.Exhausted {
			conflicts = nil
		}
		blockers := conflictRefs(conflicts, req)
		attempt := Attempt{LayoutID: layoutCandidate.ID, Status: routed.Status, Reason: routed.Reason, States: routed.States, Blockers: blockers, Conflicts: conflicts, PartialRoutes: routed.Partial}
		rep.Attempts = append(rep.Attempts, attempt)
		if routeErr != nil {
			if ctx.Err() != nil {
				return rep, routeErr
			}
			continue
		}
		if routed.Status != pcbrouting.Found {
			if !routed.Exhausted {
				for _, ref := range blockers {
					feedback[ref] = true
				}
				expand(layoutCandidate, blockers, "route feedback: "+routed.Reason)
			}
			rep.Exhausted = rep.Exhausted || routed.Exhausted
			continue
		}
		candidate := Candidate{BaseSemanticHash: board.SemanticHash, RequestHash: rep.RequestHash, Board: layoutCandidate.Board, Moves: layoutCandidate.Moves, Routes: routed.Selected}
		candidate.Reservations = reservations(layoutCandidate.Board, routed.Selected)
		candidate.Metrics = candidateMetrics(layoutCandidate, routed.Selected)
		for _, move := range candidate.Moves {
			if move.Reason != "" {
				candidate.MoveReasons = append(candidate.MoveReasons, move.Group+": "+move.Reason)
			} else if feedback[move.Group] || moveTouchesRefs(move, feedback) {
				candidate.MoveReasons = append(candidate.MoveReasons, fmt.Sprintf("%s moved after route feedback", move.Group))
			}
		}
		if err := Check(ctx, board, req, candidate); err != nil {
			rep.Attempts[len(rep.Attempts)-1].Status = Fail
			rep.Attempts[len(rep.Attempts)-1].Reason = "independent check: " + err.Error()
			continue
		}
		rep.Candidates = append(rep.Candidates, candidate)
	}
	sort.SliceStable(rep.Candidates, func(i, j int) bool { return candidateLess(rep.Candidates[i], rep.Candidates[j]) })
	for i := range rep.Candidates {
		rep.Candidates[i].ID = fmt.Sprintf("candidate-%02d", i+1)
	}
	if len(rep.Candidates) > 0 {
		rep.Status, rep.Reason = Pass, ""
	} else if rep.Exhausted || rep.Layout.Exhausted {
		rep.Reason = "bounded layout/routing search exhausted; no global impossibility claim"
	} else {
		rep.Reason = "no jointly routable placement in the declared finite search"
	}
	rep.SearchComplete = !rep.Exhausted && !rep.Layout.Exhausted && !rep.Layout.Truncated && len(pending) == 0
	return rep, nil
}

func Check(ctx context.Context, board pcbmodel.Board, req Request, candidate Candidate) error {
	if err := req.Validate(board); err != nil {
		return err
	}
	if candidate.BaseSemanticHash != board.SemanticHash || candidate.RequestHash != hashRequest(req) {
		return fmt.Errorf("candidate provenance does not match the independent board/request")
	}
	projected, err := pcblayout.Project(board, req.Layout, candidate.Moves)
	if err != nil {
		return err
	}
	if !samePlacement(projected, candidate.Board) {
		return fmt.Errorf("candidate board is not the projection of its declared moves")
	}
	if findings := pcblayout.ChangedFindings(board, projected, req.Layout); len(findings) > 0 {
		return fmt.Errorf("candidate has affected layout finding %s", layoutFindingKey(findings[0]))
	}
	if err := checkExistingPadCopper(board, projected); err != nil {
		return err
	}
	jointReq, demandMap := jointRequest(req.Routing)
	checker := routeChecker(projected, req.Routing, demandMap)
	if err := pcbrouting.CheckJoint(ctx, jointReq, candidate.Routes, checker); err != nil {
		return err
	}
	// Old candidates without this optional derived view remain readable.
	if candidate.Reservations != nil && !reflect.DeepEqual(candidate.Reservations, reservations(projected, candidate.Routes)) {
		return fmt.Errorf("candidate reservations differ from independently checked routes/rules")
	}
	layoutMetrics := pcblayout.Metrics{}
	for _, m := range candidate.Moves {
		if m.DX == 0 && m.DY == 0 && m.RotationDelta == 0 {
			continue
		}
		layoutMetrics.MovedGroups++
		layoutMetrics.TotalDistance += math.Hypot(m.DX, m.DY)
		if m.RotationDelta != 0 {
			layoutMetrics.Rotations++
		}
	}
	expected := candidateMetrics(pcblayout.Candidate{Metrics: layoutMetrics}, candidate.Routes)
	if candidate.Metrics != expected {
		return fmt.Errorf("candidate metrics differ from measured moves/routes")
	}
	return nil
}

func solveRoutes(ctx context.Context, board pcbmodel.Board, req RoutingRequest) (pcbrouting.JointResult, []Conflict, error) {
	jointReq, demandMap := jointRequest(req)
	observed := map[string]Conflict{}
	observe := func(c Conflict) {
		key := fmt.Sprintf("%s:%d:%s:%s", c.DemandID, c.Layer, c.Kind, c.Object)
		if _, exists := observed[key]; !exists && len(observed) < 256 {
			observed[key] = c
		}
	}
	factory := func(ctx context.Context, generic pcbrouting.Demand, selected []pcbrouting.JointCandidate, remaining int) ([]pcbrouting.JointCandidate, int, string, error) {
		d := demandMap[generic.ID]
		return routeCandidates(ctx, board, req, d, selected, remaining, observe)
	}
	result, err := pcbrouting.SolveJoint(ctx, jointReq, factory, routeChecker(board, req, demandMap))
	keys := make([]string, 0, len(observed))
	for key := range observed {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	conflicts := make([]Conflict, 0, len(keys))
	for _, key := range keys {
		conflicts = append(conflicts, observed[key])
	}
	return result, conflicts, err
}

func jointRequest(req RoutingRequest) (pcbrouting.JointRequest, map[string]Demand) {
	joint := pcbrouting.JointRequest{MaxStates: req.MaxStates}
	byID := map[string]Demand{}
	for _, d := range req.Demands {
		joint.Demands = append(joint.Demands, pcbrouting.Demand{ID: d.ID, Net: d.Net})
		byID[d.ID] = d
	}
	return joint, byID
}

func candidateMetrics(layout pcblayout.Candidate, routes []pcbrouting.JointCandidate) Metrics {
	m := Metrics{MovedGroups: layout.Metrics.MovedGroups, TotalDistance: layout.Metrics.TotalDistance, Rotations: layout.Metrics.Rotations}
	for _, candidate := range routes {
		m.NewVias += len(candidate.Vias)
		for _, route := range candidate.Routes {
			length, bends := pcbrouting.Metrics(route.Points)
			m.RouteLength += length
			m.Bends += bends
		}
	}
	return m
}

func candidateLess(a, b Candidate) bool {
	if a.Metrics.MovedGroups != b.Metrics.MovedGroups {
		return a.Metrics.MovedGroups < b.Metrics.MovedGroups
	}
	if a.Metrics.TotalDistance != b.Metrics.TotalDistance {
		return a.Metrics.TotalDistance < b.Metrics.TotalDistance
	}
	if a.Metrics.Rotations != b.Metrics.Rotations {
		return a.Metrics.Rotations < b.Metrics.Rotations
	}
	if a.Metrics.NewVias != b.Metrics.NewVias {
		return a.Metrics.NewVias < b.Metrics.NewVias
	}
	if a.Metrics.RouteLength != b.Metrics.RouteLength {
		return a.Metrics.RouteLength < b.Metrics.RouteLength
	}
	if a.Metrics.Bends != b.Metrics.Bends {
		return a.Metrics.Bends < b.Metrics.Bends
	}
	return a.ID < b.ID
}

func hashRequest(req Request) string {
	raw, _ := json.Marshal(req)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func movesFeedbackRefs(moves []pcblayout.Move, feedback map[string]bool) int {
	n := 0
	for _, move := range moves {
		if feedback[move.Group] || moveTouchesRefs(move, feedback) {
			n++
		}
	}
	return n
}

func moveTouchesRefs(move pcblayout.Move, feedback map[string]bool) bool {
	for _, ref := range move.Refs {
		if feedback[ref] {
			return true
		}
	}
	return false
}

func samePlacement(a, b pcbmodel.Board) bool {
	return reflect.DeepEqual(a, b)
}

func layoutFindingKey(f pcblayout.Finding) string { return f.Type + ":" + strings.Join(f.Objects, ":") }
func pointDistance(a, b pcbmodel.Point) float64   { return math.Hypot(a[0]-b[0], a[1]-b[1]) }

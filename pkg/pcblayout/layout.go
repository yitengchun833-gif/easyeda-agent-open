// Package pcblayout provides bounded, deterministic PCB placement search over
// the editor-independent pcbmodel representation. It does not read files or
// call an EDA host.
package pcblayout

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

const (
	Candidates = "candidates"
	Incomplete = "incomplete"
	Invalid    = "invalid"
)

type Offset struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

type Group struct {
	ID                    string             `json:"id"`
	Refs                  []string           `json:"refs"`
	AnchorRef             string             `json:"anchorRef"`
	FixedAxes             []string           `json:"fixedAxes,omitempty"`
	AllowedRotationDeltas []float64          `json:"allowedRotationDeltas,omitempty"`
	Offsets               []Offset           `json:"offsets"`
	TranslationSearch     *TranslationSearch `json:"translationSearch,omitempty"`
	InternalTrackIDs      []string           `json:"internalTrackIds,omitempty"`
	InternalViaIDs        []string           `json:"internalViaIds,omitempty"`
	InternalRegionIDs     []string           `json:"internalRegionIds,omitempty"`
}

type Keepout struct {
	ID     string        `json:"id"`
	BBox   pcbmodel.BBox `json:"bbox"`
	Layers []int         `json:"layers,omitempty"`
}

type Request struct {
	Groups        []Group   `json:"groups"`
	FixedRefs     []string  `json:"fixedRefs,omitempty"`
	Keepouts      []Keepout `json:"keepouts,omitempty"`
	MinGap        float64   `json:"minGap"`
	MaxStates     int       `json:"maxStates"`
	MaxCandidates int       `json:"maxCandidates"`
}

type Move struct {
	Group         string         `json:"group"`
	Refs          []string       `json:"refs"`
	Reason        string         `json:"reason,omitempty"`
	Pivot         pcbmodel.Point `json:"pivot"`
	DX            float64        `json:"dx"`
	DY            float64        `json:"dy"`
	RotationDelta float64        `json:"rotationDelta"`
}

type Metrics struct {
	MovedGroups   int     `json:"movedGroups"`
	TotalDistance float64 `json:"totalDistance"`
	Rotations     int     `json:"rotations"`
}

type Candidate struct {
	ID      string         `json:"id"`
	Board   pcbmodel.Board `json:"board"`
	Moves   []Move         `json:"moves,omitempty"`
	Metrics Metrics        `json:"metrics"`
}

type Finding struct {
	Type    string   `json:"type"`
	Objects []string `json:"objects"`
	Detail  string   `json:"detail"`
}

type Report struct {
	Status     string      `json:"status"`
	States     int         `json:"states"`
	Budget     int         `json:"budget"`
	Exhausted  bool        `json:"exhausted"`
	Truncated  bool        `json:"truncated,omitempty"`
	Candidates []Candidate `json:"candidates,omitempty"`
	Rejected   []Finding   `json:"rejected,omitempty"`
}

func (r Request) Validate(board pcbmodel.Board) error {
	if err := board.Validate(true); err != nil {
		return err
	}
	if r.MinGap < 0 || r.MaxStates < 1 || r.MaxStates > 2000000 || r.MaxCandidates < 1 || r.MaxCandidates > 100 {
		return fmt.Errorf("layout requires minGap >= 0, 1 <= maxStates <= 2000000 and 1 <= maxCandidates <= 100")
	}
	refs := map[string]bool{}
	for _, c := range board.Components {
		refs[c.Ref] = true
	}
	fixed := stringSet(r.FixedRefs)
	owner := map[string]string{}
	groupIDs := map[string]bool{}
	for _, g := range r.Groups {
		if strings.TrimSpace(g.ID) == "" || groupIDs[g.ID] || strings.TrimSpace(g.AnchorRef) == "" || len(g.Refs) == 0 || !refs[g.AnchorRef] {
			return fmt.Errorf("layout group requires id, members and a measured anchor")
		}
		groupIDs[g.ID] = true
		if len(g.Offsets) == 0 && g.TranslationSearch == nil {
			return fmt.Errorf("layout group %s requires explicit offsets or translationSearch", g.ID)
		}
		if s := g.TranslationSearch; s != nil && (!finite(s.Step) || !finite(s.MaxDistance) || s.Step <= 0 || s.MaxDistance < s.Step) {
			return fmt.Errorf("layout group %s requires finite translationSearch step > 0 and maxDistance >= step", g.ID)
		}
		for _, o := range g.Offsets {
			if !finite(o.X) || !finite(o.Y) {
				return fmt.Errorf("layout group %s has non-finite offset", g.ID)
			}
		}
		member := false
		for _, ref := range g.Refs {
			if !refs[ref] || owner[ref] != "" {
				return fmt.Errorf("layout group %s has missing/duplicate member %s", g.ID, ref)
			}
			owner[ref], member = g.ID, member || ref == g.AnchorRef
			if fixed[ref] {
				return fmt.Errorf("layout group %s contains fixed member %s", g.ID, ref)
			}
		}
		if !member {
			return fmt.Errorf("layout group %s anchor is not a member", g.ID)
		}
		for _, axis := range g.FixedAxes {
			if axis != "x" && axis != "y" && axis != "rotation" {
				return fmt.Errorf("layout group %s has invalid fixed axis %s", g.ID, axis)
			}
		}
		for _, d := range rotations(g) {
			if !quarterTurn(d) {
				return fmt.Errorf("layout group %s rotation %.3f is not a quarter turn", g.ID, d)
			}
		}
	}
	return nil
}

// Generate enumerates complete, mechanically legal board candidates. It keeps
// the current placement as the first state and orders changed states by minimum
// movement, making it suitable for route-feedback iteration.
func Generate(board pcbmodel.Board, req Request) (Report, error) {
	rep := Report{Status: Invalid, Budget: req.MaxStates}
	if err := req.Validate(board); err != nil {
		return rep, err
	}
	groups := append([]Group(nil), req.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	var candidates []Candidate
	visited := map[string]bool{}
	var search func(int, pcbmodel.Board, []Move)
	search = func(index int, current pcbmodel.Board, moves []Move) {
		if rep.States >= req.MaxStates {
			rep.Exhausted = true
			return
		}
		if len(candidates) >= req.MaxCandidates*8 {
			rep.Truncated = true
			return
		}
		rep.States++
		if index == len(groups) {
			if findings := ChangedFindings(board, current, req); len(findings) > 0 {
				rep.Rejected = append(rep.Rejected, findings...)
				return
			}
			sig := candidateSignature(current, moves)
			if visited[sig] {
				return
			}
			visited[sig] = true
			candidates = append(candidates, Candidate{Board: current, Moves: append([]Move(nil), moves...), Metrics: moveMetrics(moves)})
			return
		}
		g := groups[index]
		anchor, _ := current.Component(g.AnchorRef)
		transforms := groupTransforms(g, anchor)
		for _, move := range transforms {
			next, changed, err := ApplyGroup(current, g, move)
			if err != nil {
				rep.Rejected = append(rep.Rejected, Finding{Type: "transform", Objects: []string{g.ID}, Detail: err.Error()})
				continue
			}
			nextMoves := moves
			if changed {
				nextMoves = append(append([]Move(nil), moves...), move)
			}
			search(index+1, next, nextMoves)
			if rep.Exhausted || rep.Truncated {
				return
			}
		}
	}
	search(0, board, nil)
	sort.SliceStable(candidates, func(i, j int) bool { return Less(candidates[i], candidates[j]) })
	if len(candidates) > req.MaxCandidates {
		rep.Truncated = true
		candidates = candidates[:req.MaxCandidates]
	}
	for i := range candidates {
		candidates[i].ID = fmt.Sprintf("layout-%02d", i+1)
	}
	rep.Candidates = candidates
	rep.Status = Candidates
	if len(candidates) == 0 || rep.Exhausted || rep.Truncated {
		rep.Status = Incomplete
	}
	return rep, nil
}

// ApplyGroup applies one rigid transform to every member and every explicitly
// owned internal track/via. Assembly sides never change.
func ApplyGroup(board pcbmodel.Board, group Group, move Move) (pcbmodel.Board, bool, error) {
	if move.Group != group.ID {
		return board, false, fmt.Errorf("move group %s does not match %s", move.Group, group.ID)
	}
	if contains(group.FixedAxes, "x") && math.Abs(move.DX) > 1e-7 || contains(group.FixedAxes, "y") && math.Abs(move.DY) > 1e-7 || contains(group.FixedAxes, "rotation") && math.Abs(move.RotationDelta) > 1e-7 {
		return board, false, fmt.Errorf("move violates fixed axes")
	}
	member := stringSet(group.Refs)
	trackIDs, viaIDs, regionIDs := stringSet(group.InternalTrackIDs), stringSet(group.InternalViaIDs), stringSet(group.InternalRegionIDs)
	tr := pcbmodel.Transform{Pivot: move.Pivot, DX: move.DX, DY: move.DY, RotationDelta: move.RotationDelta}
	out := board
	out.Components = append([]pcbmodel.Component(nil), board.Components...)
	found := map[string]bool{}
	changed := math.Abs(move.DX) > 1e-7 || math.Abs(move.DY) > 1e-7 || math.Abs(move.RotationDelta) > 1e-7
	for i, c := range out.Components {
		if !member[c.Ref] {
			continue
		}
		if c.Locked && changed {
			return board, false, fmt.Errorf("member %s is locked", c.Ref)
		}
		found[c.Ref] = true
		out.Components[i] = tr.Component(c)
		out.Components[i].Side = c.Side
	}
	for ref := range member {
		if !found[ref] {
			return board, false, fmt.Errorf("member %s is missing", ref)
		}
	}
	out.Tracks = append([]pcbmodel.Track(nil), board.Tracks...)
	for i, t := range out.Tracks {
		if trackIDs[t.ID] {
			out.Tracks[i] = tr.Track(t)
		}
	}
	out.Vias = append([]pcbmodel.Via(nil), board.Vias...)
	for i, v := range out.Vias {
		if viaIDs[v.ID] {
			out.Vias[i] = tr.Via(v)
		}
	}
	out.Regions = append([]pcbmodel.Region(nil), board.Regions...)
	for i, region := range out.Regions {
		if regionIDs[region.ID] {
			out.Regions[i] = tr.Region(region)
		}
	}
	return out, changed, nil
}

// Check returns mechanical facts only. Electrical and route facts belong to
// pcbrouting/pcbsolve and are deliberately not inferred from placement scores.
func Check(board pcbmodel.Board, req Request) []Finding {
	var out []Finding
	for _, c := range board.Components {
		if !board.Outline.ContainsBBox(c.BBox) {
			out = append(out, Finding{Type: "off-board", Objects: []string{c.Ref}, Detail: "component bbox crosses the exact board outline"})
		}
		for _, k := range req.Keepouts {
			if layerApplies(k.Layers, c.Side) && (c.BBox.Overlaps(k.BBox) || c.BBox.Gap(k.BBox) < req.MinGap) {
				out = append(out, Finding{Type: "keepout", Objects: []string{c.Ref, k.ID}, Detail: "component intersects or violates keepout clearance"})
			}
		}
	}
	for i, a := range board.Components {
		for _, b := range board.Components[i+1:] {
			if a.Side != b.Side {
				continue
			}
			if a.BBox.Overlaps(b.BBox) {
				out = append(out, Finding{Type: "overlap", Objects: ordered(a.Ref, b.Ref), Detail: "same-side component bboxes overlap"})
			} else if a.BBox.Gap(b.BBox) < req.MinGap {
				out = append(out, Finding{Type: "spacing", Objects: ordered(a.Ref, b.Ref), Detail: "same-side component gap is below minGap"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return findingKey(out[i]) < findingKey(out[j]) })
	return out
}

func Less(a, b Candidate) bool {
	if a.Metrics.MovedGroups != b.Metrics.MovedGroups {
		return a.Metrics.MovedGroups < b.Metrics.MovedGroups
	}
	if math.Abs(a.Metrics.TotalDistance-b.Metrics.TotalDistance) > 1e-7 {
		return a.Metrics.TotalDistance < b.Metrics.TotalDistance
	}
	if a.Metrics.Rotations != b.Metrics.Rotations {
		return a.Metrics.Rotations < b.Metrics.Rotations
	}
	return candidateSignature(a.Board, a.Moves) < candidateSignature(b.Board, b.Moves)
}

func groupTransforms(g Group, anchor pcbmodel.Component) []Move {
	var out []Move
	offsets := g.Offsets
	if len(offsets) == 0 {
		offsets = []Offset{{}}
	}
	for _, d := range rotations(g) {
		for _, o := range offsets {
			out = append(out, Move{Group: g.ID, Refs: append([]string(nil), g.Refs...), Pivot: anchor.Anchor, DX: o.X, DY: o.Y, RotationDelta: d})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := moveMetrics([]Move{out[i]}), moveMetrics([]Move{out[j]})
		if a.MovedGroups != b.MovedGroups {
			return a.MovedGroups < b.MovedGroups
		}
		if a.TotalDistance != b.TotalDistance {
			return a.TotalDistance < b.TotalDistance
		}
		if a.Rotations != b.Rotations {
			return a.Rotations < b.Rotations
		}
		if out[i].DY != out[j].DY {
			return out[i].DY < out[j].DY
		}
		return out[i].DX < out[j].DX
	})
	return out
}

func rotations(g Group) []float64 {
	if len(g.AllowedRotationDeltas) == 0 {
		return []float64{0}
	}
	return g.AllowedRotationDeltas
}

func moveMetrics(moves []Move) Metrics {
	var m Metrics
	for _, move := range moves {
		if math.Abs(move.DX) < 1e-7 && math.Abs(move.DY) < 1e-7 && math.Abs(move.RotationDelta) < 1e-7 {
			continue
		}
		m.MovedGroups++
		m.TotalDistance += math.Hypot(move.DX, move.DY)
		if math.Abs(move.RotationDelta) > 1e-7 {
			m.Rotations++
		}
	}
	return m
}

func candidateSignature(board pcbmodel.Board, moves []Move) string {
	parts := make([]string, 0, len(board.Components)+len(moves))
	for _, c := range board.Components {
		parts = append(parts, fmt.Sprintf("%s:%.6f:%.6f:%.6f:%d", c.Ref, c.Anchor[0], c.Anchor[1], c.Rotation, c.Side))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}

func findingKey(f Finding) string { return f.Type + ":" + strings.Join(f.Objects, ":") }
func findingKeys(findings []Finding) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings {
		out[findingKey(f)] = true
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, v := range values {
		out[v] = true
	}
	return out
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

func layerApplies(layers []int, side int) bool {
	if len(layers) == 0 {
		return true
	}
	for _, l := range layers {
		if l == side || l == pcbmodel.LayerMulti {
			return true
		}
	}
	return false
}

func ordered(a, b string) []string {
	if a > b {
		a, b = b, a
	}
	return []string{a, b}
}

func quarterTurn(v float64) bool {
	r := math.Mod(math.Abs(v), 90)
	return r < 1e-7 || math.Abs(r-90) < 1e-7
}

package pcblayout

import (
	"fmt"
	"math"
	"reflect"
	"sort"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

// TranslationSearch bounds automatic displacement relative to the original
// footprint anchor, on a finite grid. It does not contain a target coordinate.
type TranslationSearch struct {
	Step        float64 `json:"step"`
	MaxDistance float64 `json:"maxDistance"`
}

// ChangedFindings preserves unrelated, unchanged baseline facts only. A moved
// object must satisfy the constraints even if the same pair was already bad.
func ChangedFindings(base, projected pcbmodel.Board, req Request) []Finding {
	baseline := findingKeys(Check(base, req))
	changed := map[string]bool{}
	for _, c := range projected.Components {
		before, ok := base.Component(c.Ref)
		changed[c.Ref] = !ok || !reflect.DeepEqual(before, c)
	}
	var out []Finding
	for _, f := range Check(projected, req) {
		affected := !baseline[findingKey(f)]
		for _, ref := range f.Objects {
			affected = affected || changed[ref]
		}
		if affected {
			out = append(out, f)
		}
	}
	return out
}

// Project reconstructs a candidate from independent bounds. Each group has
// exactly one absolute transform from the base board; incremental move logs
// cannot escape the original radius or omit members in a host Apply queue.
func Project(base pcbmodel.Board, req Request, moves []Move) (pcbmodel.Board, error) {
	groups := map[string]Group{}
	for _, g := range req.Groups {
		groups[g.ID] = g
	}
	seen := map[string]bool{}
	projected := base
	for _, m := range moves {
		g, ok := groups[m.Group]
		if !ok || seen[m.Group] {
			return base, fmt.Errorf("unknown or duplicate move group %s", m.Group)
		}
		seen[m.Group] = true
		anchor, _ := base.Component(g.AnchorRef)
		refs := append([]string(nil), m.Refs...)
		members := append([]string(nil), g.Refs...)
		sort.Strings(refs)
		sort.Strings(members)
		if !reflect.DeepEqual(refs, members) || m.Pivot != anchor.Anchor {
			return base, fmt.Errorf("move %s differs from complete members or original anchor", m.Group)
		}
		allowed := false
		for _, d := range rotations(g) {
			allowed = allowed || d == m.RotationDelta
		}
		if !allowed || !finite(m.DX) || !finite(m.DY) || !allowedOffset(g, m.DX, m.DY) {
			return base, fmt.Errorf("move %s exceeds declared offsets/translationSearch/rotations", m.Group)
		}
		var err error
		projected, _, err = ApplyGroup(projected, g, m)
		if err != nil {
			return base, err
		}
	}
	return projected, nil
}

func allowedOffset(g Group, x, y float64) bool {
	for _, o := range g.Offsets {
		if math.Abs(o.X-x) < 1e-7 && math.Abs(o.Y-y) < 1e-7 {
			return true
		}
	}
	s := g.TranslationSearch
	if s == nil {
		return false
	}
	grid := func(v float64) bool { return math.Abs(v/s.Step-math.Round(v/s.Step)) < 1e-7 }
	return math.Hypot(x, y) <= s.MaxDistance+1e-7 && grid(x) && grid(y)
}

// Neighbors expands only groups named by actual route/mechanical conflicts.
// Mechanically blocked projections are returned for bounded recursive repair;
// the coordinator must check ChangedFindings before attempting any routes.
func Neighbors(base pcbmodel.Board, req Request, current Candidate, refs []string, reason string) []Candidate {
	touched := stringSet(refs)
	groups := append([]Group(nil), req.Groups...)
	sort.Slice(groups, func(i, j int) bool { return groups[i].ID < groups[j].ID })
	var out []Candidate
	for _, g := range groups {
		relevant := touched[g.ID]
		for _, ref := range g.Refs {
			relevant = relevant || touched[ref]
		}
		if !relevant {
			continue
		}
		anchor, _ := base.Component(g.AnchorRef)
		prior := Move{Group: g.ID, Refs: g.Refs, Pivot: anchor.Anchor}
		for _, m := range current.Moves {
			if m.Group == g.ID {
				prior = m
			}
		}
		// Initial rotations may have been rejected before the group moved. Try
		// every declared orientation again at the current and neighboring offsets.
		options := append([]Offset{{X: prior.DX, Y: prior.DY}}, g.Offsets...)
		if s := g.TranslationSearch; s != nil {
			for _, d := range [][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}, {-1, -1}, {1, -1}, {-1, 1}, {1, 1}} {
				options = append(options, Offset{X: prior.DX + float64(d[0])*s.Step, Y: prior.DY + float64(d[1])*s.Step})
			}
		}
		for _, o := range options {
			for _, rotation := range rotations(g) {
				if o.X == prior.DX && o.Y == prior.DY && rotation == prior.RotationDelta {
					continue
				}
				m := prior
				m.DX, m.DY, m.RotationDelta, m.Reason = o.X, o.Y, rotation, reason
				moves := make([]Move, 0, len(current.Moves)+1)
				for _, old := range current.Moves {
					if old.Group != g.ID {
						moves = append(moves, old)
					}
				}
				if m.DX != 0 || m.DY != 0 || m.RotationDelta != 0 {
					moves = append(moves, m)
				}
				sort.Slice(moves, func(i, j int) bool { return moves[i].Group < moves[j].Group })
				// Validate the zero transition too, before dropping its no-op record.
				if !allowedOffset(g, m.DX, m.DY) {
					continue
				}
				board, err := Project(base, req, moves)
				if err != nil {
					continue
				}
				out = append(out, Candidate{Board: board, Moves: moves, Metrics: moveMetrics(moves)})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return Less(out[i], out[j]) })
	return out
}

func StateKey(c Candidate) string { return candidateSignature(c.Board, c.Moves) }
func finite(v float64) bool       { return !math.IsNaN(v) && !math.IsInf(v, 0) }

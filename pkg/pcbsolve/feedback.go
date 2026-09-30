package pcbsolve

import (
	"fmt"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

// Conflict records an actual rejected segment, not a guess based on a ratsnest
// line. It is a local search observation, never an impossibility certificate.
type Conflict struct {
	DemandID         string            `json:"demandId"`
	Net              string            `json:"net"`
	Layer            int               `json:"layer"`
	Kind             string            `json:"kind"`
	Object           string            `json:"object"`
	Ref              string            `json:"ref,omitempty"`
	ConflictNet      string            `json:"conflictNet,omitempty"`
	ConflictDemandID string            `json:"conflictDemandId,omitempty"`
	Segment          [2]pcbmodel.Point `json:"segment"`
}

// Reservation contains the exact trial routes plus the rules needed to reserve
// their space. Routes/vias are planning data, not entries in Board copper.
// Check reconstructs this data, and never trusts a reported "passed" flag.
type Reservation struct {
	DemandID       string                  `json:"demandId"`
	Clearance      float64                 `json:"clearance"`
	TrackClearance float64                 `json:"trackClearance"`
	Routes         []pcbrouting.LayerRoute `json:"routes"`
	Vias           []pcbrouting.RouteVia   `json:"vias,omitempty"`
}

func reservations(board pcbmodel.Board, routes []pcbrouting.JointCandidate) []Reservation {
	out := make([]Reservation, 0, len(routes))
	for _, c := range routes {
		r := Reservation{DemandID: c.DemandID, Clearance: board.Rules.Clearance, TrackClearance: max(board.Rules.Clearance, board.Rules.TrackTrack), Routes: make([]pcbrouting.LayerRoute, len(c.Routes)), Vias: append([]pcbrouting.RouteVia(nil), c.Vias...)}
		for i, path := range c.Routes {
			r.Routes[i] = path
			r.Routes[i].Points = append([]pcbmodel.Point(nil), path.Points...)
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DemandID < out[j].DemandID })
	return out
}

func conflictRefs(conflicts []Conflict, req Request) []string {
	refs := map[string]bool{}
	for _, c := range conflicts {
		if c.Ref != "" {
			refs[c.Ref] = true
		}
		for _, g := range req.Layout.Groups {
			owned := c.Kind == "track" && containsString(g.InternalTrackIDs, c.Object) || c.Kind == "via" && containsString(g.InternalViaIDs, c.Object) || c.Kind == "region" && containsString(g.InternalRegionIDs, c.Object)
			if owned {
				refs[g.ID] = true
			}
		}
		// Competing trial routes can be relieved by moving their endpoint groups.
		if c.ConflictDemandID != "" {
			for _, d := range req.Routing.Demands {
				if d.ID == c.DemandID || d.ID == c.ConflictDemandID {
					refs[strings.SplitN(d.From, ".", 2)[0]] = true
					refs[strings.SplitN(d.To, ".", 2)[0]] = true
				}
			}
		}
	}
	out := make([]string, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

// Automatic movement needs a complete declared connection graph on every net
// leaving the group. Equal net names or existing copper alone are not evidence
// of connectivity. Explicit-offset legacy requests retain their declared scope.
func validateExternalCoverage(board pcbmodel.Board, req Request) error {
	byNet := map[string][]string{}
	for _, c := range board.Components {
		for _, p := range c.Pads {
			if p.Net != "" {
				byNet[p.Net] = append(byNet[p.Net], c.Ref+"."+p.Number)
			}
		}
	}
	graph := map[string][]string{}
	for _, d := range req.Routing.Demands {
		graph[d.From] = append(graph[d.From], d.To)
		graph[d.To] = append(graph[d.To], d.From)
	}
	for _, g := range req.Layout.Groups {
		if g.TranslationSearch == nil {
			continue
		}
		members := map[string]bool{}
		for _, ref := range g.Refs {
			members[ref] = true
		}
		nets := make([]string, 0, len(byNet))
		for net := range byNet {
			nets = append(nets, net)
		}
		sort.Strings(nets)
		for _, net := range nets {
			pads := byNet[net]
			inside, outside := false, false
			for _, p := range pads {
				if members[strings.SplitN(p, ".", 2)[0]] {
					inside = true
				} else {
					outside = true
				}
			}
			if !inside || !outside {
				continue
			}
			seen := map[string]bool{}
			pending := []string{pads[0]}
			for len(pending) > 0 {
				p := pending[0]
				pending = pending[1:]
				if seen[p] {
					continue
				}
				seen[p] = true
				pending = append(pending, graph[p]...)
			}
			for _, p := range pads {
				if !seen[p] {
					return fmt.Errorf("group %s external connection net %s lacks a complete declared demand graph (including %s)", g.ID, net, p)
				}
			}
		}
	}
	return nil
}

func mechanicalRefs(findings []pcblayout.Finding) []string {
	var refs []string
	for _, f := range findings {
		refs = append(refs, f.Objects...)
	}
	return refs
}

package app

import (
	"errors"
	"fmt"
	"sort"
)

// SchematicPlacementConflict is a bounded-search diagnosis, never a global
// impossibility proof. Stable owner IDs permit dependency-aware backjumping and
// machine-readable failure reports without parsing Error's human text.
type SchematicPlacementConflict struct {
	ComponentID         string           `json:"componentId"`
	ComponentRef        string           `json:"componentRef"`
	AttachmentHosts     []string         `json:"attachmentHosts"`
	BlockerOwners       []string         `json:"blockerOwners"`
	ReasonCounts        map[string]int   `json:"reasonCounts"`
	OwnersComplete      bool             `json:"ownersComplete"`
	CandidateCount      int              `json:"candidateCount"`
	SearchExhaustion    string           `json:"searchExhaustion"`
	FutureHostsPossible bool             `json:"futureHostsPossible"`
	LocalLayout         *powerLayoutPlan `json:"localLayout,omitempty"`
	cause               error
}

func (e *SchematicPlacementConflict) Error() string {
	return fmt.Sprintf("component %s (%s) placement search %s after %d candidates: %v", e.ComponentID, e.ComponentRef, e.SearchExhaustion, e.CandidateCount, e.cause)
}
func (e *SchematicPlacementConflict) Unwrap() error { return e.cause }
func (e *SchematicPlacementConflict) FailureDetails() any {
	return struct {
		Kind                      string `json:"kind"`
		GlobalInfeasibilityProven bool   `json:"globalInfeasibilityProven"`
		*SchematicPlacementConflict
	}{"placement-conflict", false, e}
}

// Geometry routines attach semantic provenance at the point of detection.
// Unknown provenance is preserved and disables selective backjumping.
type schGeometryObstruction struct {
	kind             string
	owners           []string
	blockers         []string
	blockersExplicit bool
	nets             []string
	complete         bool
	cause            error
}

func (e *schGeometryObstruction) Error() string { return e.cause.Error() }
func (e *schGeometryObstruction) Unwrap() error { return e.cause }
func schObstruction(kind string, cause error, owners ...string) error {
	e := &schGeometryObstruction{kind: kind, cause: cause, owners: append([]string(nil), owners...), blockers: append([]string(nil), owners...), blockersExplicit: len(owners) > 0, complete: len(owners) > 0}
	for _, owner := range owners {
		if owner == "" {
			e.complete = false
		}
	}
	return e
}
func schWireObstruction(p *powerLayoutPlan, kind string, cause error, nets []string, owners ...string) error {
	e := schObstruction(kind, cause, owners...).(*schGeometryObstruction)
	e.nets = append([]string(nil), nets...)
	e.complete = true
	for _, net := range nets {
		found := false
		for _, c := range p.Placements {
			for _, pin := range c.Pins {
				if net != "" && pin.Net == net {
					e.owners = append(e.owners, c.Designator)
					found = true
					break
				}
			}
		}
		if !found {
			e.complete = false
		}
	}
	// Explicit owners identify the object hit by the rejected edge. If the
	// obstruction is an ownerless wire contact, the components carrying that
	// foreign net are the only available blocker provenance. Keep these apart
	// from the broader electrical owners used by legacy placement diagnostics:
	// source-net membership is not evidence that a component blocked this edge.
	if len(e.blockers) == 0 {
		e.blockers = append([]string(nil), e.owners...)
	}
	for _, owner := range e.owners {
		if owner == "" {
			e.complete = false
		}
	}
	return e
}

type placementCandidateConflict struct {
	refs       map[string]bool
	reasons    map[string]int
	complete   bool
	candidates int
	exhaustion string
	cause      error
}

func newPlacementCandidateConflict() *placementCandidateConflict {
	return &placementCandidateConflict{refs: map[string]bool{}, reasons: map[string]int{}, complete: true}
}
func (e *placementCandidateConflict) Error() string {
	return fmt.Sprintf("%s after %d placement candidates: %v", e.exhaustion, e.candidates, e.cause)
}
func (e *placementCandidateConflict) Unwrap() error { return e.cause }
func (e *placementCandidateConflict) observe(err error) {
	if err == nil {
		return
	}
	e.cause = err
	var o *schGeometryObstruction
	if !errors.As(err, &o) {
		e.complete = false
		e.reasons["unattributed"]++
		return
	}
	e.complete = e.complete && o.complete
	e.reasons[o.kind]++
	for _, ref := range o.owners {
		if ref != "" {
			e.refs[ref] = true
		}
	}
}
func (e *placementCandidateConflict) finish(kind string, cause error) error {
	e.exhaustion = kind
	if cause != nil {
		e.cause = cause
	}
	if kind == "candidate-budget" {
		e.complete = false
	}
	return e
}

func (s *schematicRepairSearch) placementConflict(id string, pairs []libAttachmentPair, placed map[string]powerLayoutPlacement, pending []string, current *powerLayoutPlan, cause error) *SchematicPlacementConflict {
	e := &SchematicPlacementConflict{ComponentID: id, ComponentRef: s.measured[id].Designator, ReasonCounts: map[string]int{}, cause: cause, SearchExhaustion: "unknown", OwnersComplete: false}
	if current != nil {
		layout := *current
		layout.Placements = append([]powerLayoutPlacement(nil), current.Placements...)
		layout.Wires = clonePowerLayoutWires(current.Wires)
		layout.Flags = append([]powerLayoutFlag(nil), current.Flags...)
		e.LocalLayout = &layout
	}
	var raw *placementCandidateConflict
	if errors.As(cause, &raw) {
		e.ReasonCounts = raw.reasons
		e.CandidateCount = raw.candidates
		e.SearchExhaustion = raw.exhaustion
		e.OwnersComplete = raw.complete
		for ref := range raw.refs {
			known := false
			for cid, c := range s.measured {
				if c.Designator == ref {
					e.BlockerOwners = append(e.BlockerOwners, cid)
					known = true
					break
				}
			}
			if !known {
				e.OwnersComplete = false
			}
		}
	}
	hosts := map[string]bool{}
	for _, pair := range pairs {
		found := false
		for cid, c := range placed {
			for _, pin := range c.Pins {
				if pin.Number == pair.host.Number && pin.X == pair.host.X && pin.Y == pair.host.Y {
					hosts[cid] = true
					found = true
				}
			}
		}
		if !found {
			e.OwnersComplete = false
		}
	}
	for host := range hosts {
		e.AttachmentHosts = append(e.AttachmentHosts, host)
	}
	sort.Strings(e.AttachmentHosts)
	sort.Strings(e.BlockerOwners)
	e.FutureHostsPossible = s.hasFutureAttachmentHost(id, pending)
	return e
}

// Adding a not-yet-placed same-net host may open a new attachment option. An
// already usable host is not authority to prune that dependency ordering.
func (s *schematicRepairSearch) hasFutureAttachmentHost(id string, pending []string) bool {
	hint := s.hints[id]
	for _, hostID := range pending {
		if hostID == id {
			continue
		}
		if hint.AttachTo != nil && hint.AttachTo.ComponentID != hostID {
			continue
		}
		for _, host := range s.measured[hostID].Pins {
			if host.Net == "" || (hint.AttachTo != nil && hint.AttachTo.PinNumber != host.Number) || (hint.AttachTo == nil && s.input.NetPolicies[host.Net] == "local_ground") {
				continue
			}
			for _, own := range s.measured[id].Pins {
				if own.Net == host.Net && (hint.PinNumber == "" || hint.PinNumber == own.Number) {
					return true
				}
			}
		}
	}
	return false
}

func (s *schematicRepairSearch) placementParticipates(id string, e *SchematicPlacementConflict) bool {
	if !e.OwnersComplete || e.FutureHostsPossible {
		return true
	}
	for _, owner := range e.AttachmentHosts {
		if id == owner {
			return true
		}
	}
	for _, owner := range e.BlockerOwners {
		if id == owner {
			return true
		}
	}
	return false
}

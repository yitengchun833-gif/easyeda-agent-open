package app

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

func decodeSchematicRouteInput(raw []byte) (SchematicLayoutInput, error) {
	var input SchematicLayoutInput
	if err := connectivity.DecodeStrictDesignJSON(raw, &input); err != nil {
		return input, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return SchematicLayoutInput{}, err
	}
	// Reuse strict measurement evidence checks; route-only has no core.
	if _, present := fields["coreComponentId"]; !present && fields != nil {
		fields["coreComponentId"] = json.RawMessage(`"route-only"`)
		var err error
		raw, err = json.Marshal(fields)
		if err != nil {
			return SchematicLayoutInput{}, err
		}
	}
	return decodeSchematicLayoutInput(raw)
}

// PlanSchematicRoutes routes direct nets without changing measured placements.
// Non-direct nets remain obstacles and do not imply physically connected trees.
func PlanSchematicRoutes(input SchematicLayoutInput) (*SchematicLayoutResult, error) {
	return planSchematicRoutes(input, nil, false)
}

// ValidateSchematicRoutes checks supplied polylines without searching or editing.
func ValidateSchematicRoutes(input SchematicLayoutInput, wires []SchematicWire) (*SchematicLayoutResult, error) {
	return planSchematicRoutes(input, wires, true)
}

func decodeSchematicRouteWires(raw []byte) ([]SchematicWire, error) {
	var packet struct {
		Routes []struct {
			Net    string       `json:"net"`
			Points [][]*float64 `json:"points"`
		} `json:"routes"`
	}
	if err := connectivity.DecodeStrictDesignJSON(raw, &packet); err != nil {
		return nil, err
	}
	if packet.Routes == nil {
		return nil, fmt.Errorf("routes requires an explicit array")
	}
	wires := make([]SchematicWire, 0, len(packet.Routes))
	for i, route := range packet.Routes {
		wire := SchematicWire{Net: route.Net}
		if strings.TrimSpace(route.Net) == "" || len(route.Points) < 2 {
			return nil, fmt.Errorf("routes[%d] needs net and at least two points", i)
		}
		for _, point := range route.Points {
			if len(point) != 2 || point[0] == nil || point[1] == nil {
				return nil, fmt.Errorf("routes[%d] points need explicit numeric x,y", i)
			}
			wire.Points = append(wire.Points, [2]float64{*point[0], *point[1]})
		}
		wires = append(wires, wire)
	}
	return wires, nil
}

func planSchematicRoutes(input SchematicLayoutInput, planned []SchematicWire, validateOnly bool) (*SchematicLayoutResult, error) {
	// Detach caller evidence before returning slices or invoking the shared router.
	raw, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	var detached SchematicLayoutInput
	if err := json.Unmarshal(raw, &detached); err != nil {
		return nil, err
	}
	input = detached
	if input.SchemaVersion != 1 || len(input.Components) == 0 {
		return nil, fmt.Errorf("schemaVersion:1 and components required")
	}
	if (input.LayoutMode != "" && input.LayoutMode != "search") || input.Optimization != nil || len(input.Attachments) != 0 || len(input.MarkerAnchors) != 0 || input.MaxCandidates != 0 {
		return nil, fmt.Errorf("route-only does not accept layout modes, optimization, attachments, markerAnchors or maxCandidates")
	}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{}, Wires: []powerLayoutWire{}, Flags: []powerLayoutFlag{}, measuredTextOnly: true}
	refs, ids := map[string]string{}, map[string]bool{}
	states := map[string]map[string]string{}
	used := map[string]bool{}
	for _, c := range input.Components {
		m := c.Measurement
		if strings.TrimSpace(c.ID) == "" || ids[c.ID] || strings.TrimSpace(m.Designator) == "" || refs[m.Designator] != "" {
			return nil, fmt.Errorf("invalid/duplicate component ID or designator %s", c.ID)
		}
		if !plBoxValid(m.BBox) || !plGrid(m.X) || !plGrid(m.Y) || !plFinite(m.Rotation) || math.Mod(m.Rotation, 90) != 0 || len(m.Pins) == 0 {
			return nil, fmt.Errorf("%s incomplete measured geometry", c.ID)
		}
		if len(c.AllowedRotations) != 0 {
			return nil, fmt.Errorf("%s route-only keeps measured rotation; omit allowedRotations", c.ID)
		}
		pins := map[string]bool{}
		for _, q := range m.Pins {
			if q.Number == "" || pins[q.Number] || !plGrid(q.X) || !plGrid(q.Y) {
				return nil, fmt.Errorf("%s invalid/duplicate pin %s", c.ID, q.Number)
			}
			pins[q.Number] = true
			if q.Net == "" {
				if c.PinStates[q.Number] != "nc" && c.PinStates[q.Number] != "unconnected" {
					return nil, fmt.Errorf("%s.%s needs explicit nc/unconnected state", c.ID, q.Number)
				}
			} else {
				if strings.TrimSpace(q.Net) == "" || c.PinStates[q.Number] != "" {
					return nil, fmt.Errorf("%s.%s conflicts with connected net", c.ID, q.Number)
				}
				used[q.Net] = true
			}
			if _, err := libPinSide(q, m.BBox); err != nil {
				return nil, err
			}
		}
		for number := range c.PinStates {
			if !pins[number] {
				return nil, fmt.Errorf("%s unknown pin state %s", c.ID, number)
			}
		}
		for _, box := range m.TextBBoxes {
			if !plBoxValid(box) {
				return nil, fmt.Errorf("%s invalid text bbox", c.ID)
			}
		}
		p.Placements = append(p.Placements, m)
		refs[m.Designator], ids[c.ID], states[c.ID] = c.ID, true, c.PinStates
	}
	policies := map[string]string{}
	for net := range used {
		switch input.NetPolicies[net] {
		case "direct":
			policies[net] = "direct"
		case "module_port", "local_power", "local_ground":
			// The direct-net pass skips rail-class policies. Preserve these pins
			// as obstacles without opportunistically joining module ports.
			policies[net] = "local_power"
		default:
			return nil, fmt.Errorf("net %s needs explicit policy", net)
		}
	}
	for net := range input.NetPolicies {
		if !used[net] {
			return nil, fmt.Errorf("unused policy %s", net)
		}
	}
	if err := validateLibGeometry(&p); err != nil {
		return nil, fmt.Errorf("fixed placement: %w", err)
	}
	mode := "route-only"
	var routing *schematicRoutingContext
	if validateOnly {
		mode = "route-validate"
		for i, wire := range planned {
			if !used[wire.Net] || len(wire.Points) < 2 {
				return nil, fmt.Errorf("route %d needs a known net and at least two points", i)
			}
			for j := 1; j < len(wire.Points); j++ {
				p.Wires = append(p.Wires, SchematicWire{Net: wire.Net, Points: [][2]float64{wire.Points[j-1], wire.Points[j]}})
			}
		}
	} else {
		routing, err = newSchematicRoutingContext(input.Routing, input.Components)
		if err != nil {
			return nil, err
		}
		routing.policies = policies
		// Fixed placements have no later relocation phase to reserve nodes for.
		routing.relocation = 1
		if err := libJoinDirectNets(&p, policies, routing); err != nil {
			return nil, err
		}
	}
	if err := validateLibGeometry(&p); err != nil {
		return nil, err
	}
	joined := map[string]bool{}
	for _, island := range libIslands(&p) {
		if policies[island.net] == "direct" {
			if joined[island.net] {
				return nil, fmt.Errorf("direct net %s remains physically disconnected", island.net)
			}
			joined[island.net] = true
		}
	}
	if p.Wires == nil {
		p.Wires = []powerLayoutWire{}
	}
	return &SchematicLayoutResult{SchemaVersion: 1, LayoutMode: mode, ComponentIDs: refs, PinStates: states,
		Placements: p.Placements, Wires: p.Wires, Flags: []SchematicMarker{}, Score: libCandidateScore(&p), Routing: routing.snapshot()}, nil
}

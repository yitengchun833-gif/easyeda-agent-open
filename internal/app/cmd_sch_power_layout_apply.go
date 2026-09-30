package app

import (
	"encoding/json"
	"fmt"
	"sort"
)

// powerLayoutPlaybook compiles an offline plan into the existing ordered Apply
// runner. Geometry is checked both before moving and before drawing: an EDA
// rotation convention or a user edit cannot silently redirect a planned wire.
func powerLayoutPlaybook(plan *powerLayoutPlan, sourceRaw []byte) (*playbook, error) {
	var env struct {
		Result  json.RawMessage `json:"result"`
		Context struct {
			Project string `json:"projectUuid"`
			Doc     string `json:"documentUuid"`
		} `json:"context"`
	}
	if err := json.Unmarshal(sourceRaw, &env); err != nil {
		return nil, err
	}
	if env.Context.Doc != "" && env.Context.Doc != plan.DocumentID {
		return nil, fmt.Errorf("source document differs from planned document")
	}
	if len(env.Result) > 0 {
		sourceRaw = env.Result
	}
	var source powerLayoutSnapshot
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		return nil, err
	}
	var inventory struct {
		Wires []struct {
			PrimitiveID     string `json:"primitiveId"`
			WirePrimitiveID string `json:"wirePrimitiveId"`
		} `json:"wires"`
		Summary *struct {
			Wires        int `json:"wires"`
			Buses        int `json:"buses"`
			ShortSymbols int `json:"shortSymbols"`
		} `json:"connectivitySummary"`
	}
	if err := json.Unmarshal(sourceRaw, &inventory); err != nil {
		return nil, err
	}
	if inventory.Summary == nil || inventory.Wires == nil {
		return nil, fmt.Errorf("Apply requires includeWires and includeConnectivitySummary in the source snapshot; missing inventory does not mean an empty page")
	}
	if inventory.Summary.Buses != 0 || inventory.Summary.ShortSymbols != 0 {
		return nil, fmt.Errorf("power-layout apply does not support buses or short symbols")
	}
	deleteIDs := map[string]bool{}
	wireIDs := map[string]bool{}
	for _, w := range inventory.Wires {
		id := w.PrimitiveID
		if id == "" {
			id = w.WirePrimitiveID
		}
		if id == "" {
			return nil, fmt.Errorf("wire snapshot lacks primitiveId; refresh its typed geometry inventory before generating a destructive Apply")
		}
		deleteIDs[id] = true
		wireIDs[id] = true
	}
	if len(wireIDs) != inventory.Summary.Wires {
		return nil, fmt.Errorf("wire inventory is incomplete: %d ids for %d wires", len(wireIDs), inventory.Summary.Wires)
	}
	before := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}
	markers := map[string]int{"netflag": 0, "netport": 0, "netlabel": 0}
	for _, c := range source.Components {
		if c.ComponentType == "sheet" {
			continue
		}
		if _, ok := markers[c.ComponentType]; ok {
			if c.PrimitiveID == "" {
				return nil, fmt.Errorf("marker snapshot lacks primitiveId")
			}
			deleteIDs[c.PrimitiveID] = true
			markers[c.ComponentType]++
			continue
		}
		if c.ComponentType != "part" {
			return nil, fmt.Errorf("unsupported cleanup type %q", c.ComponentType)
		}
		p := schematicPartExpectation{PrimitiveID: c.PrimitiveID, X: c.X, Y: c.Y, Rotation: c.Rotation, Mirror: c.Mirror, Pins: map[string]schematicPinExpectation{}}
		for _, pin := range c.Pins {
			p.Pins[pin.Number] = schematicPinExpectation{X: pin.X, Y: pin.Y}
		}
		before.Parts[c.Designator] = p
	}
	if len(before.Parts) != len(plan.Placements) {
		return nil, fmt.Errorf("source and planned part sets differ")
	}
	for _, c := range plan.Placements {
		if before.Parts[c.Designator].PrimitiveID != c.PrimitiveID || deleteIDs[c.PrimitiveID] {
			return nil, fmt.Errorf("source/planned primitive identity mismatch: %s", c.Designator)
		}
	}
	if err := before.validate(); err != nil {
		return nil, err
	}
	pb := &playbook{Version: 1, RequireFullExecution: true, Meta: playbookMeta{Name: "POWER direct-wire layout", Project: env.Context.Project, Doc: plan.DocumentID}}
	readPayload := map[string]any{"includePins": true, "includeBBox": true, "includeWires": true, "includeConnectivitySummary": true}
	counts := map[string]string{"$.connectivitySummary.wires": fmt.Sprintf("==%d", len(wireIDs)), "$.connectivitySummary.buses": "==0", "$.connectivitySummary.shortSymbols": "==0"}
	for kind, count := range markers {
		counts["$.connectivitySummary."+kind+"s"] = fmt.Sprintf("==%d", count)
	}
	pb.Steps = append(pb.Steps, playbookStep{ID: "source-geometry", Action: "schematic.components.list", Payload: readPayload, ExpectSchematic: before, Assert: counts})
	if len(deleteIDs) > 0 {
		ids := make([]string, 0, len(deleteIDs))
		for id := range deleteIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		pb.Steps = append(pb.Steps, playbookStep{ID: "remove-old-connections", Action: "schematic.primitives.delete", Payload: map[string]any{"primitiveIds": ids}, Assert: map[string]string{"$.total": fmt.Sprintf("==%d", len(ids))}})
	}
	for _, c := range plan.Placements {
		pb.Steps = append(pb.Steps, playbookStep{ID: "place-" + c.Designator, Action: "schematic.component.modify", Payload: map[string]any{"primitiveId": c.PrimitiveID, "patch": map[string]any{"x": c.X, "y": c.Y, "rotation": c.Rotation, "mirror": c.Mirror}}})
	}
	cleanCounts := map[string]string{}
	for path := range counts {
		cleanCounts[path] = "==0"
	}
	pb.Steps = append(pb.Steps, playbookStep{ID: "verify-position-before-wiring", Action: "schematic.components.list", Payload: readPayload, ExpectSchematic: powerLayoutExpectation(plan, false), Assert: cleanCounts})
	for i, w := range plan.Wires {
		// The plan carries the net, but each connected tree gets its name from
		// the local symbol below. EasyEDA persists an attribute for EVERY named
		// segment; merging two "+3V3" segments produces a duplicate-name DRC.
		pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("wire-%d", i+1), Name: w.Net + " direct wire", Action: "schematic.wire.create", Payload: map[string]any{"points": w.Points}, Capture: map[string]string{fmt.Sprintf("WIRE_%d", i+1): "$.primitiveId"}})
	}
	for i, f := range plan.Flags {
		pb.Steps = append(pb.Steps, playbookStep{ID: fmt.Sprintf("local-symbol-%d", i+1), Action: "schematic.power.connect_pin", Payload: map[string]any{"pinX": f.PinX, "pinY": f.PinY, "kind": f.Kind, "net": f.Net, "direction": f.Direction, "offset": f.Offset}})
	}
	frameSteps, err := powerLayoutFrameSteps(plan)
	if err != nil {
		return nil, err
	}
	pb.Steps = append(pb.Steps, frameSteps...)
	pb.Steps = append(pb.Steps,
		playbookStep{ID: "verify-all-pin-nets", Action: "schematic.components.list", Payload: readPayload, ExpectSchematic: powerLayoutExpectation(plan, true)},
		playbookStep{ID: "verify-electrical", Action: "schematic.check", Assert: map[string]string{"$.passed": "true"}},
		playbookStep{ID: "verify-wire-trees", Action: "schematic.bridgeCheck", Assert: map[string]string{"$.passed": "true"}},
		playbookStep{ID: "save-verified-power", Action: "schematic.save", Assert: map[string]string{"$.saved": "true"}},
	)
	if errs := preflight(pb, nil); len(errs) > 0 {
		return nil, fmt.Errorf("generated power-layout Apply failed preflight: %v", errs)
	}
	return pb, nil
}

func powerLayoutFrameSteps(plan *powerLayoutPlan) ([]playbookStep, error) {
	if len(plan.Frames) == 0 {
		return nil, fmt.Errorf("layout plan has no module frames")
	}
	raw, err := json.Marshal(schFrameDocument{SchemaVersion: 1, DocumentID: plan.DocumentID, Frames: plan.Frames})
	if err != nil {
		return nil, err
	}
	return []playbookStep{
		{ID: "apply-module-frames", Run: "sch frame apply", Flags: map[string]any{"data": string(raw)}, Assert: map[string]string{"$.verified": "true"}},
		{ID: "verify-module-frames", Run: "sch frame check", Flags: map[string]any{"data": string(raw)}, Assert: map[string]string{"$.verified": "true"}},
	}, nil
}

// Decoration-only conversion uses the same generated data and adapter as the
// full pipeline. It cannot move a part or delete a wire; every existing pin's
// geometry/net must already match, both offline and immediately before Apply.
func powerLayoutFramePlaybook(plan *powerLayoutPlan, sourceRaw []byte) (*playbook, error) {
	var env struct {
		Result  json.RawMessage `json:"result"`
		Context struct {
			Project string `json:"projectUuid"`
			Doc     string `json:"documentUuid"`
		} `json:"context"`
	}
	if err := json.Unmarshal(sourceRaw, &env); err != nil {
		return nil, err
	}
	if env.Context.Doc != "" && env.Context.Doc != plan.DocumentID {
		return nil, fmt.Errorf("source document differs from planned document")
	}
	if len(env.Result) > 0 {
		sourceRaw = env.Result
	}
	var source map[string]any
	if err := json.Unmarshal(sourceRaw, &source); err != nil {
		return nil, err
	}
	expect := powerLayoutExpectation(plan, true)
	if err := expect.check(source, nil); err != nil {
		return nil, fmt.Errorf("frames-only requires the existing circuit at planned geometry/nets: %w", err)
	}
	steps, err := powerLayoutFrameSteps(plan)
	if err != nil {
		return nil, err
	}
	pb := &playbook{Version: 1, RequireFullExecution: true, Meta: playbookMeta{Name: "Module frame conversion validation", Project: env.Context.Project, Doc: plan.DocumentID}}
	pb.Steps = append(pb.Steps, playbookStep{ID: "verify-circuit-before-frames", Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: expect})
	pb.Steps = append(pb.Steps, steps...)
	pb.Steps = append(pb.Steps,
		playbookStep{ID: "verify-circuit-after-frames", Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: expect},
		playbookStep{ID: "save-verified-frames", Action: "schematic.save", Assert: map[string]string{"$.saved": "true"}},
	)
	if errs := preflight(pb, nil); len(errs) > 0 {
		return nil, fmt.Errorf("generated frame Apply failed preflight: %v", errs)
	}
	return pb, nil
}

func powerLayoutExpectation(plan *powerLayoutPlan, nets bool) *schematicStateExpectation {
	e := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}
	for _, c := range plan.Placements {
		p := schematicPartExpectation{PrimitiveID: c.PrimitiveID, X: &c.X, Y: &c.Y, Rotation: &c.Rotation, Mirror: &c.Mirror, Pins: map[string]schematicPinExpectation{}}
		for _, pin := range c.Pins {
			ep := schematicPinExpectation{X: &pin.X, Y: &pin.Y}
			if nets {
				ep.Net = &pin.Net
			}
			p.Pins[pin.Number] = ep
		}
		e.Parts[c.Designator] = p
	}
	return e
}

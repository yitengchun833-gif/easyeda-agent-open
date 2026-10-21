package app

import (
	"encoding/json"
	"fmt"
	"maps"
	"sort"

	"github.com/zhoushoujianwork/easyeda-agent/internal/connectivity"
)

// Native snapshots enter the existing canonical/drawing diff; they are observations, never execution authority.
func decodeNativeSchDesignInput(raw []byte) (schDesignInput, error) {
	var snapshot map[string]any
	if err := connectivity.DecodeStrictDesignJSON(raw, &snapshot); err != nil {
		return schDesignInput{}, err
	}
	scope, _ := snapshot["readScope"].(map[string]any)
	if scope["complete"] != true || scope["concurrentChange"] != false || scope["allPages"] == true || snapshot["partial"] == true || snapshot["wiresAvailable"] != true {
		return schDesignInput{}, fmt.Errorf("native design diff needs complete single-page wire/pin/geometry observations")
	}
	for _, key := range []string{"primitiveIds", "missingIds"} {
		if values, ok := scope[key].([]any); ok && len(values) > 0 {
			return schDesignInput{}, fmt.Errorf("native diff cannot use partial scope")
		}
	}
	project, document := asString(snapshot["projectUuid"]), asString(snapshot["documentUuid"])
	if project == "" || document == "" {
		return schDesignInput{}, fmt.Errorf("native diff needs projectUuid/documentUuid")
	}
	components, ok := snapshot["components"].([]any)
	if !ok {
		return schDesignInput{}, fmt.Errorf("native component inventory unavailable")
	}
	hasParts := false
	for _, item := range components {
		part, ok := item.(map[string]any)
		if !ok {
			return schDesignInput{}, fmt.Errorf("invalid component record")
		}
		if part["componentType"] == "part" {
			hasParts = true
			if part["netlistAvailable"] != true || part["pinsAvailable"] != true {
				return schDesignInput{}, fmt.Errorf("native pin evidence unavailable")
			}
		}
	}
	if hasParts {
		if _, _, _, err := parseAutolayoutPartsChecked(snapshot, true); err != nil {
			return schDesignInput{}, err
		}
	}
	if _, ok := snapshot["wires"].([]any); !ok {
		return schDesignInput{}, fmt.Errorf("native wire inventory unavailable")
	}
	page, ok := snapshot["pagePrimitives"].(map[string]any)
	if !ok {
		return schDesignInput{}, fmt.Errorf("native drawing inventory unavailable")
	}
	for _, kind := range []string{"components", "wires", "buses", "arcs", "circles", "rectangles", "polygons", "texts", "attributes", "objects"} {
		if _, ok := page[kind].([]any); !ok {
			return schDesignInput{}, fmt.Errorf("native drawing inventory missing %s", kind)
		}
	}
	doc, err := connectivity.FromRead(snapshot)
	if err != nil {
		return schDesignInput{}, err
	}
	doc.ProjectID, doc.DocumentID = project, document
	encoded, err := json.Marshal(doc)
	if err != nil {
		return schDesignInput{}, err
	}
	var canonical map[string]any
	if err = json.Unmarshal(encoded, &canonical); err != nil {
		return schDesignInput{}, err
	}
	// FromRead's typed IR omits zero coordinates; restore explicit observed zeros before evidence decoding.
	for _, item := range canonical["components"].([]any) {
		component := item.(map[string]any)
		for _, observed := range snapshot["components"].([]any) {
			part := observed.(map[string]any)
			if part["designator"] != component["ref"] {
				continue
			}
			for _, pinItem := range component["pins"].([]any) {
				pin := pinItem.(map[string]any)
				for _, actualItem := range part["pins"].([]any) {
					actual := actualItem.(map[string]any)
					if actual["pinNumber"] == pin["number"] || actual["number"] == pin["number"] {
						pin["x"], pin["y"] = actual["x"], actual["y"]
					}
				}
			}
		}
	}
	encoded, err = json.Marshal(canonical)
	if err != nil {
		return schDesignInput{}, err
	}
	evidence, err := connectivity.DecodeDesignEvidence(encoded)
	if err != nil {
		return schDesignInput{}, err
	}
	drawing := map[string]any{"components": []any{}, "wires": snapshot["wires"], "pagePrimitives": nativeDrawingInventory(snapshot, page)}
	for _, item := range snapshot["components"].([]any) {
		part := item.(map[string]any)
		state := map[string]any{}
		for _, key := range []string{"primitiveId", "componentType", "uniqueId", "designator", "subPartName", "x", "y", "rotation", "mirror", "bbox", "device", "symbol", "footprint", "name", "manufacturer", "manufacturerId", "supplier", "supplierId", "addIntoBom", "addIntoPcb", "otherProperty"} {
			if value, exists := part[key]; exists {
				state[key] = value
			}
		}
		if part["componentType"] == "sheet" {
			delete(state, "otherProperty")
		}
		drawing["components"] = append(drawing["components"].([]any), state)
	}
	for _, list := range []any{drawing["components"], drawing["wires"]} {
		if rows, ok := list.([]any); ok {
			sort.Slice(rows, func(i, j int) bool {
				a, _ := json.Marshal(rows[i])
				b, _ := json.Marshal(rows[j])
				return string(a) < string(b)
			})
		}
	}
	drawing = normalizeSchDesignDrawingNumbers(drawing).(map[string]any)
	return schDesignInput{canonical: evidence, drawing: drawing, kind: "native-snapshot"}, nil
}

// Reopening rebuilds symbol-pin display attribute IDs, but not their native pin ownership or content.
func nativeDrawingInventory(snapshot, page map[string]any) map[string]any {
	pins, emptyWires := map[string]bool{}, map[string]bool{}
	for _, item := range snapshot["components"].([]any) {
		part := item.(map[string]any)
		items, _ := part["pins"].([]any)
		for _, item := range items {
			pin, _ := item.(map[string]any)
			if id := asString(pin["primitiveId"]); id != "" {
				pins[id] = true
			}
		}
	}
	for _, item := range page["wires"].([]any) {
		wire, _ := item.(map[string]any)
		if net, ok := wire["Net"].(string); ok && net == "" {
			emptyWires[asString(wire["primitiveId"])] = true
		}
	}
	bounds, _ := snapshot["pagePrimitiveBounds"].(map[string]any)
	attributes := []any{}
	for _, item := range page["attributes"].([]any) {
		attr, ok := item.(map[string]any)
		if !ok {
			attributes = append(attributes, item)
			continue
		}
		state := maps.Clone(attr)
		parent, key := asString(attr["ParentPrimitiveId"]), asString(attr["Key"])
		if pins[parent] && (key == "Pin Name" || key == "Pin Number" || key == "Pin Type") {
			state["primitiveId"] = "native-pin-attribute:" + parent + ":" + key
		}
		if value, ok := attr["Value"].(string); ok && value == "" && key == "Name" && attr["KeyVisible"] == false && emptyWires[parent] {
			entry, _ := bounds[asString(attr["primitiveId"])].(map[string]any)
			box, _ := entry["bbox"].(map[string]any)
			x0, ax := box["minX"].(float64)
			x1, bx := box["maxX"].(float64)
			y0, ay := box["minY"].(float64)
			y1, by := box["maxY"].(float64)
			if _, visibleKnown := attr["ValueVisible"].(bool); visibleKnown && ax && bx && ay && by && x0 == x1 && y0 == y1 {
				// An empty value with a hidden key and measured zero area has no rendered label.
				state["ValueVisible"] = false
			}
		}
		attributes = append(attributes, state)
	}
	result := maps.Clone(page)
	result["attributes"] = sortDesignDrawingList(attributes)
	return result
}

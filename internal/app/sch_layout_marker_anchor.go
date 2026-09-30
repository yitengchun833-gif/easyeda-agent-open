package app

import (
	"fmt"
	"sort"
)

func markerAnchorFloat(v float64) *float64 { n := v; return &n }

func setSchematicMarkerAnchorZone(layout *SchematicLayoutResult, zoneID string) {
	if layout == nil {
		return
	}
	for i := range layout.Flags {
		if layout.Flags[i].Anchor != nil {
			layout.Flags[i].Anchor.ZoneID = zoneID
		}
	}
	for i := range layout.Variants {
		setSchematicMarkerAnchorZone(layout.Variants[i].Layout, zoneID)
	}
}

func inferredSchematicMarkerAnchor(layout *SchematicLayoutResult, flag powerLayoutFlag, zoneID string) (*SchematicMarkerAnchor, error) {
	var pins []SchematicMarkerAnchor
	for _, component := range layout.Placements {
		for _, pin := range component.Pins {
			if pin.Net != flag.Net || pin.X != flag.PinX || pin.Y != flag.PinY {
				continue
			}
			side, err := libPinSide(pin, component.BBox)
			if err != nil {
				return nil, err
			}
			if side != flag.Direction {
				return nil, fmt.Errorf("pin marker %s.%s direction %s differs from official outward %s", component.Designator, pin.Number, flag.Direction, side)
			}
			pins = append(pins, SchematicMarkerAnchor{Type: "pin", ComponentID: layout.ComponentIDs[component.Designator], PinNumber: pin.Number, ZoneID: zoneID, Net: flag.Net})
		}
	}
	if len(pins) == 1 {
		return &pins[0], nil
	}
	if len(pins) > 1 {
		return nil, fmt.Errorf("marker %s at %g,%g ambiguously touches %d pins", flag.Net, flag.PinX, flag.PinY, len(pins))
	}
	p := powerLayoutPlan{Placements: layout.Placements, Wires: layout.Wires}
	islands := libIslands(&p)
	var keys []string
	for _, island := range islands {
		if island.net != flag.Net {
			continue
		}
		for _, index := range island.wireIndices {
			wire := layout.Wires[index]
			if plOnSegment([2]float64{flag.PinX, flag.PinY}, wire.Points[0], wire.Points[1]) {
				keys = append(keys, island.key)
				break
			}
		}
	}
	sort.Strings(keys)
	keys = compactStrings(keys)
	if len(keys) != 1 {
		return nil, fmt.Errorf("marker %s at %g,%g needs one physical wire-tree anchor; found %d", flag.Net, flag.PinX, flag.PinY, len(keys))
	}
	return &SchematicMarkerAnchor{Type: "wire_tree", ZoneID: zoneID, Net: flag.Net, X: markerAnchorFloat(flag.PinX), Y: markerAnchorFloat(flag.PinY)}, nil
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func sameSchematicMarkerAnchor(a, b SchematicMarkerAnchor) bool {
	if a.Type != b.Type || a.ComponentID != b.ComponentID || a.PinNumber != b.PinNumber || a.Net != b.Net {
		return false
	}
	if a.Type == "wire_tree" {
		return a.X != nil && a.Y != nil && b.X != nil && b.Y != nil && *a.X == *b.X && *a.Y == *b.Y
	}
	return true
}

func annotateSchematicMarkerAnchors(layout *SchematicLayoutResult, explicit []SchematicMarkerAnchor, zoneID string) error {
	if layout == nil {
		return fmt.Errorf("layout required")
	}
	used := make([]bool, len(explicit))
	for i := range layout.Flags {
		anchor, err := inferredSchematicMarkerAnchor(layout, layout.Flags[i], zoneID)
		if err != nil {
			return err
		}
		if len(explicit) > 0 {
			matched := -1
			for j, want := range explicit {
				if !used[j] && sameSchematicMarkerAnchor(*anchor, want) {
					matched = j
					break
				}
			}
			if matched < 0 {
				return fmt.Errorf("generated marker %s has undeclared %s anchor", layout.Flags[i].Net, anchor.Type)
			}
			used[matched] = true
		}
		layout.Flags[i].Anchor = anchor
	}
	for i, ok := range used {
		if !ok {
			return fmt.Errorf("declared marker anchor %d was not generated", i)
		}
	}
	return nil
}

package app

import (
	"strings"
	"testing"
)

func markerAnchorLayout() *SchematicLayoutResult {
	rotation := 180.0
	return &SchematicLayoutResult{
		ComponentIDs: map[string]string{"U1": "stable-u1"},
		Placements:   []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 20, MaxY: 20}, Pins: []powerLayoutPin{{Number: "1", Net: "SIG", X: 0, Y: 10, Rotation: &rotation}}}},
	}
}

func TestMarkerAnchorPinUsesOfficialOutwardDirection(t *testing.T) {
	layout := markerAnchorLayout()
	flag := powerLayoutFlag{Net: "SIG", Kind: "net_port_bi", PinX: 0, PinY: 10, Direction: "left", Offset: 20}
	anchor, err := inferredSchematicMarkerAnchor(layout, flag, "zone")
	if err != nil || anchor.Type != "pin" || anchor.ComponentID != "stable-u1" || anchor.PinNumber != "1" || anchor.ZoneID != "zone" {
		t.Fatalf("pin anchor was not inferred: %+v %v", anchor, err)
	}
	flag.Direction = "right"
	if _, err := inferredSchematicMarkerAnchor(layout, flag, "zone"); err == nil || !strings.Contains(err.Error(), "official outward") {
		t.Fatalf("reversed pin marker accepted: %v", err)
	}
}

func TestMarkerAnchorWireTreeRequiresOnePhysicalIsland(t *testing.T) {
	layout := &SchematicLayoutResult{
		Placements: []powerLayoutPlacement{{Designator: "A", BBox: layoutBBox{MinX: -30, MinY: -10, MaxX: -20, MaxY: 10}, Pins: []powerLayoutPin{{Number: "1", Net: "SIG", X: -20, Y: 0}}}},
		Wires:      []powerLayoutWire{{Net: "SIG", Points: [][2]float64{{-20, 0}, {20, 0}}}},
	}
	flag := powerLayoutFlag{Net: "SIG", Kind: "net_port_bi", PinX: 0, PinY: 0, Direction: "up", Offset: 20}
	anchor, err := inferredSchematicMarkerAnchor(layout, flag, "zone")
	if err != nil || anchor.Type != "wire_tree" || anchor.X == nil || anchor.Y == nil || *anchor.X != 0 || *anchor.Y != 0 {
		t.Fatalf("wire-tree anchor was not inferred: %+v %v", anchor, err)
	}
	layout.Placements = append(layout.Placements, powerLayoutPlacement{Designator: "B", BBox: layoutBBox{MinX: -10, MinY: -30, MaxX: 10, MaxY: -20}, Pins: []powerLayoutPin{{Number: "1", Net: "SIG", X: 0, Y: -20}}})
	layout.Wires = append(layout.Wires, powerLayoutWire{Net: "SIG", Points: [][2]float64{{0, -20}, {0, 20}}})
	if _, err := inferredSchematicMarkerAnchor(layout, flag, "zone"); err == nil || !strings.Contains(err.Error(), "found 2") {
		t.Fatalf("ambiguous strict-X islands accepted: %v", err)
	}
}

package app

import (
	"errors"
	"testing"
)

// Regression distilled from the P1 USB Type-C zone.  A vertical CC resistor
// owns a measured Designator at the corner of the existing USB_DP line tree.
// The two USB_DP islands still have a short legal route around the resistor;
// the router must not exhaust a zone budget or depend on component array order.
func TestMazeRouteAroundMeasuredDesignatorCorner(t *testing.T) {
	rotationRight, rotationLeft := 0.0, 180.0
	connector := powerLayoutPlacement{Designator: "USBC1", BBox: layoutBBox{-25.5, -65.5, 25.5, 65.5}, TextBBoxes: []layoutBBox{{-25, 65, -1.191925048828125, 73}}, Pins: []powerLayoutPin{
		{Number: "A4B9", Net: "USB5V", X: 35, Y: -45, Rotation: &rotationRight},
		{Number: "A5", Net: "CC1", X: 35, Y: -25, Rotation: &rotationRight},
		{Number: "B7", Net: "USB_DM", X: 35, Y: -15, Rotation: &rotationRight},
		{Number: "A6", Net: "USB_DP", X: 35, Y: -5, Rotation: &rotationRight},
		{Number: "A7", Net: "USB_DM", X: 35, Y: 5, Rotation: &rotationRight},
		{Number: "B6", Net: "USB_DP", X: 35, Y: 15, Rotation: &rotationRight},
		{Number: "B5", Net: "CC2", X: 35, Y: 35, Rotation: &rotationRight},
		{Number: "B4A9", Net: "USB5V", X: 35, Y: 45, Rotation: &rotationRight},
	}}
	protection := powerLayoutPlacement{Designator: "D1", BBox: layoutBBox{99.5, -35.5, 170.5, 5.5}, TextBBoxes: []layoutBBox{{100, 5, 109.12718200683594, 13}}, Pins: []powerLayoutPin{
		{Number: "1", Net: "USB_DP", X: 90, Y: -5, Rotation: &rotationLeft},
		{Number: "2", Net: "GND", X: 90, Y: -15, Rotation: &rotationLeft},
		{Number: "3", Net: "USB_DM", X: 90, Y: -25, Rotation: &rotationLeft},
		{Number: "4", Net: "USB_DM", X: 180, Y: -25, Rotation: &rotationRight},
		{Number: "5", Net: "USB5V", X: 180, Y: -15, Rotation: &rotationRight},
		{Number: "6", Net: "USB_DP", X: 180, Y: -5, Rotation: &rotationRight},
	}}
	resistor := powerLayoutPlacement{Designator: "R4", X: 60, Y: 15, BBox: layoutBBox{55.5, 4.5, 64.5, 25.5}, TextBBoxes: []layoutBBox{{70, 15, 79.12718200683594, 23}}, Pins: []powerLayoutPin{
		{Number: "2", Net: "GND", X: 60, Y: -5},
		{Number: "1", Net: "CC2", X: 60, Y: 35},
	}}
	resistorCC1 := powerLayoutPlacement{Designator: "R3", X: 65, Y: -45, BBox: layoutBBox{60.5, -55.5, 69.5, -34.5}, TextBBoxes: []layoutBBox{{75, -45, 84.12718200683594, -37}}, Pins: []powerLayoutPin{
		{Number: "2", Net: "GND", X: 65, Y: -65},
		{Number: "1", Net: "CC1", X: 65, Y: -25},
	}}
	decoupling := powerLayoutPlacement{Designator: "C8", X: 205, Y: -35, BBox: layoutBBox{196.5, -45.5, 213.5, -24.5}, TextBBoxes: []layoutBBox{{220, -35, 229.12718200683594, -27}}, Pins: []powerLayoutPin{
		{Number: "2", Net: "GND", X: 205, Y: -55},
		{Number: "1", Net: "USB5V", X: 205, Y: -15},
	}}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{connector, resistor, resistorCC1, protection, decoupling}, Wires: []powerLayoutWire{
		{Net: "USB5V", Points: [][2]float64{{45, -45}, {35, -45}}},
		{Net: "USB_DM", Points: [][2]float64{{35, 5}, {55, 5}}},
		{Net: "USB_DM", Points: [][2]float64{{55, 5}, {55, -15}}},
		{Net: "USB_DM", Points: [][2]float64{{55, -15}, {35, -15}}},
		{Net: "USB_DP", Points: [][2]float64{{35, 15}, {50, 15}}},
		{Net: "USB_DP", Points: [][2]float64{{50, 15}, {50, -5}}},
		{Net: "USB_DP", Points: [][2]float64{{50, -5}, {35, -5}}},
		{Net: "USB5V", Points: [][2]float64{{205, -10}, {185, -10}}},
		{Net: "USB5V", Points: [][2]float64{{185, -10}, {185, -15}}},
		{Net: "USB5V", Points: [][2]float64{{185, -15}, {180, -15}}},
		{Net: "USB5V", Points: [][2]float64{{205, -15}, {205, -10}}},
		{Net: "USB5V", Points: [][2]float64{{205, -10}, {205, 45}}},
		{Net: "USB5V", Points: [][2]float64{{35, 45}, {45, 45}}},
		{Net: "USB5V", Points: [][2]float64{{45, 45}, {205, 45}}},
		{Net: "USB5V", Points: [][2]float64{{45, -45}, {45, 45}}},
		{Net: "USB_DP", Points: [][2]float64{{180, -5}, {200, -5}}},
		{Net: "USB_DP", Points: [][2]float64{{200, -5}, {200, 15}}},
		{Net: "USB_DP", Points: [][2]float64{{200, 15}, {70, 15}}},
		{Net: "USB_DP", Points: [][2]float64{{70, 15}, {70, -5}}},
		{Net: "USB_DP", Points: [][2]float64{{70, -5}, {90, -5}}},
	}}
	// The historical fast path ran along the R4 Designator's left and bottom
	// boundaries.  A rendered text obstacle is closed geometry, so this exact
	// pre-fix tree must be rejected before it can poison later routing.
	if err := validateLibGeometry(&p); !isWireTextObstruction(err) {
		t.Fatalf("Designator boundary contact was accepted: %v", err)
	}
	if err := validateLibRoutingEdge(&p, "USB_DP", [2]float64{200, 15}, [2]float64{70, 15}); !isWireTextObstruction(err) {
		t.Fatalf("A* edge check disagrees with final Designator gate: %v", err)
	}
	legal := p
	legal.Wires = append([]powerLayoutWire(nil), p.Wires[:len(p.Wires)-5]...)
	legal.Wires = libAppendRoute(legal.Wires, libPointsRoute("USB_DP",
		[2]float64{180, -5}, [2]float64{190, -5}, [2]float64{190, -75},
		[2]float64{70, -75}, [2]float64{70, -5}, [2]float64{90, -5}))
	if err := validateLibGeometry(&legal); err != nil {
		t.Fatalf("corrected target tree is invalid: %v", err)
	}
	p = legal
	var islands []libIsland
	for _, island := range libIslands(&p) {
		if island.net == "USB_DP" {
			islands = append(islands, island)
		}
	}
	if len(islands) != 2 {
		t.Fatalf("fixture must expose two USB_DP islands, got %d", len(islands))
	}
	probe := p
	probe.Wires = libAppendRoute(probe.Wires, libPointsRoute("USB_DP", [2]float64{50, -5}, [2]float64{50, -75}, [2]float64{70, -75}))
	if err := validateLibGeometry(&probe); err != nil {
		t.Fatalf("fixture's known short detour is invalid: %v", err)
	}
	for _, order := range [][]powerLayoutPlacement{
		{connector, resistor, resistorCC1, protection, decoupling},
		{decoupling, protection, resistorCC1, resistor, connector},
	} {
		trial := p
		trial.Placements = order
		routing := &schematicRoutingContext{options: SchematicRoutingOptions{MaxExpandedNodes: 200000, MaxReroutes: 4}, components: map[string]string{}, netPins: map[string]int{"USB_DP": 4}, policies: map[string]string{"USB_DP": "direct"}, rejections: map[string]*SchematicRoutingRejection{}, cache: map[string]schematicMazeCacheEntry{}, templates: map[string][][]powerLayoutWire{}}
		route, err := libMazeRoute(&trial, islands[0], islands[1], routing)
		if err != nil {
			t.Fatalf("order %s..%s: %v", order[0].Designator, order[len(order)-1].Designator, err)
		}
		trial.Wires = libAppendRoute(trial.Wires, route)
		if err := validateLibGeometry(&trial); err != nil || !libPinsShareIsland(&trial, connector.Pins[3], protection.Pins[0]) {
			t.Fatalf("order %s..%s: invalid merged route: %v", order[0].Designator, order[len(order)-1].Designator, err)
		}
		if routing.expanded > 1000 {
			t.Fatalf("short corner detour expanded %d nodes", routing.expanded)
		}
	}

}

func isWireTextObstruction(err error) bool {
	var obstruction *schGeometryObstruction
	return errors.As(err, &obstruction) && obstruction.kind == "wire-text"
}

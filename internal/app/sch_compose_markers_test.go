package app

import (
	"strings"
	"testing"
)

func TestComposeRejectsElectricallySeparateButOverlappingLabels(t *testing.T) {
	p := &powerLayoutPlan{Flags: []powerLayoutFlag{
		{Net: "UART_TX", Kind: "net_port_bi", PinX: 100, PinY: 0, Direction: "left", Offset: 10},
		{Net: "UART_RX", Kind: "net_port_bi", PinX: 110, PinY: 5, Direction: "left", Offset: 10},
	}}
	if _, err := compositionMarkerGeometry(p); err == nil {
		t.Fatal("overlapping label names escaped the local data gate")
	}
	p.Flags[1].PinY = 20
	if _, err := compositionMarkerGeometry(p); err != nil {
		t.Fatalf("clear stagger rejected: %v", err)
	}
	p.Flags[1] = p.Flags[0]
	if _, err := compositionMarkerGeometry(p); err == nil {
		t.Fatal("duplicate coincident marker escaped local gate")
	}
}

func TestComposeMarkerTextCannotOccupyPartBody(t *testing.T) {
	p := &powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "J1", BBox: layoutBBox{50, -10, 80, 10}}}, Flags: []powerLayoutFlag{{Net: "UART_TX", Kind: "net_port_bi", PinX: 0, PinY: 0, Direction: "right", Offset: 10}}}
	// The marker hexagon ends at 50.5; its external text continues into J1.
	if _, err := compositionMarkerGeometry(p); err == nil {
		t.Fatal("label's external name may not overlap a part")
	}
}

func TestComposeWireMarkerInteriors(t *testing.T) {
	marker := powerLayoutFlag{Net: "RF_IO0", Kind: "net_port_bi", PinX: 100, PinY: 0, Direction: "left", Offset: 10}
	body := predictedMarkerBody(90, 0, marker.Kind, marker.Direction, marker.Net)
	for _, tc := range []struct {
		name   string
		points [][2]float64
		bad    bool
	}{
		{"cross body", [][2]float64{{body.MinX + 5, -20}, {body.MinX + 5, 20}}, true},
		{"cross external text", [][2]float64{{body.MinX - 5, -20}, {body.MinX - 5, 20}}, true},
		{"bent wire envelope only", [][2]float64{{0, -20}, {120, -20}, {120, 20}}, false},
		{"foreign wire ending at anchor", [][2]float64{{110, 0}, {90, 0}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &powerLayoutPlan{Flags: []powerLayoutFlag{marker}, Wires: []powerLayoutWire{{Net: "RXD", Points: tc.points}}}
			_, err := compositionMarkerGeometry(p)
			if tc.bad && (err == nil || !strings.Contains(err.Error(), "wire-marker overlap")) {
				t.Fatalf("expected local wire-marker rejection: %v", err)
			}
			if !tc.bad && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestComposeRejectsForeignWireAlongMarkerStroke(t *testing.T) {
	// Reduced from the live C7 GND stub beside the DTR port. The wire centreline
	// lies on the marker edge, so centreline-vs-shrunk-box misses it; the visible
	// 0.5-raw strokes have positive-area overlap and must fail.
	p := &powerLayoutPlan{
		Flags: []powerLayoutFlag{{
			Net: "DTR", Kind: "net_port_bi", PinX: 50, PinY: -15,
			Direction: "right", Offset: 15,
		}},
		Wires: []powerLayoutWire{{
			Net:    "GND",
			Points: [][2]float64{{65, -10}, {75, -10}, {75, -20}},
		}},
	}
	if _, err := compositionMarkerGeometry(p); err == nil || !strings.Contains(err.Error(), "wire-marker overlap") {
		t.Fatalf("foreign edge-collinear wire escaped visible-stroke gate: %v", err)
	}
}

func TestComposeOwnMarkerLeadMayTerminateAtItsAnchor(t *testing.T) {
	for _, direction := range []string{"left", "right", "up", "down"} {
		p := &powerLayoutPlan{Flags: []powerLayoutFlag{{
			Net: "DTR", Kind: "net_port_bi", PinX: 0, PinY: 0,
			Direction: direction, Offset: 20,
		}}}
		if _, err := compositionMarkerGeometry(p); err != nil {
			t.Fatalf("own %s lead must be allowed to terminate at its exact anchor: %v", direction, err)
		}
	}
}

func TestComposeMarkerLeadCrossesAnotherName(t *testing.T) {
	p := &powerLayoutPlan{Flags: []powerLayoutFlag{
		{Net: "RF_IO0", Kind: "net_port_bi", PinX: 100, PinY: 0, Direction: "left", Offset: 10},
		{Net: "RXD", Kind: "net_port_bi", PinX: 0, PinY: 0, Direction: "right", Offset: 140},
	}}
	if _, err := compositionMarkerGeometry(p); err == nil || !strings.Contains(err.Error(), "wire-marker overlap") {
		t.Fatalf("generated marker lead escaped local overlap check: %v", err)
	}
}

func TestComposeNormalPowerGroundLeads(t *testing.T) {
	for _, kind := range []string{"power", "ground", "net_port_bi"} {
		for _, dir := range []string{"left", "right", "up", "down"} {
			net := map[string]string{"power": "+3V3", "ground": "GND", "net_port_bi": "UART_RX"}[kind]
			p := &powerLayoutPlan{Flags: []powerLayoutFlag{{Net: net, Kind: kind, Direction: dir, Offset: 20}}}
			if _, err := compositionMarkerGeometry(p); err != nil {
				t.Fatalf("%s/%s: %v", kind, dir, err)
			}
		}
	}
}

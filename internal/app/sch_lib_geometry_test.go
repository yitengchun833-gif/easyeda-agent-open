package app

import (
	"strings"
	"testing"
)

func libGeometryStemFixture() *powerLayoutPlan {
	return &powerLayoutPlan{Placements: []powerLayoutPlacement{{
		Designator: "U1", BBox: layoutBBox{-20, -20, 20, 20},
		Pins: []powerLayoutPin{{Number: "3", X: 100, Y: 5}},
	}}}
}

func TestLibGeometryRejectsPinStemCollisions(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*powerLayoutPlan)
		want string
	}{
		{"NC stem through peripheral", func(p *powerLayoutPlan) {
			p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "R1", X: 70, BBox: layoutBBox{60, -10, 80, 10}})
		}, "pin stem U1.3 crosses R1 body"},
		{"foreign wire through stem", func(p *powerLayoutPlan) {
			p.Wires = []powerLayoutWire{{Net: "B", Points: [][2]float64{{60, -10}, {60, 20}}}}
		}, "wire/marker lead B touches NC/foreign pin stem"},
		{"foreign marker lead through stem", func(p *powerLayoutPlan) {
			p.Flags = []powerLayoutFlag{{Net: "B", Kind: "power", PinX: 60, PinY: -20, Direction: "up", Offset: 40}}
		}, "wire/marker lead B touches NC/foreign pin stem"},
		{"foreign stem through stem", func(p *powerLayoutPlan) {
			p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "U2", X: 60, Y: -30, BBox: layoutBBox{50, -40, 70, -20}, Pins: []powerLayoutPin{{Number: "1", Net: "B", X: 60, Y: 20}}})
		}, "NC/foreign pin stems intersect"},
		{"marker body through stem", func(p *powerLayoutPlan) {
			p.Flags = []powerLayoutFlag{{Net: "B", Kind: "net_port_bi", PinX: 110, PinY: 0, Direction: "left", Offset: 10}}
		}, "marker B body/text crosses pin stem"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := libGeometryStemFixture()
			tc.edit(p)
			if err := validateLibGeometry(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestLibGeometryStemSameNetTouchAndHalo(t *testing.T) {
	p := libGeometryStemFixture()
	p.Placements[0].Pins[0].Net = "A"
	p.Wires = []powerLayoutWire{{Net: "A", Points: [][2]float64{{60, -10}, {60, 20}}}}
	if err := validateLibGeometry(p); err != nil {
		t.Fatalf("same-net branch rejected: %v", err)
	}
	p = &powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "J1", BBox: layoutBBox{-20.5, -20.5, 20.5, 20.5}, Pins: []powerLayoutPin{{Number: "1", X: -20, Y: 0}}}}}
	stems, err := libMeasuredStems(p)
	if err != nil || len(stems) != 0 {
		t.Fatalf("stroke halo must not invent an inward/long stem: %v %+v", err, stems)
	}
	p.Placements[0].Pins[0].X, p.Placements[0].Pins[0].Y = 30, 30
	if _, err := libMeasuredStems(p); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("diagonal inferred from corner pin: %v", err)
	}
}

func TestLibGeometryMarkerExternalTextAvoidsStem(t *testing.T) {
	f := powerLayoutFlag{Net: "RF_IO0", Kind: "net_port_bi", PinX: 100, PinY: 0, Direction: "left", Offset: 10}
	boxes := schTerminalMarkerBoxes(f)
	if len(boxes) != 2 {
		t.Fatal("fixture needs an external marker text band")
	}
	x := plFloor((boxes[1].MinX + boxes[1].MaxX) / 2)
	p := &powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "J1", X: x, Y: -60,
		BBox: layoutBBox{x - 5, -70, x + 5, -50}, Pins: []powerLayoutPin{{Number: "1", X: x, Y: 50}},
	}}, Flags: []powerLayoutFlag{f}}
	if plSegmentBox([2]float64{x, -50}, [2]float64{x, 50}, boxes[0]) {
		t.Fatal("fixture must intersect external text only")
	}
	if err := validateLibGeometry(p); err == nil || !strings.Contains(err.Error(), "marker RF_IO0 body/text crosses pin stem") {
		t.Fatalf("external marker text ignored: %v", err)
	}
}

func TestLibGeometryReservesOtherComponentLabelSpace(t *testing.T) {
	p := &powerLayoutPlan{Placements: []powerLayoutPlacement{
		{Designator: "U123456", Value: "short", BBox: layoutBBox{-20, -20, 20, 20}},
		{Designator: "R1", X: 55, BBox: layoutBBox{45, -10, 65, 10}},
	}}
	if err := validatePowerLayout(p, layoutBBox{-1000, -1000, 1000, 1000}); err != nil {
		t.Fatalf("fixture must have separated component bodies: %v", err)
	}
	if err := validateLibGeometry(p); err == nil || !strings.Contains(err.Error(), "label reservation") {
		t.Fatalf("missing conservative label reservation: %v", err)
	}
}

func TestLibGeometryExcludesNonDesignatorValueFromReservation(t *testing.T) {
	base := powerLayoutPlacement{Designator: "U1", BBox: layoutBBox{-20, -20, 20, 20}}
	long := base
	long.Value = "S8050 J3Y(RANGE:200-350) / SUPPLIER-DESCRIPTION-THAT-CROSSES-THE-PAGE"
	if got, want := libPartLabelReservation(long), libPartLabelReservation(base); got != want {
		t.Fatalf("non-designator Value changed reservation: got %+v want %+v", got, want)
	}
	p := &powerLayoutPlan{Placements: []powerLayoutPlacement{
		long,
		{Designator: "R1", X: 80, BBox: layoutBBox{70, -10, 90, 10}},
	}}
	if err := validateLibGeometry(p); err != nil {
		t.Fatalf("non-designator Value became a collision obstacle: %v", err)
	}
}

// Sanitized real measured POWER geometry from the Hongen four-part example.
// It retains AMS1117's two physical VOUT pins and vertically measured shunts.
func libGeometryRealPowerFixture() *powerLayoutPlan {
	cap := func(ref, value, net string, x, y float64) powerLayoutPlacement {
		return powerLayoutPlacement{Designator: ref, Value: value, X: x, Y: y, Rotation: 270,
			BBox: layoutBBox{x - 8.5, y - 10.5, x + 8.5, y + 10.5},
			Pins: []powerLayoutPin{{Number: "1", Net: net, X: x, Y: y + 20}, {Number: "2", Net: "GND", X: x, Y: y - 20}}}
	}
	return &powerLayoutPlan{
		Placements: []powerLayoutPlacement{
			{Designator: "U1", X: 300, Y: 650, Rotation: 180, Mirror: true, BBox: layoutBBox{264.5, 629.5, 335.5, 670.5}, Pins: []powerLayoutPin{
				{Number: "1", Net: "GND", X: 255, Y: 640}, {Number: "2", Net: "+3V3", X: 255, Y: 650},
				{Number: "3", Net: "+5V", X: 255, Y: 660}, {Number: "4", Net: "+3V3", X: 345, Y: 650},
			}}, cap("C2", "10uF", "+5V", 145, 640), cap("C1", "100nF", "+3V3", 405, 630), cap("C3", "22uF", "+3V3", 490, 630),
		},
		Wires: []powerLayoutWire{
			{Net: "+5V", Points: [][2]float64{{145, 660}, {255, 660}}},
			{Net: "+3V3", Points: [][2]float64{{345, 650}, {405, 650}}},
			{Net: "+3V3", Points: [][2]float64{{405, 650}, {490, 650}}},
			{Net: "GND", Points: [][2]float64{{255, 640}, {235, 640}}},
			{Net: "+3V3", Points: [][2]float64{{255, 650}, {210, 650}}},
		},
		Flags: []powerLayoutFlag{
			{Net: "+5V", Kind: "power", PinX: 145, PinY: 660, Direction: "up", Offset: 30},
			{Net: "+3V3", Kind: "power", PinX: 490, PinY: 650, Direction: "up", Offset: 30},
			{Net: "+3V3", Kind: "power", PinX: 210, PinY: 650, Direction: "down", Offset: 20},
			{Net: "GND", Kind: "ground", PinX: 235, PinY: 640, Direction: "down", Offset: 30},
			{Net: "GND", Kind: "ground", PinX: 145, PinY: 620, Direction: "down", Offset: 30},
			{Net: "GND", Kind: "ground", PinX: 405, PinY: 610, Direction: "down", Offset: 30},
			{Net: "GND", Kind: "ground", PinX: 490, PinY: 610, Direction: "down", Offset: 30},
		},
	}
}

func TestLibGeometryAcceptsRealAMS1117VerticalShunts(t *testing.T) {
	p := libGeometryRealPowerFixture()
	if err := validateLibGeometry(p); err == nil || !strings.Contains(err.Error(), "pin-exit-direction") {
		t.Fatalf("historical sideways capacitor fanout must now fail: %v", err)
	}
	// Preserve the measured placement; repair source wires to outward stubs
	// and a rail one grid step above the capacitor endpoints.
	p.Wires = append(p.Wires[3:], []powerLayoutWire{
		{Net: "+5V", Points: [][2]float64{{145, 660}, {145, 665}}}, {Net: "+5V", Points: [][2]float64{{145, 665}, {250, 665}}}, {Net: "+5V", Points: [][2]float64{{250, 665}, {250, 660}}}, {Net: "+5V", Points: [][2]float64{{250, 660}, {255, 660}}},
		{Net: "+3V3", Points: [][2]float64{{345, 650}, {350, 650}}}, {Net: "+3V3", Points: [][2]float64{{350, 650}, {350, 655}}}, {Net: "+3V3", Points: [][2]float64{{350, 655}, {490, 655}}}, {Net: "+3V3", Points: [][2]float64{{405, 655}, {405, 650}}}, {Net: "+3V3", Points: [][2]float64{{490, 655}, {490, 650}}},
	}...)
	p.Flags[0].PinY += 5
	p.Flags[1].PinY += 5
	if err := validateLibGeometry(p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(p); err != nil {
		t.Fatal(err)
	}
	translatePowerLayout(p, -125, 50)
	if err := validateLibGeometry(p); err != nil {
		t.Fatalf("translation changed validation: %v", err)
	}
}

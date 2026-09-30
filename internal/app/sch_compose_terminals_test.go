package app

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func terminalFixture(direction string, nets ...string) (*powerLayoutPlan, []schCompositionTerminal) {
	c := powerLayoutPlacement{Designator: "U_RF", BBox: layoutBBox{-40, -40, 40, 40}}
	var terminals []schCompositionTerminal
	for i, net := range nets {
		q := powerLayoutPin{Number: string(rune('1' + i)), Net: net}
		switch direction {
		case "left":
			q.X, q.Y = -50, float64(i*10)
		case "right":
			q.X, q.Y = 50, float64(i*10)
		case "up":
			q.X, q.Y = float64(i*10), 50
		case "down":
			q.X, q.Y = float64(i*10), -50
		}
		c.Pins = append(c.Pins, q)
		terminals = append(terminals, schCompositionTerminal{Designator: c.Designator, Pin: q.Number, Direction: direction})
	}
	return &powerLayoutPlan{Placements: []powerLayoutPlacement{c}}, terminals
}

func TestComposeTerminalsStraightAndStaggeredAllDirections(t *testing.T) {
	for _, direction := range []string{"left", "right", "up", "down"} {
		t.Run(direction, func(t *testing.T) {
			p, terminals := terminalFixture(direction, "A", "B", "C")
			if err := planSchCompositionTerminals(p, terminals); err != nil {
				t.Fatal(err)
			}
			if len(p.Flags) != 3 || p.Flags[0].Offset != 10 || p.Flags[1].Offset != 60 || p.Flags[2].Offset != 10 {
				t.Fatalf("want shortest alternating 10/60/10 stubs for 10-spaced pins: %+v", p.Flags)
			}
			for i, f := range p.Flags {
				pin := p.Placements[0].Pins[i]
				if f.PinX != pin.X || f.PinY != pin.Y || f.Net != pin.Net || f.Kind != "net_port_bi" || f.Direction != direction {
					t.Fatalf("terminal must use exact measured pin/net: %+v", f)
				}
			}
			if _, err := compositionMarkerGeometry(p); err != nil {
				t.Fatalf("adjacent leads may pass alongside names: %v", err)
			}
			if err := validatePowerLayout(p, layoutBBox{-1000, -1000, 1000, 1000}); err != nil {
				t.Fatal(err)
			}
			if err := validateSchCompositionNets(p); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestComposeTerminalsLongNameAndPowerKinds(t *testing.T) {
	p, terminals := terminalFixture("left", strings.Repeat("N", 30), "RX")
	if err := planSchCompositionTerminals(p, terminals); err != nil {
		t.Fatal(err)
	}
	if p.Flags[0].Offset != 10 || p.Flags[1].Offset <= 200 {
		t.Fatalf("long first name must increase its neighbor's straight reach: %+v", p.Flags)
	}
	for _, kind := range []string{"net_port_in", "net_port_out", "net_port_bi", "power", "ground"} {
		for _, direction := range []string{"left", "right", "up", "down"} {
			t.Run(kind+"/"+direction, func(t *testing.T) {
				p, terminals := terminalFixture(direction, "PWR")
				terminals[0].Kind = kind
				if err := planSchCompositionTerminals(p, terminals); err != nil {
					t.Fatal(err)
				}
				if p.Flags[0].Kind != kind {
					t.Fatalf("kind changed: %+v", p.Flags)
				}
			})
		}
	}
}

func TestComposeTerminalsReachLimitIsAtomic(t *testing.T) {
	p, terminals := terminalFixture("right", strings.Repeat("N", 60), "RX")
	before, _ := json.Marshal(p)
	err := planSchCompositionTerminals(p, terminals)
	if err == nil || !strings.Contains(err.Error(), "U_RF.2 has no legal straight right stub at 10..300 raw") {
		t.Fatalf("expected bounded failure after planning the first terminal: %v", err)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("failed second terminal must not retain the first generated flag")
	}
}

func TestComposeTerminalsRejectBadIntentAndExistingConnections(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*powerLayoutPlan, *[]schCompositionTerminal)
		want string
	}{
		{"unknown ref", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Designator = "u_rf" }, "unknown"},
		{"unknown pin", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Pin = "01" }, "unknown"},
		{"NC", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { p.Placements[0].Pins[0].Net = "" }, "NC/unknown"},
		{"duplicate", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { *ts = append(*ts, (*ts)[0]) }, "duplicated"},
		{"inward", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Direction = "right" }, "not outward"},
		{"tangent", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Direction = "up" }, "not outward"},
		{"unknown direction", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Direction = "northwest" }, "not outward"},
		{"unknown kind", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) { (*ts)[0].Kind = "label" }, "unsupported kind"},
		{"wire middle", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) {
			p.Wires = []powerLayoutWire{{Net: "A", Points: [][2]float64{{-50, -10}, {-50, 10}}}}
		}, "already touches"},
		{"marker lead", func(p *powerLayoutPlan, ts *[]schCompositionTerminal) {
			p.Flags = []powerLayoutFlag{{Net: "A", PinX: -45, PinY: 0, Direction: "left", Offset: 10, Kind: "net_port_bi"}}
		}, "already touches"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, terminals := terminalFixture("left", "A")
			tc.edit(p, &terminals)
			before, _ := json.Marshal(p)
			err := planSchCompositionTerminals(p, terminals)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			after, _ := json.Marshal(p)
			if string(before) != string(after) {
				t.Fatal("failed planning must not change even the flags")
			}
		})
	}
}

func TestComposeTerminalsAvoidObstacleWithoutDogleg(t *testing.T) {
	p, terminals := terminalFixture("left", "TX")
	// Off-axis body overlaps the first label, but leaves the y=0 lead clear.
	p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "R1", X: -100, Y: 15, BBox: layoutBBox{-110, 5, -90, 25}})
	if err := planSchCompositionTerminals(p, terminals); err != nil {
		t.Fatal(err)
	}
	if p.Flags[0].Offset <= 10 || len(p.Wires) != 0 || p.Flags[0].PinY != 0 {
		t.Fatalf("must lengthen only, without a dogleg: %+v", p.Flags)
	}
	if _, err := compositionMarkerGeometry(p); err != nil {
		t.Fatal(err)
	}
}

func TestComposeTerminalsNoStraightSolution(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*powerLayoutPlan)
	}{
		{"body", func(p *powerLayoutPlan) {
			p.Placements = append(p.Placements, powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-70, -10, -55, 10}})
		}},
		{"foreign wire endpoint", func(p *powerLayoutPlan) {
			// A T contact blocks; a bare interior X is not an electrical short.
			p.Wires = []powerLayoutWire{{Net: "B", Points: [][2]float64{{-55, 0}, {-55, 10}}}}
		}},
		{"foreign pin", func(p *powerLayoutPlan) {
			p.Placements[0].Pins = append(p.Placements[0].Pins, powerLayoutPin{Number: "2", Net: "B", X: -55, Y: 0})
		}},
		{"NC pin", func(p *powerLayoutPlan) {
			p.Placements[0].Pins = append(p.Placements[0].Pins, powerLayoutPin{Number: "2", X: -55, Y: 0})
		}},
		{"existing marker text", func(p *powerLayoutPlan) {
			p.Flags = []powerLayoutFlag{{Net: "B", Kind: "net_port_bi", PinX: -55, PinY: 70, Direction: "down", Offset: 20}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, terminals := terminalFixture("left", "A")
			tc.edit(p)
			before, _ := json.Marshal(p)
			err := planSchCompositionTerminals(p, terminals)
			if err == nil || !strings.Contains(err.Error(), "no legal straight") {
				t.Fatalf("want explicit no-straight-solution error, got %v", err)
			}
			after, _ := json.Marshal(p)
			if string(before) != string(after) {
				t.Fatal("failure changed source")
			}
		})
	}
}

func TestComposeTerminalsPreserveSourceAndAreDeterministic(t *testing.T) {
	p, terminals := terminalFixture("right", "TX", "RX")
	p.Wires = []powerLayoutWire{{Net: "OTHER", Points: [][2]float64{{-100, -100}, {-90, -100}}}}
	p.Flags = []powerLayoutFlag{{Net: "EXISTING", Kind: "net_port_bi", PinX: -300, PinY: -300, Direction: "left", Offset: 10}}
	raw, _ := json.Marshal(p)
	var same powerLayoutPlan
	_ = json.Unmarshal(raw, &same)
	if err := planSchCompositionTerminals(p, terminals); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Placements, same.Placements) || !reflect.DeepEqual(p.Wires, same.Wires) || !reflect.DeepEqual(p.Flags[:1], same.Flags) {
		t.Fatal("planning changed measured parts/pins, existing wires or markers")
	}
	if err := planSchCompositionTerminals(&same, terminals); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*p, same) {
		t.Fatal("same measured data and intent must produce exactly the same plan")
	}
}

func TestComposeTerminalDirectionHaloDoesNotPermitBodyCrossing(t *testing.T) {
	p, terminals := terminalFixture("left", "TX")
	p.Placements[0].Pins[0].X = -40
	p.Placements[0].BBox.MinX = -40.25
	if !schTerminalPointsOutward(p.Placements[0].Pins[0], p.Placements[0].BBox, "left") {
		t.Fatal("nearest measured edge within 0.5 should classify as outward")
	}
	if err := planSchCompositionTerminals(p, terminals); err == nil || !strings.Contains(err.Error(), "lead crosses U_RF body") {
		t.Fatalf("direction tolerance must not bypass strict body collision: %v", err)
	}
}

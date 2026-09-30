package app

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func powerLayoutFixture(t *testing.T, shiftX, shiftY, capSpan float64, turns int) map[string]any {
	t.Helper()
	core := powerLayoutPlacement{PrimitiveID: "core", Designator: "U1", X: 250, Y: 250, Rotation: 180, Mirror: true,
		BBox: layoutBBox{MinX: 214.5, MinY: 229.5, MaxX: 285.5, MaxY: 270.5}, Pins: []powerLayoutPin{
			{Number: "1", Name: "GND", Net: "GND", X: 205, Y: 240},
			{Number: "2", Name: "VOUT", Net: "+3V3", X: 205, Y: 250},
			{Number: "3", Name: "VIN", Net: "+5V", X: 205, Y: 260},
			{Number: "4", Name: "VOUT", Net: "+3V3", X: 295, Y: 250},
		}}
	parts := []powerLayoutPlacement{core}
	for _, ref := range []string{"C1", "C2", "C3"} {
		net := "+3V3"
		if ref == "C2" {
			net = "+5V"
		}
		c := powerLayoutPlacement{PrimitiveID: "id-" + ref, Designator: ref, X: 400, Y: 300, Rotation: 0, BBox: layoutBBox{MinX: 389.5, MinY: 291.5, MaxX: 410.5, MaxY: 308.5}, Pins: []powerLayoutPin{
			{Number: "1", Name: "1", Net: net, X: 400 - capSpan, Y: 300},
			{Number: "2", Name: "2", Net: "GND", X: 400 + capSpan, Y: 300},
		}}
		parts = append(parts, plRotate(c, turns))
	}
	comps := []any{map[string]any{"componentType": "sheet", "bbox": map[string]float64{"minX": 0, "minY": 0, "maxX": 1170, "maxY": 825}}}
	for _, c := range parts {
		c = plTranslate(c, shiftX, shiftY)
		pins := []any{}
		for _, p := range c.Pins {
			pins = append(pins, map[string]any{"pinNumber": p.Number, "pinName": p.Name, "net": p.Net, "x": p.X, "y": p.Y})
		}
		value := map[string]string{"C1": "100nF", "C2": "10uF", "C3": "22uF"}[c.Designator]
		comps = append(comps, map[string]any{"primitiveId": c.PrimitiveID, "designator": c.Designator, "componentType": "part", "x": c.X, "y": c.Y, "rotation": c.Rotation, "mirror": c.Mirror, "bbox": c.BBox, "pins": pins, "pinsAvailable": true, "otherProperty": map[string]any{"Value": value}})
	}
	return map[string]any{"context": map[string]any{"documentUuid": "power-doc"}, "result": map[string]any{"components": comps}}
}

func powerLayoutBytes(t *testing.T, fixture map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func powerLayoutTestOptions() powerLayoutOptions {
	at := [2]float64{300, 650}
	return powerLayoutOptions{Core: "U1", InputCap: "C2", OutputCaps: []string{"C1", "C3"}, At: &at}
}
func powerLayoutTestComp(f map[string]any, i int) map[string]any {
	return f["result"].(map[string]any)["components"].([]any)[i].(map[string]any)
}

// Exercise different measured baselines: every symbol rotation, three pin
// spans and translated input scenes. Assertions describe the circuit drawing,
// not fixed implementation coordinates or template strings.
func TestPowerLayoutMeasuredGeometryCorpus(t *testing.T) {
	for turns := 0; turns < 4; turns++ {
		for _, span := range []float64{20, 25, 30} {
			fixture := powerLayoutFixture(t, float64(turns)*15, span*2, span, turns)
			raw := powerLayoutBytes(t, fixture)
			plan, err := planPowerLayout(raw, powerLayoutTestOptions())
			if err != nil {
				t.Fatalf("turns=%d span=%v: %v", turns, span, err)
			}
			core, input, first, second := plan.Placements[0], plan.Placements[1], plan.Placements[2], plan.Placements[3]
			if core.X != 300 || core.Y != 650 || !core.Mirror || core.Rotation != 180 {
				t.Fatalf("lost measured core orientation: %+v", core)
			}
			if input.BBox.MaxX >= core.BBox.MinX || first.BBox.MinX <= core.BBox.MaxX || second.X <= first.X {
				t.Fatal("expected left input, core, parallel outputs on the right")
			}
			for _, c := range plan.Placements[1:] {
				a, b := plPin(c, "1"), plPin(c, "2")
				if a.X != b.X || a.Y <= b.Y || math.Abs(a.Y-b.Y-2*span) > 1e-7 {
					t.Fatalf("%s pins must be vertical with power above ground and preserve measured span", c.Designator)
				}
			}
			if plPin(first, "1").Y != plPin(core, "4").Y || plPin(second, "1").Y != plPin(core, "4").Y {
				t.Fatal("output terminals must share the direct horizontal rail")
			}
			if len(plan.ExpectedPinNets) != 10 || plan.ExpectedPinNets["U1.2"] != plan.ExpectedPinNets["U1.4"] {
				t.Fatal("all ten physical pins must survive with both VOUTs on the output net")
			}
			for _, wire := range plan.Wires[:3] {
				if wire.Points[0][1] != wire.Points[1][1] {
					t.Fatal("VIN/VOUT main connections should not bend")
				}
			}
			for _, flag := range plan.Flags {
				if flag.Kind == "ground" && flag.Direction != "down" {
					t.Fatal("ground symbols must face down")
				}
			}
			again, err := planPowerLayout(raw, powerLayoutTestOptions())
			if err != nil || !reflect.DeepEqual(plan, again) {
				t.Fatal("identical snapshots must yield deterministic plans")
			}
		}
	}
}

func TestPowerLayoutRefusesUnprovenInput(t *testing.T) {
	cases := []struct {
		name, want string
		mutate     func(map[string]any)
	}{
		{"bbox", "incomplete", func(f map[string]any) { delete(powerLayoutTestComp(f, 2), "bbox") }},
		{"pose", "off-grid", func(f map[string]any) { powerLayoutTestComp(f, 2)["x"] = 401 }},
		{"pin", "off-grid", func(f map[string]any) { powerLayoutTestComp(f, 2)["pins"].([]any)[0].(map[string]any)["x"] = 381 }},
		{"topology", "topology mismatch", func(f map[string]any) { powerLayoutTestComp(f, 2)["pins"].([]any)[0].(map[string]any)["net"] = "+5V" }},
		{"core pin name", "must be GND", func(f map[string]any) {
			powerLayoutTestComp(f, 1)["pins"].([]any)[0].(map[string]any)["pinName"] = "ADJ"
		}},
		{"unread pins", "incomplete", func(f map[string]any) { powerLayoutTestComp(f, 2)["pinsAvailable"] = false }},
		{"duplicate refs", "unexpected or duplicate", func(f map[string]any) { powerLayoutTestComp(f, 2)["designator"] = "C8" }},
		{"floating", "unconnected", func(f map[string]any) { powerLayoutTestComp(f, 2)["pins"].([]any)[0].(map[string]any)["net"] = "" }},
		{"original core orientation", "unsupported core pin orientation", func(f map[string]any) {
			p := powerLayoutTestComp(f, 1)["pins"].([]any)
			p[0].(map[string]any)["y"] = 260
			p[2].(map[string]any)["y"] = 240
		}},
		{"missing sheet", "one measured sheet", func(f map[string]any) {
			r := f["result"].(map[string]any)
			r["components"] = r["components"].([]any)[1:]
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := powerLayoutFixture(t, 0, 0, 20, 0)
			tc.mutate(f)
			_, err := planPowerLayout(powerLayoutBytes(t, f), powerLayoutTestOptions())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
	_, err := planPowerLayout(powerLayoutBytes(t, powerLayoutFixture(t, 0, 0, 20, 0)), powerLayoutOptions{Core: "U1", InputCap: "C2", OutputCaps: []string{"C1", "C3"}, Doc: "another-doc"})
	if err == nil || !strings.Contains(err.Error(), "document mismatch") {
		t.Fatal("must reject another document")
	}
	o := powerLayoutTestOptions()
	o.At = &[2]float64{300, 900}
	_, err = planPowerLayout(powerLayoutBytes(t, powerLayoutFixture(t, 0, 0, 20, 0)), o)
	if err == nil || !strings.Contains(err.Error(), "outside sheet") {
		t.Fatalf("out of sheet plan accepted: %v", err)
	}
}

func TestPowerLayoutRejectsCrossingsAndBodyRouting(t *testing.T) {
	raw := powerLayoutBytes(t, powerLayoutFixture(t, 0, 0, 20, 0))
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 1170, MaxY: 825}
	plan, err := planPowerLayout(raw, powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	plan.Wires = append(plan.Wires, powerLayoutWire{Net: "FOREIGN", Points: [][2]float64{{100, 650}, {600, 650}}})
	if err = validatePowerLayout(plan, sheet); err == nil {
		t.Fatal("cross-net pin and body route accepted")
	}
	plan, _ = planPowerLayout(raw, powerLayoutTestOptions())
	plan.Wires = append(plan.Wires, powerLayoutWire{Net: "GND", Points: [][2]float64{{430, 630}, {430, 680}}})
	if err = validatePowerLayout(plan, sheet); err != nil {
		t.Fatalf("bare interior crossing is not a contact: %v", err)
	}
	// A foreign endpoint on an actual existing segment remains a short.
	for _, w := range plan.Wires[:len(plan.Wires)-1] {
		a, b := w.Points[0], w.Points[1]
		if a[1] == b[1] && plOnSegment([2]float64{430, a[1]}, a, b) {
			plan.Wires[len(plan.Wires)-1].Points[0] = [2]float64{430, a[1]}
			break
		}
	}
	if err = validatePowerLayout(plan, sheet); err == nil || !strings.Contains(err.Error(), "wire crossing") {
		t.Fatalf("foreign T accepted: %v", err)
	}
}

func TestPowerLayoutCanonicalizesOnlyRoundoff(t *testing.T) {
	f := powerLayoutFixture(t, 0, 0, 20, 3)
	for i := 1; i <= 4; i++ {
		c := powerLayoutTestComp(f, i)
		c["x"] = c["x"].(float64) + 1e-10
		for j, p := range c["pins"].([]any) {
			pin := p.(map[string]any)
			pin["x"] = pin["x"].(float64) + float64(j+1)*1e-10
			pin["y"] = pin["y"].(float64) - 1e-10
		}
	}
	if _, err := planPowerLayout(powerLayoutBytes(t, f), powerLayoutTestOptions()); err != nil {
		t.Fatalf("API roundoff rejected: %v", err)
	}
	f = powerLayoutFixture(t, 0, 0, 20, 3)
	c := powerLayoutTestComp(f, 2)
	c["pins"].([]any)[0].(map[string]any)["x"] = 400.01
	if _, err := planPowerLayout(powerLayoutBytes(t, f), powerLayoutTestOptions()); err == nil {
		t.Fatal("genuinely off-grid measurement was silently snapped")
	}
}

func TestPowerLayoutSpacingUsesDesignatorsAndMarkerNameButExcludesValues(t *testing.T) {
	fixture := powerLayoutFixture(t, 0, 0, 20, 3)
	base, err := planPowerLayout(powerLayoutBytes(t, fixture), powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	input, first, second := base.Placements[1], base.Placements[2], base.Placements[3]
	markerLeft := plPin(base.Placements[0], "2").X - 4*schAnchorGrid - math.Max(markerBBoxProfile("power", "+3V3").Far, plPowerTextWidth("+3V3")/2)
	if plPin(input, "1").X+plPowerCapRightReach(input)+bslPartGap > markerLeft {
		t.Fatal("input designator must clear the duplicate VOUT marker")
	}
	if plPin(first, "1").X+plPowerCapRightReach(first)+bslPartGap > second.BBox.MinX {
		t.Fatal("output designator column must clear the next capacitor")
	}
	powerLayoutTestComp(fixture, 3)["otherProperty"] = map[string]any{"Value": "1000uF / 50V"} // C2
	powerLayoutTestComp(fixture, 2)["otherProperty"] = map[string]any{"Value": "470uF / 25V"}  // C1
	long, err := planPowerLayout(powerLayoutBytes(t, fixture), powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	for i := range base.Placements {
		if long.Placements[i].X != base.Placements[i].X || long.Placements[i].Y != base.Placements[i].Y || long.Frames[0].Rect != base.Frames[0].Rect {
			t.Fatalf("non-designator Value changed layout at placement %d: base=%+v long=%+v", i, base.Placements[i], long.Placements[i])
		}
	}
	fixture = powerLayoutFixture(t, 0, 0, 20, 3)
	for i := 1; i <= 4; i++ {
		for _, p := range powerLayoutTestComp(fixture, i)["pins"].([]any) {
			pin := p.(map[string]any)
			if pin["net"] == "+3V3" {
				pin["net"] = "VCC_MAIN_3V3"
			}
		}
	}
	wideNet, err := planPowerLayout(powerLayoutBytes(t, fixture), powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if wideNet.Placements[1].X >= input.X {
		t.Fatal("longer duplicate output net names must widen the input corridor")
	}
}

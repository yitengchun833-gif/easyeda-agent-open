package app

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestModuleFrameGeometryCorpus(t *testing.T) {
	for turns := 0; turns < 4; turns++ {
		for _, span := range []float64{20, 25, 30} {
			plan, err := planPowerLayout(powerLayoutBytes(t, powerLayoutFixture(t, float64(turns)*15, span*2, span, turns)), powerLayoutTestOptions())
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Frames) != 1 {
				t.Fatal("exactly one module frame required")
			}
			f := plan.Frames[0]
			content := powerLayoutContentBounds(plan)
			if !boxInside(content, f.Rect) || checkSchFrameTitleOccupancy(f, f.titleBounds()) != nil {
				t.Fatalf("frame/title cuts into the content: %+v, %+v", f, content)
			}
			if f.FontSize != .2/.01 || f.Color != "#AA00AA" || f.LineType != 1 {
				t.Fatalf("lost presentation contract: %+v", f)
			}
			for _, w := range plan.Wires {
				for _, p := range w.Points {
					if p[0] < f.Rect.MinX || p[0] > f.Rect.MaxX || p[1] < f.Rect.MinY || p[1] > f.Rect.MaxY {
						t.Fatal("wire outside frame")
					}
				}
			}
			raw, _ := json.Marshal(plan)
			if strings.Contains(string(raw), `"notes"`) {
				t.Fatal("Notes must not reappear in the plan")
			}
		}
	}
}

func TestModuleFrameGenericAndInvalidInput(t *testing.T) {
	content := layoutBBox{MinX: 100, MinY: 100, MaxX: 300, MaxY: 220}
	sheet := layoutBBox{MinX: 0, MinY: 0, MaxX: 1170, MaxY: 825}
	a, err := planSchModuleFrame("USB", "USB 接口", content, sheet)
	if err != nil {
		t.Fatal(err)
	}
	content.MinX += 50
	content.MaxX += 50
	content.MinY += 25
	content.MaxY += 25
	b, err := planSchModuleFrame("USB", "USB 接口", content, sheet)
	if err != nil || b.Rect.MinX-a.Rect.MinX != 50 || b.TitleY-a.TitleY != 25 {
		t.Fatalf("frame must follow content translation: %v", err)
	}
	for _, box := range []layoutBBox{
		{MinX: 0, MinY: 10, MaxX: 100, MaxY: 100},
		{MinX: 100, MinY: 100, MaxX: 300, MaxY: 820},
		{MinX: math.NaN(), MinY: 100, MaxX: 300, MaxY: 200},
	} {
		if _, err := planSchModuleFrame("USB", "USB", box, sheet); err == nil {
			t.Fatal("invalid/outside frame must fail")
		}
	}
}

func TestFrameOnlyPlaybookPreservesCircuit(t *testing.T) {
	f := powerLayoutFixture(t, 0, 0, 20, 0)
	plan, err := planPowerLayout(powerLayoutBytes(t, f), powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := powerLayoutFramePlaybook(plan, powerLayoutBytes(t, f)); err == nil {
		t.Fatal("source at different locations must fail offline")
	}
	for _, c := range plan.Placements {
		for _, entry := range f["result"].(map[string]any)["components"].([]any) {
			r := entry.(map[string]any)
			if r["designator"] != c.Designator {
				continue
			}
			r["x"], r["y"], r["rotation"], r["mirror"] = c.X, c.Y, c.Rotation, c.Mirror
			for _, pinEntry := range r["pins"].([]any) {
				pin := pinEntry.(map[string]any)
				for _, p := range c.Pins {
					if pin["pinNumber"] == p.Number {
						pin["x"], pin["y"] = p.X, p.Y
					}
				}
			}
		}
	}
	before := powerLayoutBytes(t, f)
	pb, err := powerLayoutFramePlaybook(plan, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(pb.Steps) != 5 || pb.Steps[0].ExpectSchematic == nil || pb.Steps[3].ExpectSchematic == nil {
		t.Fatal("both circuit guards required")
	}
	for _, s := range pb.Steps {
		if s.Action != "" && s.Action != "schematic.components.list" && s.Action != "schematic.save" {
			t.Fatalf("unexpected circuit mutation %s", s.Action)
		}
		if s.Run != "" && s.Run != "sch frame apply" && s.Run != "sch frame check" {
			t.Fatalf("unexpected command %s", s.Run)
		}
	}
	if !reflect.DeepEqual(before, powerLayoutBytes(t, f)) {
		t.Fatal("converter modified its source data")
	}
	powerLayoutTestComp(f, 1)["pins"].([]any)[0].(map[string]any)["net"] = "+5V"
	if _, err := powerLayoutFramePlaybook(plan, powerLayoutBytes(t, f)); err == nil {
		t.Fatal("wrong existing net must fail")
	}
}

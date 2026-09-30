package app

import (
	"sort"
	"testing"
)

// Field data from EasyEDA Pro 3.2.149, international build (issue #228).
// `sch zone-plan` planned three partitions on the live 1170x825 sheet --
// (18,392)..(978,813), (500,228)..(1036,392), (86,228)..(500,322) -- and
// `sch zone-draw` created them with those y-UP top edges. getState_TopLeftY()
// reported the mirrored values below, so every frame landed off the sheet and
// all 12 placed bodies raised part-frame-unresolved.
func mirroredFrameSurveyInput() map[string]any {
	return map[string]any{
		"rectangles": []any{
			map[string]any{"id": "r-usbc", "x": 17.5, "y": -813.0, "width": 961.0, "height": 420.5, "rotation": 0.0, "color": "#AA00AA"},
			map[string]any{"id": "r-ldo", "x": 500.0, "y": -392.0, "width": 536.0, "height": 164.5, "rotation": 0.0, "color": "#AA00AA"},
			map[string]any{"id": "r-led", "x": 85.5, "y": -321.5, "width": 414.5, "height": 93.5, "rotation": 0.0, "color": "#AA00AA"},
		},
		// The titles sit just inside those frames and round-trip unmirrored,
		// which is what pins the anomaly to the rectangle getter alone.
		"texts": []any{
			map[string]any{"id": "t-usbc", "content": "usbc_ufp_power_or(D1)", "x": 21.5, "y": 791.0, "color": "#AA00AA"},
			map[string]any{"id": "t-ldo", "content": "ams1117_ldo_3v3(C1)", "x": 504.0, "y": 370.0, "color": "#AA00AA"},
			map[string]any{"id": "t-led", "content": "led_indicator_gpio(LED1)", "x": 89.5, "y": 299.5, "color": "#AA00AA"},
		},
	}
}

// Measured bbox centres of the 12 placed parts on that same page.
var mirroredFrameBodies = map[string][2]float64{
	"D1": {159, 620}, "D2": {340, 570}, "J1": {725, 565},
	"R1": {160, 760}, "R2": {440, 760}, "D3": {718.5, 759.5},
	"U1": {740, 300}, "C2": {520, 290}, "C3": {920, 290}, "C1": {1000, 290},
	"LED1": {355, 254.5}, "R3": {160, 250},
}

func TestParseSchFrameSurveyMirrorsRectangleTopY(t *testing.T) {
	s, err := parseSchFrameSurvey(mirroredFrameSurveyInput())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for id, want := range map[string]float64{"r-usbc": 813, "r-ldo": 392, "r-led": 321.5} {
		if got := s.Rectangles[id]["y"]; got != want {
			t.Errorf("%s top y = %v, want %g (y-UP sheet coordinates)", id, got, want)
		}
	}
	// Text anchors are already y-UP and must not be touched.
	if got := s.Texts["t-usbc"]["y"]; got != 791.0 {
		t.Errorf("text top y = %v, want 791 (unmirrored)", got)
	}
}

// Every body must resolve to exactly one frame -- the condition
// schDesignatorFindings enforces before it raises part-frame-unresolved.
func TestSchModuleFramesOwnEveryLiveBody(t *testing.T) {
	s, err := parseSchFrameSurvey(mirroredFrameSurveyInput())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	frames := schModuleFrames(s)
	if len(frames) != 3 {
		t.Fatalf("frames = %d, want 3", len(frames))
	}
	designators := make([]string, 0, len(mirroredFrameBodies))
	for d := range mirroredFrameBodies {
		designators = append(designators, d)
	}
	sort.Strings(designators)
	for _, d := range designators {
		c := mirroredFrameBodies[d]
		owners := 0
		for _, f := range frames {
			if c[0] >= f.Rect.MinX && c[0] <= f.Rect.MaxX && c[1] >= f.Rect.MinY && c[1] <= f.Rect.MaxY {
				owners++
			}
		}
		if owners != 1 {
			t.Errorf("%s: body centre (%g,%g) belongs to %d frames, want exactly 1", d, c[0], c[1], owners)
		}
	}
}

// A build that reports the top edge unmirrored must be left byte-for-byte alone.
func TestParseSchFrameSurveyKeepsPositiveRectangleTopY(t *testing.T) {
	v := map[string]any{
		"rectangles": []any{map[string]any{"id": "r", "x": 100.0, "y": 300.0, "width": 300.0, "height": 200.0, "rotation": 0.0, "color": "#AA00AA"}},
		"texts":      []any{},
	}
	s, err := parseSchFrameSurvey(v)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := s.Rectangles["r"]["y"]; got != 300.0 {
		t.Fatalf("top y = %v, want 300 (unchanged)", got)
	}
	frames := schModuleFrames(s)
	if len(frames) != 1 || frames[0].Rect.MinY != 100 || frames[0].Rect.MaxY != 300 {
		t.Fatalf("frames = %+v, want one frame with y 100..300", frames)
	}
}

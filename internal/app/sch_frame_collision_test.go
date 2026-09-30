package app

import "testing"

// ceshi P2, live rectangle inventory captured 2026-09-13. The old ESP32
// rectangle overlaps the newer LED/MCU/BUTTONS frames even though each layout
// plan independently has no collisions.
func TestCeshiP2StaleFrameRegression(t *testing.T) {
	s := schFrameSurvey{Rectangles: map[string]map[string]any{}}
	add := func(id string, x, y, w, h float64) {
		s.Rectangles[id] = map[string]any{"x": x, "y": y, "width": w, "height": h, "rotation": 0.0, "color": "#AA00AA"}
	}
	add("LED", 36.5, 791, 227, 222.5)
	add("MCU", 284, 789.5, 461.5, 517)
	add("AUTO_DOWNLOAD", 826.5, 790.5, 308, 417)
	add("BUTTONS", 35.5, 544.5, 145, 372)
	if f := schFrameCollisionFindings(s); len(f) != 0 {
		t.Fatalf("clean live page: %+v", f)
	}
	add("old-ESP32", 25, 800, 665, 345)
	add("old-AUTO-BOOT", 700, 800, 310, 240)
	add("old-LED", 25, 445, 260, 160)
	if f := schFrameCollisionFindings(s); len(f) == 0 {
		t.Fatal("real stale-frame overlaps escaped")
	}
}

func TestLiveFrameCollisionIncludesUnrecordedFrames(t *testing.T) {
	for _, tc := range []struct {
		name       string
		x, y, w, h float64
		want       int
	}{
		{"overlap", 350, 300, 100, 100, 1},
		{"contained stale frame", 120, 280, 100, 100, 1},
		{"duplicate", 100, 300, 300, 200, 1},
		{"separate", 410, 300, 100, 100, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, s := schFrameFixture()
			s.Rectangles["old-unrecorded"] = map[string]any{"x": tc.x, "y": tc.y, "width": tc.w, "height": tc.h, "rotation": 0.0, "color": "#AA00AA"}
			if got := len(schFrameCollisionFindings(s)); got != tc.want {
				t.Fatalf("got %d findings, want %d", got, tc.want)
			}
		})
	}
}

func TestLiveFrameCollisionMissingGeometryFails(t *testing.T) {
	_, _, s := schFrameFixture()
	delete(s.Rectangles["rect"], "width")
	f := schFrameCollisionFindings(s)
	if len(f) != 1 || f[0].Level != "ERROR" || f[0].Type != "partition-geometry-unavailable" {
		t.Fatalf("must fail closed: %+v", f)
	}
}

func TestFrameInputRejectsOverlappingZones(t *testing.T) {
	a, _, _ := schFrameFixture()
	b := a
	b.ID = "other"
	p := schFrameDocument{SchemaVersion: 1, DocumentID: "doc", Frames: []schFrameSpec{a, b}}
	if p.validate() == nil {
		t.Fatal("duplicate geometry accepted")
	}
	b.Rect.MinX += 400
	b.Rect.MaxX += 400
	b.TitleX += 400
	p.Frames[1] = b
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
}

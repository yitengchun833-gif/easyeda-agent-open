package app

import "testing"

func TestSurveyTextFrameCrossing(t *testing.T) {
	for _, tc := range []struct {
		name string
		x    float64
		want int
	}{
		{"inside", 5, 0}, {"crossing", 95, 1}, {"outside ownership unknown", 110, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := schFrameSurvey{
				Rectangles: map[string]map[string]any{"frame": {"x": 0.0, "y": 100.0, "width": 100.0, "height": 100.0, "rotation": 0.0, "color": "#AA00AA"}},
				Texts:      map[string]map[string]any{"text": {"content": "title", "bbox": map[string]any{"minX": tc.x, "minY": 10.0, "maxX": tc.x + 10, "maxY": 20.0}}},
			}
			f := schSurveyTextFindings(s, nil)
			if len(f) != tc.want {
				t.Fatalf("got %+v", f)
			}
			if len(f) > 0 && f[0].Type != "text-frame-crossing" {
				t.Fatalf("got %+v", f)
			}
		})
	}
}

func TestSurveyTextCollisionAndMissingGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		bbox map[string]any
		want string
	}{
		{"overlap", map[string]any{"minX": 5.0, "minY": 5.0, "maxX": 15.0, "maxY": 15.0}, "text-overlap"},
		{"separate", map[string]any{"minX": 20.0, "minY": 20.0, "maxX": 30.0, "maxY": 30.0}, ""},
		{"touch", map[string]any{"minX": 10.0, "minY": 0.0, "maxX": 20.0, "maxY": 10.0}, ""},
		{"missing", nil, "text-geometry-unavailable"},
		{"partial", map[string]any{"minX": 0.0}, "text-geometry-unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := schFrameSurvey{Texts: map[string]map[string]any{"title": {"content": "POWER_ENTRY", "bbox": tc.bbox}}}
			f := schSurveyTextFindings(s, []layoutComp{{ID: "U1", ComponentType: "part", BBox: &layoutBBox{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}}})
			if tc.want == "" {
				if len(f) != 0 {
					t.Fatalf("false positive: %+v", f)
				}
				return
			}
			if len(f) != 1 || f[0].Type != tc.want {
				t.Fatalf("want %s, got %+v", tc.want, f)
			}
			if !checkLevelBlocks(f[0].Level, true) {
				t.Fatal("strict gate must block")
			}
		})
	}
}

func TestSurveyTextAgainstTextAndMarker(t *testing.T) {
	bb := map[string]any{"minX": 0.0, "minY": 0.0, "maxX": 10.0, "maxY": 10.0}
	s := schFrameSurvey{Texts: map[string]map[string]any{"a": {"content": "A", "bbox": bb}, "b": {"content": "B", "bbox": bb}}}
	f := schSurveyTextFindings(s, []layoutComp{{ID: "port", ComponentType: "netport", BBox: &layoutBBox{MinX: 0, MinY: 0, MaxX: 10, MaxY: 10}}})
	if len(f) != 3 {
		t.Fatalf("want one text pair + two marker overlaps, got %+v", f)
	}
}

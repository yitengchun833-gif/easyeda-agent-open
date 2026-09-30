package app

import (
	"fmt"
	"sort"
	"strings"
)

// Free text is absent from components.list. Use the SDK text inventory and
// rendered bboxes, not guessed font metrics. Component attribute text is NOT
// part of this inventory and must not be advertised as verified by this rule.
func schSurveyTextFindings(s schFrameSurvey, comps []layoutComp) []checkFinding {
	var out []checkFinding
	var texts []layoutComp
	ids := make([]string, 0, len(s.Texts))
	for id := range s.Texts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		m := s.Texts[id]
		if m["content"] == "" {
			continue
		}
		bb, _ := m["bbox"].(map[string]any)
		coords := make([]float64, 4)
		valid := true
		for i, k := range []string{"minX", "minY", "maxX", "maxY"} {
			n, ok := bb[k].(float64)
			if !ok || !plFinite(n) {
				valid = false
			}
			coords[i] = n
		}
		b := layoutBBox{MinX: coords[0], MinY: coords[1], MaxX: coords[2], MaxY: coords[3]}
		if !valid || !plBoxValid(b) || b.MinX == b.MaxX || b.MinY == b.MaxY {
			out = append(out, checkFinding{Type: "text-geometry-unavailable", Level: "ERROR", PrimitiveId: id, Message: "nonempty text rendered bbox unavailable; text collision check incomplete"})
			continue
		}
		texts = append(texts, layoutComp{ID: id, ComponentType: "text", BBox: &b})
	}
	for i, t := range texts {
		frameIDs := make([]string, 0, len(s.Rectangles))
		for id := range s.Rectangles {
			frameIDs = append(frameIDs, id)
		}
		sort.Strings(frameIDs)
		for _, id := range frameIDs {
			r := s.Rectangles[id]
			if !strings.EqualFold(fmt.Sprint(r["color"]), "#AA00AA") {
				continue
			}
			x, xok := r["x"].(float64)
			y, yok := r["y"].(float64)
			w, wok := r["width"].(float64)
			h, hok := r["height"].(float64)
			rot, rok := r["rotation"].(float64)
			if !xok || !yok || !wok || !hok || !rok || rot != 0 || !plFinite(x) || !plFinite(y) || !plFinite(w) || !plFinite(h) || w <= 0 || h <= 0 {
				continue
			}
			frame := layoutBBox{MinX: x, MaxX: x + w, MinY: y - h, MaxY: y}
			ox, oy, overlap := overlapExtent(*t.BBox, frame)
			if overlap && ox > 0 && oy > 0 && !boxInside(*t.BBox, frame) {
				out = append(out, checkFinding{Type: "text-frame-crossing", Level: "warn", PrimitiveId: t.ID, PrimitiveIds: []string{t.ID, id}, BBox: t.BBox, Message: "rendered free text crosses a module-frame boundary; verify title/content placement"})
			}
		}
		others := append([]layoutComp{}, texts[i+1:]...)
		for _, c := range comps {
			if c.ComponentType == "part" || isSchMarker(c.ComponentType) {
				others = append(others, c)
			}
		}
		for _, c := range others {
			if c.BBox == nil || c.ID == t.ID {
				continue
			}
			boxes := []layoutBBox{*c.BBox}
			if band := flagTextBand(c); band != nil {
				boxes = append(boxes, *band)
			}
			for _, b := range boxes {
				ox, oy, overlap := overlapExtent(*t.BBox, b)
				if !overlap || ox <= 0 || oy <= 0 {
					continue
				}
				out = append(out, checkFinding{Type: "text-overlap", Level: "warn", PrimitiveId: t.ID, PrimitiveIds: []string{t.ID, c.ID}, BBox: t.BBox, Message: fmt.Sprintf("text %s overlaps %s %s by %.2f×%.2f raw (marker text bands are estimated)", t.ID, c.ComponentType, c.ID, ox, oy)})
				break
			}
		}
	}
	return out
}

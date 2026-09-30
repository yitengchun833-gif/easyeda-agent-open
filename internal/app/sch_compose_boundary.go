package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// The frame adapter creates/verifies a 1-raw stroke. Explicit inner-border
// clearance includes that half-stroke, not just the rectangle centerline.
const schCompositionFrameHalfStroke = 0.5

// BBox's numeric zero values cannot distinguish an explicit coordinate from a
// missing/null JSON field. Only the new inner-border contract requires all four
// observations here; legacy sheet/placement decoding remains compatible.
func validateSchCompositionBorderJSON(raw []byte) error {
	var src map[string]json.RawMessage
	if err := json.Unmarshal(raw, &src); err != nil {
		return err
	}
	for name := range src {
		if name != "sheetBorder" && strings.EqualFold(name, "sheetBorder") {
			return fmt.Errorf("use the exact field name sheetBorder, not %q", name)
		}
	}
	b, present := src["sheetBorder"]
	if !present || bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return nil
	}
	var border map[string]json.RawMessage
	if err := json.Unmarshal(b, &border); err != nil {
		return fmt.Errorf("sheetBorder must be an object with all four coordinates: %w", err)
	}
	for _, name := range []string{"minX", "minY", "maxX", "maxY"} {
		value, present := border[name]
		if !present || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("sheetBorder.%s must be explicitly provided as a number", name)
		}
		var coordinate float64
		if err := json.Unmarshal(value, &coordinate); err != nil || !plFinite(coordinate) {
			return fmt.Errorf("sheetBorder.%s must be a finite number", name)
		}
	}
	return nil
}

func schCompositionUsableBounds(sheet layoutBBox, border *layoutBBox) (layoutBBox, string, error) {
	boundary, source, inset := sheet, "sheet-bbox-fallback", schModulePageMargin
	if !plBoxValid(sheet) {
		return layoutBBox{}, "", fmt.Errorf("invalid sheet bbox")
	}
	if border != nil {
		if !plBoxValid(*border) || !boxInside(*border, sheet) {
			return layoutBBox{}, "", fmt.Errorf("sheetBorder must be a positive finite bbox inside the measured sheet")
		}
		boundary, source = *border, "explicit-sheet-border"
		inset += schCompositionFrameHalfStroke
	}
	usable := layoutBBox{MinX: plCeil(boundary.MinX + inset), MinY: plCeil(boundary.MinY + inset), MaxX: plFloor(boundary.MaxX - inset), MaxY: plFloor(boundary.MaxY - inset)}
	if !plBoxValid(usable) {
		return layoutBBox{}, "", fmt.Errorf("drawing boundary has no usable area after clearance and inward grid rounding")
	}
	return usable, source, nil
}

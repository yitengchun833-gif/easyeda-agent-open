package app

// pcb outline-round — generate a rounded-rectangle board outline (#29). The
// connector accepts EasyEDA's verified polygon source format, so each corner is a
// real `ARC,signedSweep,endX,endY` command instead of a chord approximation.

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
)

const boardOutlineLineWidthMil = 10.0

// normalizeRoundedRect returns ordered corners and a radius clamped to half the
// shorter side. EasyEDA PCB coordinates are y-up and use mil.
func normalizeRoundedRect(x0, y0, x1, y1, r float64) (float64, float64, float64, float64, float64) {
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	if maxR := math.Min(x1-x0, y1-y0) / 2; r > maxR {
		r = maxR
	}
	if r < 0 {
		r = 0
	}
	return x0, y0, x1, y1, r
}

// roundedRectSource returns one closed EasyEDA polygon source. Positive ARC
// sweeps walk counter-clockwise in the verified PCB source format. With r=0 it
// intentionally falls back to a sharp rectangle without degenerate arcs.
func roundedRectSource(x0, y0, x1, y1, r float64) []any {
	x0, y0, x1, y1, r = normalizeRoundedRect(x0, y0, x1, y1, r)
	if r == 0 {
		return []any{
			round2(x0), round2(y0), "L",
			round2(x1), round2(y0),
			round2(x1), round2(y1),
			round2(x0), round2(y1),
			round2(x0), round2(y0),
		}
	}
	return []any{
		round2(x0 + r), round2(y0),
		"L", round2(x1 - r), round2(y0),
		"ARC", 90.0, round2(x1), round2(y0 + r),
		"L", round2(x1), round2(y1 - r),
		"ARC", 90.0, round2(x1 - r), round2(y1),
		"L", round2(x0 + r), round2(y1),
		"ARC", 90.0, round2(x0), round2(y1 - r),
		"L", round2(x0), round2(y0 + r),
		"ARC", 90.0, round2(x0 + r), round2(y0),
	}
}

// currentOutlineCenterlineRect reads the true milled edge. outline.get's bbox is
// rendered geometry and includes half the stroke on every side, so it must never
// be used to infer nominal board dimensions.
func currentOutlineCenterlineRect(cfg *appConfig, window string) ([4]float64, error) {
	res, err := requestAction(cfg, "pcb.outline.get", window, nil)
	if err != nil || res == nil {
		return [4]float64{}, fmt.Errorf("outline.get failed: %v", err)
	}
	bb, ok := mnav(res.Result, "centerlineBBox").(map[string]any)
	if ok {
		minX, okMinX := asFloatOK(bb["minX"])
		minY, okMinY := asFloatOK(bb["minY"])
		maxX, okMaxX := asFloatOK(bb["maxX"])
		maxY, okMaxY := asFloatOK(bb["maxY"])
		if okMinX && okMinY && okMaxX && okMaxY && maxX > minX && maxY > minY {
			return [4]float64{minX, minY, maxX, maxY}, nil
		}
	}
	return [4]float64{}, fmt.Errorf("board outline has no readable center-line bounds; pass --rect explicitly rather than using the stroke-inclusive rendered bbox")
}

// runOutlineRound resolves the rectangle (explicit --rect, else the current outline
// center-line bounds), expands by margin, rounds the corners, and replaces the outline.
func runOutlineRound(cfg *appConfig, window, rectSpec string, radius, margin float64, dryRun bool, stdout, stderr io.Writer) error {
	// ADR-0004 Decision 4: dry-run 必须纯计算 —— 机械保证,Mutates 派发直接被拒。
	if dryRun {
		defer setDispatchDryRun(true)()
	}
	var x0, y0, x1, y1 float64
	if rectSpec != "" {
		var err error
		x0, y0, x1, y1, err = parseRectSpec(rectSpec)
		if err != nil {
			return err
		}
	} else {
		rect, err := currentOutlineCenterlineRect(cfg, window)
		if err != nil {
			return fmt.Errorf("no --rect and no current outline to round (%v) — set one with `pcb outline-set`/`outline-fit` first", err)
		}
		x0, y0, x1, y1 = rect[0], rect[1], rect[2], rect[3]
	}
	x0, y0, x1, y1, _ = normalizeRoundedRect(x0, y0, x1, y1, 0)
	// margin expands outward.
	x0 -= margin
	y0 -= margin
	x1 += margin
	y1 += margin
	if x1 <= x0 || y1 <= y0 {
		return fmt.Errorf("--margin collapses the board rectangle: [%.2f,%.2f]-[%.2f,%.2f] mil", x0, y0, x1, y1)
	}

	if radius <= 0 {
		radius = math.Min(x1-x0, y1-y0) * 0.12 // sensible default
	}
	x0, y0, x1, y1, radius = normalizeRoundedRect(x0, y0, x1, y1, radius)
	source := roundedRectSource(x0, y0, x1, y1, radius)

	if dryRun {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"dryRun": true, "rect": []float64{x0, y0, x1, y1},
			"width": round2(x1 - x0), "height": round2(y1 - y0), "radius": round2(radius),
			"lineWidth": boardOutlineLineWidthMil, "locked": true,
			"outlineFormat": "arc-polyline", "source": source,
		})
	}
	if err := dispatch(cfg, "pcb.outline.set", window, map[string]any{
		"source": source, "lineWidth": boardOutlineLineWidthMil,
	}, stdout, stderr); err != nil {
		return err
	}
	return nil
}

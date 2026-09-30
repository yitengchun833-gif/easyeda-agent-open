package app

// pcb_fillrect.go — `pcb fill create` rect ergonomics + oversized-fill guard
// (issue #109). --rect is TWO OPPOSITE CORNERS "x0,y0,x1,y1"; an agent passing
// x,y,w,h by intuition once generated giant GND fills over the whole USB-C area
// (~50 native DRC violations). Three defenses:
//  1. help text spells out the two-corner semantics (cmd_pcb.go fill section);
//  2. --at x,y --size w,h alias removes the ambiguity (converted here);
//  3. a sanity guard rejects fills larger than 25% of the board-outline bbox
//     (or > 4,000,000 mil² ≈ 50×50 mm when no outline is readable) unless
//     --force-large is passed.

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	// fillLargeBoardFrac: a fill covering more than this fraction of the board
	// bbox is almost always a --rect x,y,w,h mistake, not a design intent.
	fillLargeBoardFrac = 0.25
	// fillLargeAbsMil2: absolute fallback threshold (mil²) when the board
	// outline cannot be read — 4,000,000 mil² ≈ 50×50 mm.
	fillLargeAbsMil2 = 4_000_000.0
)

// parsePairCSV parses "a,b" into two floats (spaces tolerated).
func parsePairCSV(s, flagName string) (a, b float64, err error) {
	parts := strings.Split(strings.TrimSpace(s), ",")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("%s must be 'a,b' (2 comma-separated mil values), got %q", flagName, s)
	}
	var v [2]float64
	for i, p := range parts {
		f, e := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if e != nil {
			return 0, 0, fmt.Errorf("%s value %q is not a number", flagName, strings.TrimSpace(p))
		}
		v[i] = f
	}
	return v[0], v[1], nil
}

// atSizeToRectSpec converts --at "x,y" (anchor corner, mil, y-up) + --size
// "w,h" (extends +x/+y) into the canonical --rect spec "x0,y0,x1,y1".
// Both flags must be present; w and h must be positive.
func atSizeToRectSpec(at, size string) (string, error) {
	if at == "" || size == "" {
		return "", fmt.Errorf("--at and --size must be used together (--at x,y --size w,h)")
	}
	x, y, err := parsePairCSV(at, "--at")
	if err != nil {
		return "", err
	}
	w, h, err := parsePairCSV(size, "--size")
	if err != nil {
		return "", err
	}
	if w <= 0 || h <= 0 {
		return "", fmt.Errorf("--size must be positive 'w,h' (mil), got %g,%g", w, h)
	}
	return fmt.Sprintf("%g,%g,%g,%g", x, y, x+w, y+h), nil
}

// bboxAreaOf returns the axis-aligned bounding-box area (mil²) of a polygon.
func bboxAreaOf(points [][]float64) float64 {
	if len(points) == 0 {
		return 0
	}
	minX, maxX := points[0][0], points[0][0]
	minY, maxY := points[0][1], points[0][1]
	for _, p := range points[1:] {
		if len(p) < 2 {
			continue
		}
		if p[0] < minX {
			minX = p[0]
		}
		if p[0] > maxX {
			maxX = p[0]
		}
		if p[1] < minY {
			minY = p[1]
		}
		if p[1] > maxY {
			maxY = p[1]
		}
	}
	return (maxX - minX) * (maxY - minY)
}

// checkFillAreaGuard rejects a suspiciously huge fill area. boardArea is the
// board-outline bbox area in mil² (haveBoard=false when the outline is not
// readable, e.g. PCB not foreground / no outline set — the absolute threshold
// applies instead). force (--force-large) bypasses the guard entirely.
func checkFillAreaGuard(points [][]float64, boardArea float64, haveBoard, force bool) error {
	if force {
		return nil
	}
	area := bboxAreaOf(points)
	const hint = "--rect takes TWO OPPOSITE CORNERS 'x0,y0,x1,y1' (NOT x,y,w,h) — if you meant width/height use --at x,y --size w,h; pass --force-large if this huge fill is intentional"
	if haveBoard && boardArea > 0 {
		if area > boardArea*fillLargeBoardFrac {
			return fmt.Errorf("refusing fill: area %.0f mil² is %.0f%% of the board bbox (%.0f mil², limit %.0f%%). %s",
				area, area/boardArea*100, boardArea, fillLargeBoardFrac*100, hint)
		}
		return nil
	}
	if area > fillLargeAbsMil2 {
		return fmt.Errorf("refusing fill: area %.0f mil² exceeds %.0f mil² (≈50×50 mm; board outline not readable, absolute cap applies). %s",
			area, fillLargeAbsMil2, hint)
	}
	return nil
}

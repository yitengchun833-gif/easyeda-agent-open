package app

import (
	"math"
	"sort"
)

// ── sch autoplace-free: pack parts into the sheet's blank space ──────────────
//
// The spec-driven `sch autolayout` needs you to name zones up front. This is the
// zone-less sibling the "把这些件塞进纸面空白处" request wanted: read every placed
// part's real bbox + the sheet's usable area, treat the parts you're NOT moving as
// fixed obstacles (plus the title-block keep-out), and greedily drop each movable
// part into the first free slot — top-left first-fit over a grid, collision-free by
// construction. Deterministic and pure (unit-testable, no connector); the I/O side
// (pull geometry, --apply via schematic.component.modify) sits in the CLI command.
//
// It reuses layoutBBox / boxesOverlap / rectGap / boxInside / bboxSize / bboxCenter
// / recenterBox / snapAnchor / titleBlockKeepout rather than re-deriving them.

// freePlaceOpts are the tunable knobs.
type freePlaceOpts struct {
	Margin          float64 // inset from the sheet edge (schematic units)
	Gap             float64 // min edge-to-edge gap to every obstacle + already-placed part
	GridStep        float64 // scan resolution for candidate anchor positions
	AvoidTitleBlock bool    // title block is a hard keep-out
}

func defaultFreePlaceOpts() freePlaceOpts {
	return freePlaceOpts{Margin: 40, Gap: 20, GridStep: 20, AvoidTitleBlock: true}
}

// freePlacement is one relocated part.
type freePlacement struct {
	Designator  string  `json:"designator"`
	PrimitiveID string  `json:"primitiveId,omitempty"`
	FromX       float64 `json:"fromX"`
	FromY       float64 `json:"fromY"`
	X           float64 `json:"x"`
	Y           float64 `json:"y"`
}

type freePlaceReport struct {
	OK         bool            `json:"ok"`
	Placements []freePlacement `json:"placements"`
	Unplaced   []string        `json:"unplaced,omitempty"` // designators that found no free slot
	Fixed      int             `json:"fixedObstacles"`
	Note       string          `json:"note,omitempty"`
	usable     layoutBBox      // for the caller's report; not serialized
}

// autoSelectMovable is the default move-set when the caller names none: every part
// that is currently OUTSIDE the usable area, or OVERLAPS another part. A part that
// is already in-bounds and clear of everyone is left exactly where it is.
func autoSelectMovable(parts []alPart, usable layoutBBox) map[string]bool {
	move := map[string]bool{}
	for i, p := range parts {
		if !p.HasBBox {
			continue
		}
		if !boxInside(p.BBox, usable) {
			move[p.PrimitiveID] = true
			continue
		}
		for j, q := range parts {
			if i == j || !q.HasBBox {
				continue
			}
			if boxesOverlap(p.BBox, q.BBox) {
				move[p.PrimitiveID] = true
				break
			}
		}
	}
	return move
}

// planFreePlace packs the move-set parts into free space. `move` keys are the
// primitiveIds to relocate; parts not in `move` are fixed obstacles. `sheet` is
// required (the packing region); nil sheet returns an OK=false report.
func planFreePlace(parts []alPart, move map[string]bool, sheet *layoutBBox, opts freePlaceOpts) freePlaceReport {
	rep := freePlaceReport{OK: true}
	if sheet == nil {
		rep.OK = false
		rep.Note = "no sheet bbox — cannot compute the packing region"
		return rep
	}
	usable := layoutBBox{
		MinX: sheet.MinX + opts.Margin, MinY: sheet.MinY + opts.Margin,
		MaxX: sheet.MaxX - opts.Margin, MaxY: sheet.MaxY - opts.Margin,
	}
	rep.usable = usable

	// Fixed obstacles: every part NOT in the move-set (with a bbox), plus the
	// title-block keep-out. Grows as we place move-set parts so later ones dodge
	// earlier ones.
	var obstacles []layoutBBox
	for _, p := range parts {
		if p.HasBBox && !move[p.PrimitiveID] {
			obstacles = append(obstacles, p.BBox)
		}
	}
	if opts.AvoidTitleBlock {
		if tb, _ := titleBlockKeepout(sheet); tb != nil {
			obstacles = append(obstacles, *tb)
		}
	}
	rep.Fixed = len(obstacles)

	// Movable parts, largest-area first for tighter packing; deterministic
	// tie-break by designator so the same input always yields the same layout.
	var todo []alPart
	for _, p := range parts {
		if p.HasBBox && move[p.PrimitiveID] {
			todo = append(todo, p)
		}
	}
	sort.SliceStable(todo, func(i, j int) bool {
		wi, hi := bboxSize(todo[i].BBox)
		wj, hj := bboxSize(todo[j].BBox)
		ai, aj := wi*hi, wj*hj
		if ai != aj {
			return ai > aj
		}
		return todo[i].Designator < todo[j].Designator
	})

	fits := func(cand layoutBBox) bool {
		if !boxInside(cand, usable) {
			return false
		}
		for _, o := range obstacles {
			if boxesOverlap(cand, o) || rectGap(cand, o) < opts.Gap {
				return false
			}
		}
		return true
	}

	step := opts.GridStep
	if step < schAnchorGrid {
		step = schAnchorGrid
	}
	for _, p := range todo {
		w, h := bboxSize(p.BBox)
		ocx, ocy := bboxCenter(p.BBox)
		placed := false
		// Top-left first-fit: scan rows (y) then columns (x), snapped to the grid.
		for y := usable.MinY; y+h <= usable.MaxY+1e-6 && !placed; y += step {
			for x := usable.MinX; x+w <= usable.MaxX+1e-6; x += step {
				cx, cy := x+w/2, y+h/2
				cand := recenterBox(p.BBox, cx, cy)
				if !fits(cand) {
					continue
				}
				// Convert the chosen bbox-center back to a grid-snapped anchor.
				dx, dy := cx-ocx, cy-ocy
				nx, ny := snapAnchor(p.AnchorX+dx), snapAnchor(p.AnchorY+dy)
				// Re-derive the snapped bbox center and re-validate (snapping can
				// nudge it a few units); skip this slot if snapping broke the fit.
				scx, scy := ocx+(nx-p.AnchorX), ocy+(ny-p.AnchorY)
				snapped := recenterBox(p.BBox, scx, scy)
				if !fits(snapped) {
					continue
				}
				obstacles = append(obstacles, snapped)
				rep.Placements = append(rep.Placements, freePlacement{
					Designator: p.Designator, PrimitiveID: p.PrimitiveID,
					FromX: round2(p.AnchorX), FromY: round2(p.AnchorY),
					X: nx, Y: ny,
				})
				placed = true
				break
			}
		}
		if !placed {
			rep.Unplaced = append(rep.Unplaced, p.Designator)
			rep.OK = false
		}
	}
	return rep
}

// freePlaceUsableArea exposes the inset region for the CLI report (avoids
// duplicating the margin math there).
func freePlaceUsableArea(sheet layoutBBox, margin float64) layoutBBox {
	return layoutBBox{
		MinX: sheet.MinX + margin, MinY: sheet.MinY + margin,
		MaxX: math.Max(sheet.MinX+margin, sheet.MaxX-margin),
		MaxY: math.Max(sheet.MinY+margin, sheet.MaxY-margin),
	}
}

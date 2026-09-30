package app

import (
	"fmt"
	"math"
)

type libMeasuredStem struct {
	component string
	pin       string
	net       string
	a, b      [2]float64
}

// validateLibGeometry validates an incomplete local placement/routing candidate.
// Unlike the final composition net gate, it does not require every pin to have
// reached its final named tree yet.
func validateLibGeometry(p *powerLayoutPlan) error {
	if err := validatePowerLayout(p, layoutBBox{-math.MaxFloat64, -math.MaxFloat64, math.MaxFloat64, math.MaxFloat64}); err != nil {
		return err
	}
	if _, err := compositionMarkerGeometry(p); err != nil {
		return err
	}
	stems, err := libMeasuredStems(p)
	if err != nil {
		return err
	}
	segments, err := schTerminalSegments(p)
	if err != nil {
		return err
	}
	if err := libValidatePinExitSpace(p, stems, segments); err != nil {
		return err
	}
	for i, stem := range stems {
		for _, c := range p.Placements {
			if c.Designator != stem.component && plSegmentBox(stem.a, stem.b, c.BBox) {
				return schObstruction("stem-body", fmt.Errorf("pin stem %s.%s crosses %s body", stem.component, stem.pin, c.Designator), stem.component, c.Designator)
			}
		}
		for _, wire := range segments {
			if (stem.net == "" || stem.net != wire.Net) && plSegmentsMeet(stem.a, stem.b, wire.Points[0], wire.Points[1]) {
				return schWireObstruction(p, "foreign-stem", fmt.Errorf("wire/marker lead %s touches NC/foreign pin stem %s.%s", wire.Net, stem.component, stem.pin), []string{wire.Net}, stem.component)
			}
		}
		for _, other := range stems[:i] {
			if (stem.net == "" || stem.net != other.net) && plSegmentsMeet(stem.a, stem.b, other.a, other.b) {
				return schObstruction("stem-contact", fmt.Errorf("NC/foreign pin stems intersect: %s.%s/%s.%s", stem.component, stem.pin, other.component, other.pin), stem.component, other.component)
			}
		}
		for _, marker := range p.Flags {
			for _, box := range schTerminalMarkerBoxes(marker) {
				if plSegmentBox(stem.a, stem.b, box) {
					return schWireObstruction(p, "marker-stem", fmt.Errorf("marker %s body/text crosses pin stem %s.%s", marker.Net, stem.component, stem.pin), []string{marker.Net}, stem.component)
				}
			}
		}
	}
	for i, c := range p.Placements {
		for _, other := range p.Placements[:i] {
			for _, label := range libPartLabelBoxes(c) {
				for _, otherLabel := range libPartLabelBoxes(other) {
					if boxesGapOverlap(label, other.BBox, 5) || boxesGapOverlap(otherLabel, c.BBox, 5) || boxesGapOverlap(label, otherLabel, 5) {
						return schObstruction("label-reservation", fmt.Errorf("component label reservation collision: %s/%s", c.Designator, other.Designator), c.Designator, other.Designator)
					}
				}
			}
		}
	}
	return nil
}

// A connected pin owes one minimum-grid outward segment, even before its real
// wire is routed. Reserve that obligation while placing peripherals, not by
// persisting fake wires. NC/unconnected pins have no such routing obligation.
func libValidatePinExitSpace(p *powerLayoutPlan, stems []libMeasuredStem, segments []powerLayoutWire) error {
	for _, c := range p.Placements {
		for _, q := range c.Pins {
			if q.Net == "" {
				continue
			}
			side, err := libPinSide(q, c.BBox)
			if err != nil {
				return err
			}
			x, y := endpointFor(q.X, q.Y, schAnchorGrid, side)
			a, b := [2]float64{q.X, q.Y}, [2]float64{x, y}
			for _, other := range p.Placements {
				if plWireEntersBody(a, b, other) {
					return schObstruction("pin-exit-body", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment crosses %s body", c.Designator, q.Number, other.Designator), c.Designator, other.Designator)
				}
				// Only measured Designator boxes are wire obstacles. The legacy
				// broad label reservation intentionally cannot stand in for actual
				// text geometry (it includes large known-empty pin columns).
				for _, box := range other.TextBBoxes {
					if plSegmentTouchesBox(a, b, box) {
						return schObstruction("pin-exit-label", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment crosses %s Designator", c.Designator, q.Number, other.Designator), c.Designator, other.Designator)
					}
				}
				for _, pin := range other.Pins {
					if pin.Net != q.Net && plOnSegment([2]float64{pin.X, pin.Y}, a, b) {
						return schObstruction("pin-exit-foreign-pin", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment touches foreign pin %s.%s", c.Designator, q.Number, other.Designator, pin.Number), c.Designator, other.Designator)
					}
					if pin.Net == "" || pin.Net == q.Net || (c.Designator == other.Designator && q.Number == pin.Number) {
						continue
					}
					otherSide, err := libPinSide(pin, other.BBox)
					if err != nil {
						return err
					}
					ox, oy := endpointFor(pin.X, pin.Y, schAnchorGrid, otherSide)
					if plSegmentsContact(a, b, [2]float64{pin.X, pin.Y}, [2]float64{ox, oy}) {
						return schObstruction("pin-exit-foreign-exit", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment contacts foreign exit obligation %s.%s", c.Designator, q.Number, other.Designator, pin.Number), c.Designator, other.Designator)
					}
				}
			}
			for _, stem := range stems {
				if stem.net != q.Net && plSegmentsMeet(a, b, stem.a, stem.b) {
					return schObstruction("pin-exit-foreign-stem", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment touches foreign pin stem %s.%s", c.Designator, q.Number, stem.component, stem.pin), c.Designator, stem.component)
				}
			}
			for _, w := range segments {
				if w.Net != q.Net && plSegmentsContact(a, b, w.Points[0], w.Points[1]) {
					return schWireObstruction(p, "pin-exit-foreign-wire", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment touches foreign wire %s", c.Designator, q.Number, w.Net), []string{w.Net}, c.Designator)
				}
			}
			for _, f := range p.Flags {
				for _, box := range schTerminalMarkerBoxes(f) {
					if !plSegmentBox(a, b, box) {
						continue
					}
					fx, fy := endpointFor(f.PinX, f.PinY, f.Offset, f.Direction)
					// A marker on this exact minimum lead is an intended connection;
					// all other marker body/text intrusions remain obstacles.
					if f.Net == q.Net && f.PinX == q.X && f.PinY == q.Y && fx == b[0] && fy == b[1] {
						continue
					}
					return schWireObstruction(p, "pin-exit-marker", fmt.Errorf("pin-exit-blocked: %s.%s minimum outward segment crosses marker %s", c.Designator, q.Number, f.Net), []string{f.Net}, c.Designator)
				}
			}
		}
	}
	return nil
}

// Only infer the visible axis-aligned stem between an outer measured connection
// point and its unique body edge. A point inside a 0.5-raw stroke halo has no
// positive outside stem; never extrapolate it into an invented long pin.
func libMeasuredStems(p *powerLayoutPlan) ([]libMeasuredStem, error) {
	var stems []libMeasuredStem
	for _, c := range p.Placements {
		if !plBoxValid(c.BBox) {
			return nil, fmt.Errorf("%s invalid measured body", c.Designator)
		}
		for _, q := range c.Pins {
			if !plGrid(q.X) || !plGrid(q.Y) {
				return nil, fmt.Errorf("%s.%s invalid measured pin coordinate", c.Designator, q.Number)
			}
			side, err := libPinSide(q, c.BBox)
			if err != nil {
				return nil, fmt.Errorf("%s.%s measured pin stem: %w", c.Designator, q.Number, err)
			}
			a := [2]float64{q.X, q.Y}
			b := a
			// For an outside pin the body edge lies inward, not further outward.
			switch side {
			case "left":
				if q.X < c.BBox.MinX {
					b[0] = c.BBox.MinX
				}
			case "right":
				if q.X > c.BBox.MaxX {
					b[0] = c.BBox.MaxX
				}
			case "up":
				if q.Y > c.BBox.MaxY {
					b[1] = c.BBox.MaxY
				}
			case "down":
				if q.Y < c.BBox.MinY {
					b[1] = c.BBox.MinY
				}
			}
			if a != b {
				stems = append(stems, libMeasuredStem{component: c.Designator, pin: q.Number, net: q.Net, a: a, b: b})
			}
		}
	}
	return stems, nil
}

// Conservative right-side Designator estimate when no measured Designator bbox
// is available. Value/model/MPN are deliberately excluded from page collision
// and frame containment. This is not an official measured text bbox. Wires are
// not blocked by the entire reservation because the exact vertical position is
// not represented by powerLayoutPlacement.
func libPartLabelReservation(c powerLayoutPlacement) layoutBBox {
	width := plPowerTextWidth(c.Designator)
	return layoutBBox{MinX: c.BBox.MaxX, MinY: c.BBox.MinY - 20, MaxX: c.BBox.MaxX + 10 + width, MaxY: c.BBox.MaxY + 15}
}

func libPartLabelBoxes(c powerLayoutPlacement) []layoutBBox {
	if len(c.TextBBoxes) > 0 {
		return c.TextBBoxes
	}
	return []layoutBBox{libPartLabelReservation(c)}
}

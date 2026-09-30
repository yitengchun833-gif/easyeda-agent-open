package schguard

import "math"

// CardinalOutward returns a unit vector for an official world-coordinate pin
// rotation. It does not apply a component transform or infer missing evidence.
func CardinalOutward(rotation float64) (float64, float64, bool) {
	if math.IsNaN(rotation) || math.IsInf(rotation, 0) {
		return 0, 0, false
	}
	r := math.Mod(math.Mod(rotation, 360)+360, 360) / 90
	if math.Abs(r-math.Round(r)) > epsilon {
		return 0, 0, false
	}
	switch int(math.Round(r)) % 4 {
	case 0:
		return 1, 0, true
	case 1:
		return 0, 1, true
	case 2:
		return -1, 0, true
	default:
		return 0, -1, true
	}
}

// PinRayOutward is the shared constant-time first-segment predicate used by
// execution and offline candidate validation (y-UP). A zero ray is not a wire.
func PinRayOutward(rotation float64, pin, end Point) bool {
	dx, dy, ok := CardinalOutward(rotation)
	if !ok {
		return false
	}
	x, y := end.X-pin.X, end.Y-pin.Y
	return x*dx+y*dy > epsilon && math.Abs(x*dy-y*dx) <= epsilon
}

// SegmentEntersBody checks the full measured bbox, without blanket shrinkage.
func SegmentEntersBody(a, b Point, box BBox) bool { return throughInterior(a, b, box) }

// PinHaloExit is a narrowly scoped exception for a proven OWN pin anchored in
// the measured 0.5raw outline stroke. Callers must first match pin to a wire
// endpoint. It never exempts unrelated wires, perpendicular exits or deep pins.
func PinHaloExit(pin, end Point, box BBox, rotation float64) bool {
	if !PinRayOutward(rotation, pin, end) {
		return false
	}
	dx, dy, _ := CardinalOutward(rotation)
	var depth float64
	switch {
	case dx > 0:
		depth = box.MaxX - pin.X
	case dx < 0:
		depth = pin.X - box.MinX
	case dy > 0:
		depth = box.MaxY - pin.Y
	default:
		depth = pin.Y - box.MinY
	}
	return depth >= -epsilon && depth <= 0.5+epsilon
}

package schguard

import "math"

// SegmentsContact reports physical contact for normalized official wire
// segments. Endpoint-on-segment (including T) and collinear overlap conduct;
// strict interior/interior crossings do not, even for identical net names.
// Callers validate finite nonzero geometry separately. Do not use this rule for
// body, pin-stem, text, or clearance obstacles: those remain geometric tests.
func SegmentsContact(a, b, c, d Point) bool {
	return onSegment(a, c, d) || onSegment(b, c, d) || onSegment(c, a, b) || onSegment(d, a, b)
}

// SegmentsProperlyCross is geometric only; it does not imply electrical union.
func SegmentsProperlyCross(a, b, c, d Point) bool {
	side := func(p, q, r Point) int {
		v := (q.X-p.X)*(r.Y-p.Y) - (q.Y-p.Y)*(r.X-p.X)
		tol := epsilon * math.Max(1, math.Hypot(q.X-p.X, q.Y-p.Y))
		if v > tol {
			return 1
		}
		if v < -tol {
			return -1
		}
		return 0
	}
	x, y, z, w := side(a, b, c), side(a, b, d), side(c, d, a), side(c, d, b)
	return x*y < 0 && z*w < 0
}

// NormalizePolylinePoints models one API create action: the editor removes
// forward-collinear internal vertices. It must NEVER be applied across distinct
// wire actions or to observed independent segment records (their endpoints may
// be actual T junctions). Reversals, duplicate points, and endpoints are kept so
// malformed/retracing input remains visible to validation rather than repaired.
func NormalizePolylinePoints(points []Point) []Point {
	out := make([]Point, 0, len(points))
	for _, p := range points {
		for len(out) >= 2 {
			a, b := out[len(out)-2], out[len(out)-1]
			x, y, u, v := b.X-a.X, b.Y-a.Y, p.X-b.X, p.Y-b.Y
			finite := func(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }
			if !finite(x) || !finite(y) || !finite(u) || !finite(v) ||
				!((x == 0 && u == 0) || (y == 0 && v == 0)) || x*u+y*v <= epsilon {
				break
			}
			out = out[:len(out)-1]
		}
		out = append(out, p)
	}
	return out
}

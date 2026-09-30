package schguard

import (
	"math"
	"reflect"
	"testing"
)

func TestWireContactRelationTransformInvariant(t *testing.T) {
	cases := []struct {
		name           string
		a, b, c, d     Point
		contact, cross bool
	}{
		{"proper-X", Point{-20, 0}, Point{20, 0}, Point{0, -20}, Point{0, 20}, false, true},
		{"T", Point{-20, 0}, Point{20, 0}, Point{0, 0}, Point{0, 20}, true, false},
		{"shared-end", Point{-20, 0}, Point{0, 0}, Point{0, 0}, Point{0, 20}, true, false},
		{"overlap", Point{-20, 0}, Point{20, 0}, Point{0, 0}, Point{30, 0}, true, false},
		{"gap", Point{-20, 0}, Point{0, 0}, Point{5, 0}, Point{30, 0}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for r := 0; r < 4; r++ {
				for reverse := 0; reverse < 2; reverse++ {
					transform := func(p Point) Point {
						for j := 0; j < r; j++ {
							p = Point{-p.Y, p.X}
						}
						return Point{p.X + 105, p.Y - 75}
					}
					a, b, c, d := transform(tc.a), transform(tc.b), transform(tc.c), transform(tc.d)
					if reverse == 1 {
						a, b = b, a
						c, d = d, c
					}
					if SegmentsContact(a, b, c, d) != tc.contact || SegmentsProperlyCross(a, b, c, d) != tc.cross {
						t.Fatalf("r=%d reverse=%d wrong relation", r, reverse)
					}
				}
			}
		})
	}
}

func TestNormalizePolylinePreservesActionEndpointsAndReversals(t *testing.T) {
	in := []Point{{-20, 0}, {0, 0}, {20, 0}}
	before := append([]Point(nil), in...)
	got := NormalizePolylinePoints(in)
	if !reflect.DeepEqual(got, []Point{{-20, 0}, {20, 0}}) || !reflect.DeepEqual(in, before) {
		t.Fatal("normalization changed evidence or kept collinear action vertex")
	}
	if SegmentsContact(got[0], got[1], Point{0, -20}, Point{0, 20}) {
		t.Fatal("normalized midpoint invented a junction")
	}
	// Distinct API records retain their endpoint; normalizing separately must
	// not erase an intentionally created endpoint-on-wire contact.
	left := NormalizePolylinePoints(in[:2])
	if !SegmentsContact(left[0], left[1], Point{0, -20}, Point{0, 20}) {
		t.Fatal("real action endpoint lost")
	}
	for _, p := range [][]Point{{{0, 0}, {10, 0}, {5, 0}}, {{0, 0}, {0, 0}, {10, 0}}, {{0, 0}, {10, 10}, {20, 20}}} {
		if !reflect.DeepEqual(NormalizePolylinePoints(p), p) {
			t.Fatal("malformed/diagonal/retracing evidence silently removed", p)
		}
	}
	bad := NormalizePolylinePoints([]Point{{0, 0}, {math.NaN(), 0}, {20, 0}})
	if len(bad) != 3 {
		t.Fatal("nonfinite evidence hidden")
	}
}

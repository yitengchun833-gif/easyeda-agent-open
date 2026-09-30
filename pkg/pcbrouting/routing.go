// Package pcbrouting provides bounded, deterministic PCB path search without
// editor, CLI, filesystem or network dependencies. All coordinates and options
// must use the same unit. The caller supplies measured geometry and design rules
// through SegmentClear, including trace width, layer and copper-edge clearance.
//
// This kernel currently finds one single-layer, zero-via path. It does not claim
// whole-board connectivity, global optimality, differential-pair or return-path
// correctness. Search exhaustion is not a proof that a physical board is unroutable.
package pcbrouting

import (
	"context"
	"fmt"
	"math"
)

// Point is a position in the caller's common coordinate system.
type Point = [2]float64

// SegmentClear must check the ENTIRE segment, including its copper width and
// clearance, not just its endpoints. A zero-length segment queries a site.
// Unknown geometry must return false (or be rejected before calling Solve).
// The callback must be deterministic and must not mutate the board during a call.
type SegmentClear func(from, to Point) bool

const (
	// Found means a feasible path was found and checked against the input.
	Found = "found"
	// Incomplete includes bounded-search failure and exhausted budgets.
	Incomplete = "incomplete"
	// MaxSearchStates bounds discovered direction-aware states and memory usage.
	MaxSearchStates = 2000000
	maxGridCells    = 250000
	epsilon         = 1e-6
)

// Request describes a single-layer path. MaxDetour bounds Euclidean distance
// from the axis-aligned rectangle spanned by From and To (rounded corners).
// MaxStates bounds discovered (x,y,arrival direction) states, not just positions.
type Request struct {
	From      Point   `json:"from"`
	To        Point   `json:"to"`
	Step      float64 `json:"step"`
	MaxDetour float64 `json:"maxDetour"`
	MaxStates int     `json:"maxStates"`
}

// Result retains search diagnostics. Only Found contains an accepted path.
// Length is centerline length in the input unit; Bends excludes collinear splits.
type Result struct {
	Status string  `json:"status"`
	Reason string  `json:"reason,omitempty"`
	Points []Point `json:"points,omitempty"`
	States int     `json:"states"`
	Length float64 `json:"length"`
	Bends  int     `json:"bends"`
}

func finite(x float64) bool { return !math.IsNaN(x) && !math.IsInf(x, 0) }

// Validate checks the request before any geometry callback or allocation.
func (r Request) Validate() error {
	for _, v := range []float64{r.From[0], r.From[1], r.To[0], r.To[1], r.Step, r.MaxDetour} {
		if !finite(v) {
			return fmt.Errorf("routing coordinates and distances must be finite")
		}
	}
	if r.Step <= epsilon || r.MaxDetour < 0 || r.MaxStates < 1 || r.MaxStates > MaxSearchStates {
		return fmt.Errorf("routing requires step > %g, maxDetour >= 0 and 1 <= maxStates <= %d", epsilon, MaxSearchStates)
	}
	// Prevent overflow and coordinates at which advancing by Step is lost to
	// floating-point rounding. Board unit conversion belongs to the caller.
	for i := range r.From {
		if !finite(r.To[i]-r.From[i]) || !finite(math.Abs(r.To[i]-r.From[i])+2*r.MaxDetour) ||
			r.From[i]+r.Step == r.From[i] || r.To[i]+r.Step == r.To[i] ||
			!finite(r.From[i]+r.Step) || !finite(r.To[i]+r.Step) ||
			!finite(math.Max(math.Abs(r.From[i]), math.Abs(r.To[i]))+r.MaxDetour) {
			return fmt.Errorf("routing coordinate range is not representable at this step")
		}
	}
	if !finite(distance(r.From, r.To)) {
		return fmt.Errorf("routing endpoint distance is not representable")
	}
	return nil
}

func distance(a, b Point) float64 { return math.Hypot(b[0]-a[0], b[1]-a[1]) }

func within(r Request, p Point) bool {
	dx := math.Max(math.Max(math.Min(r.From[0], r.To[0])-p[0], p[0]-math.Max(r.From[0], r.To[0])), 0)
	dy := math.Max(math.Max(math.Min(r.From[1], r.To[1])-p[1], p[1]-math.Max(r.From[1], r.To[1])), 0)
	return math.Hypot(dx, dy) <= r.MaxDetour+epsilon
}

// Check validates a supplied path independently of search. It checks endpoints,
// finiteness, detour, 0/45/90-degree segment directions, at most 45-degree turns,
// and whole-segment clearance. It never trusts solver status or stored metrics.
func Check(ctx context.Context, r Request, path []Point, clear SegmentClear) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if ctx == nil || clear == nil {
		return fmt.Errorf("routing requires a context and segment checker")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(path) == 0 || len(path) > 2*MaxSearchStates+4 {
		return fmt.Errorf("path is empty or exceeds the point limit")
	}
	if distance(path[0], r.From) > epsilon || distance(path[len(path)-1], r.To) > epsilon {
		return fmt.Errorf("path endpoints differ from the request")
	}
	totalLength := 0.0
	for i, p := range path {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !finite(p[0]) || !finite(p[1]) || !within(r, p) {
			return fmt.Errorf("path point %d is non-finite or outside maxDetour", i)
		}
		if i == 0 {
			if !clear(p, p) {
				return fmt.Errorf("path start is blocked")
			}
			continue
		}
		a := path[i-1]
		dx, dy := math.Abs(p[0]-a[0]), math.Abs(p[1]-a[1])
		if !finite(dx) || !finite(dy) || distance(a, p) <= epsilon {
			return fmt.Errorf("path segment %d is degenerate", i-1)
		}
		totalLength += distance(a, p)
		if !finite(totalLength) {
			return fmt.Errorf("path length is not representable")
		}
		if dx > epsilon && dy > epsilon && math.Abs(dx-dy) > epsilon {
			return fmt.Errorf("path segment %d is not straight/45-degree", i-1)
		}
		if i > 1 {
			b := path[i-2]
			ux, uy := (a[0]-b[0])/distance(b, a), (a[1]-b[1])/distance(b, a)
			vx, vy := (p[0]-a[0])/distance(a, p), (p[1]-a[1])/distance(a, p)
			if ux*vx+uy*vy < math.Sqrt(.5)-epsilon {
				return fmt.Errorf("path turn %d exceeds 45 degrees", i-1)
			}
		}
		if !clear(a, p) {
			return fmt.Errorf("path segment %d violates clearance", i-1)
		}
	}
	if !clear(path[len(path)-1], path[len(path)-1]) {
		return fmt.Errorf("path end is blocked")
	}
	return ctx.Err()
}

// Metrics computes centerline length and actual direction changes.
func Metrics(path []Point) (length float64, bends int) {
	p := Compress(path)
	for i := 1; i < len(p); i++ {
		length += distance(p[i-1], p[i])
	}
	return length, max(0, len(p)-2)
}

// Direct45 constructs a symmetric straight/diagonal/straight candidate. It does
// not test obstacles; callers must use Check before accepting it.
func Direct45(from, to Point) []Point {
	dx, dy := to[0]-from[0], to[1]-from[1]
	ax, ay := math.Abs(dx), math.Abs(dy)
	sx, sy := math.Copysign(1, dx), math.Copysign(1, dy)
	points := []Point{from}
	if ax <= epsilon || ay <= epsilon || math.Abs(ax-ay) <= epsilon {
		return Compress(append(points, to))
	}
	if ay > ax {
		straight := (ay - ax) / 2
		points = append(points, Point{from[0], from[1] + sy*straight}, Point{to[0], to[1] - sy*straight})
	} else {
		straight := (ax - ay) / 2
		points = append(points, Point{from[0] + sx*straight, from[1]}, Point{to[0] - sx*straight, to[1]})
	}
	return append(points, to)
}

// Compress copies a path while removing duplicates and forward collinear points.
func Compress(path []Point) []Point {
	var out []Point
	for _, z := range path {
		if len(out) > 0 && distance(z, out[len(out)-1]) < epsilon {
			continue
		}
		for len(out) >= 2 {
			a, b := out[len(out)-2], out[len(out)-1]
			u, v := b[0]-a[0], b[1]-a[1]
			x, y := z[0]-b[0], z[1]-b[1]
			if math.Abs(u*y-v*x) > epsilon || u*x+v*y <= 0 {
				break
			}
			out = out[:len(out)-1]
		}
		out = append(out, z)
	}
	return out
}

// Bevel45 chamfers right-angle junctions when the replacement segment is clear.
// It rejects reversals. Final path/rule verification remains the job of Check.
func Bevel45(path []Point, clear SegmentClear) ([]Point, bool) {
	path = Compress(path)
	if clear == nil || len(path) == 0 {
		return nil, false
	}
	out := []Point{path[0]}
	for i := 1; i+1 < len(path); i++ {
		a, b, z := path[i-1], path[i], path[i+1]
		u, v := b[0]-a[0], b[1]-a[1]
		x, y := z[0]-b[0], z[1]-b[1]
		l, m := math.Hypot(u, v), math.Hypot(x, y)
		cos := (u*x + v*y) / (l * m)
		if cos >= math.Sqrt(.5)-epsilon {
			out = append(out, b)
			continue
		}
		if cos < -epsilon {
			return nil, false
		}
		cut := math.Min(l, m) / 2
		p := Point{b[0] - cut*u/l, b[1] - cut*v/l}
		q := Point{b[0] + cut*x/m, b[1] + cut*y/m}
		if !clear(p, q) {
			return nil, false
		}
		out = append(out, p, q)
	}
	if len(path) > 1 {
		out = append(out, path[len(path)-1])
	}
	out = Compress(out)
	for i := 0; i+1 < len(out); i++ {
		if !clear(out[i], out[i+1]) {
			return nil, false
		}
	}
	return out, true
}

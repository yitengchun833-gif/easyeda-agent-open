package pcbrouting

import (
	"context"
	"fmt"
	"math"
)

const maxOptimizePoints = 4096

// Optimize45 replaces a valid route with a visibility path that minimizes real
// direction changes first and centerline length second. Every replacement edge
// is straight/45-degree, stays inside Request.MaxDetour and is checked by clear.
// Large paths are returned compressed after validation to keep work bounded.
func Optimize45(ctx context.Context, r Request, path []Point, clear SegmentClear) ([]Point, error) {
	if err := Check(ctx, r, path, clear); err != nil {
		return nil, fmt.Errorf("optimize input: %w", err)
	}
	path = Compress(path)
	if len(path) < 3 || len(path) > maxOptimizePoints {
		return path, nil
	}
	type result struct {
		valid  bool
		turns  int
		length float64
		points []Point
	}
	states := make([][9]result, len(path))
	states[0][8] = result{valid: true, points: []Point{path[0]}}
	for i := 0; i+1 < len(path); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for j := i + 1; j < len(path); j++ {
			for _, edge := range visibility45(path[i], path[j]) {
				var dirs []int
				length := 0.0
				ok := true
				for k := 0; k+1 < len(edge); k++ {
					heading, valid := heading45(edge[k], edge[k+1])
					if !valid || !within(r, edge[k]) || !within(r, edge[k+1]) || !clear(edge[k], edge[k+1]) {
						ok = false
						break
					}
					dirs = append(dirs, heading)
					length += distance(edge[k], edge[k+1])
				}
				if !ok || len(dirs) == 0 {
					continue
				}
				edgeTurns := 0
				for k := 1; k < len(dirs); k++ {
					turn := headingDelta(dirs[k-1], dirs[k])
					if turn > 1 {
						ok = false
						break
					}
					if turn > 0 {
						edgeTurns++
					}
				}
				if !ok {
					continue
				}
				for heading, prev := range states[i] {
					if !prev.valid {
						continue
					}
					join := 0
					if heading != 8 {
						join = headingDelta(heading, dirs[0])
						if join > 1 {
							continue
						}
					}
					turns := prev.turns + edgeTurns + join
					d := prev.length + length
					last := dirs[len(dirs)-1]
					existing := states[j][last]
					if existing.valid && (existing.turns < turns || existing.turns == turns && existing.length <= d+epsilon) {
						continue
					}
					points := append([]Point(nil), prev.points...)
					points = append(points, edge[1:]...)
					states[j][last] = result{valid: true, turns: turns, length: d, points: points}
				}
			}
		}
	}
	var best result
	for _, candidate := range states[len(path)-1] {
		if candidate.valid && (!best.valid || candidate.turns < best.turns || candidate.turns == best.turns && candidate.length < best.length-epsilon) {
			best = candidate
		}
	}
	if !best.valid {
		return path, nil
	}
	out := Compress(best.points)
	if err := Check(ctx, r, out, clear); err != nil {
		return nil, fmt.Errorf("optimize output: %w", err)
	}
	return out, nil
}

func visibility45(a, z Point) [][]Point {
	dx, dy := z[0]-a[0], z[1]-a[1]
	d := math.Min(math.Abs(dx), math.Abs(dy))
	sx, sy := math.Copysign(1, dx), math.Copysign(1, dy)
	raw := [][]Point{Direct45(a, z), {a, {a[0] + sx*d, a[1] + sy*d}, z}, {a, {z[0] - sx*d, z[1] - sy*d}, z}}
	out := make([][]Point, 0, len(raw))
	for _, p := range raw {
		p = Compress(p)
		if len(p) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

func heading45(a, z Point) (int, bool) {
	dx, dy := z[0]-a[0], z[1]-a[1]
	if math.Hypot(dx, dy) < epsilon {
		return 0, false
	}
	if math.Abs(dx) > epsilon && math.Abs(dy) > epsilon && math.Abs(math.Abs(dx)-math.Abs(dy)) > epsilon*10 {
		return 0, false
	}
	h := int(math.Round(math.Atan2(dy, dx) / (math.Pi / 4)))
	if h < 0 {
		h += 8
	}
	return h % 8, true
}

func headingDelta(a, b int) int {
	d := int(math.Abs(float64(a - b)))
	if d > 4 {
		return 8 - d
	}
	return d
}

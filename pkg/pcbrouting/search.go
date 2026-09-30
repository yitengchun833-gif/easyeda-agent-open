package pcbrouting

import (
	"container/heap"
	"context"
	"fmt"
	"math"
)

type state struct{ x, y, heading int }
type node struct {
	state
	cost, estimate float64
	sequence       int
}
type queue []node

func (q queue) Len() int { return len(q) }
func (q queue) Less(i, j int) bool {
	if q[i].estimate != q[j].estimate {
		return q[i].estimate < q[j].estimate
	}
	if q[i].cost != q[j].cost {
		return q[i].cost > q[j].cost
	}
	return q[i].sequence < q[j].sequence
}
func (q queue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *queue) Push(v any)   { *q = append(*q, v.(node)) }
func (q *queue) Pop() any     { v := (*q)[len(*q)-1]; *q = (*q)[:len(*q)-1]; return v }

// Solve finds a feasible path using direction-aware A*. It first tries Direct45,
// then a bounded eight-direction grid anchored at From. Every accepted path is
// independently checked, including the off-grid tail to To. It returns Incomplete
// with a reason when a bound is exhausted; invalid inputs and cancellation also
// return an error. This is a feasibility search, not an optimality certificate.
func Solve(ctx context.Context, r Request, clear SegmentClear) (Result, error) {
	result := Result{Status: Incomplete}
	if err := r.Validate(); err != nil {
		result.Reason = "invalid-request"
		return result, err
	}
	if ctx == nil || clear == nil {
		result.Reason = "invalid-request"
		return result, fmt.Errorf("routing requires a context and segment checker")
	}
	canceled := func() (Result, error) { result.Reason = "canceled"; return result, ctx.Err() }
	if ctx.Err() != nil {
		return canceled()
	}
	startClear, endClear := clear(r.From, r.From), clear(r.To, r.To)
	if ctx.Err() != nil {
		return canceled()
	}
	if !startClear || !endClear {
		result.Reason = "endpoint-blocked"
		return result, nil
	}
	found := func(path []Point) Result {
		result.Status, result.Reason, result.Points = Found, "", path
		result.Length, result.Bends = Metrics(path)
		return result
	}
	if p := Direct45(r.From, r.To); Check(ctx, r, p, clear) == nil {
		return found(p), nil
	}
	if ctx.Err() != nil {
		return canceled()
	}
	// Check in floating point before converting to int, including overflow of
	// either division. Large domains are diagnostic, never an unbounded allocation.
	lowX := math.Floor((math.Min(r.From[0], r.To[0]) - r.From[0] - r.MaxDetour) / r.Step)
	lowY := math.Floor((math.Min(r.From[1], r.To[1]) - r.From[1] - r.MaxDetour) / r.Step)
	highX := math.Ceil((math.Max(r.From[0], r.To[0]) - r.From[0] + r.MaxDetour) / r.Step)
	highY := math.Ceil((math.Max(r.From[1], r.To[1]) - r.From[1] + r.MaxDetour) / r.Step)
	cells := (highX - lowX + 1) * (highY - lowY + 1)
	if !finite(cells) || cells > maxGridCells {
		result.Reason = "grid-limit"
		return result, nil
	}
	minX, minY, maxX, maxY := int(lowX), int(lowY), int(highX), int(highY)
	point := func(k state) Point { return Point{r.From[0] + float64(k.x)*r.Step, r.From[1] + float64(k.y)*r.Step} }
	start := state{0, 0, 8}
	dist := map[state]float64{start: 0}
	parent := map[state]state{}
	q := &queue{{state: start, estimate: distance(r.From, r.To)}}
	heap.Init(q)
	result.States = 1
	sequence := 0
	dirs := [][2]int{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}}
	for q.Len() > 0 {
		if ctx.Err() != nil {
			return canceled()
		}
		n := heap.Pop(q).(node)
		key := n.state
		if n.cost > dist[key]+epsilon {
			continue
		}
		p := point(key)
		if distance(p, r.To) <= r.Step*2 {
			tail := Direct45(p, r.To)
			tailClear := true
			for i := 1; i < len(tail); i++ {
				if !clear(tail[i-1], tail[i]) {
					tailClear = false
					break
				}
			}
			if tailClear {
				rev := []Point{p}
				for cursor := key; cursor != start; {
					cursor = parent[cursor]
					rev = append(rev, point(cursor))
				}
				path := make([]Point, 0, len(rev)+len(tail))
				for i := len(rev) - 1; i >= 0; i-- {
					path = append(path, rev[i])
				}
				path = append(path, tail[1:]...)
				if smooth, ok := Bevel45(path, clear); ok && Check(ctx, r, smooth, clear) == nil {
					optimized, err := Optimize45(ctx, r, smooth, clear)
					if err != nil {
						if ctx.Err() != nil {
							return canceled()
						}
						continue
					}
					return found(optimized), nil
				}
			}
		}
		for heading, d := range dirs {
			if n.heading < 8 {
				turn := int(math.Abs(float64(heading - n.heading)))
				if turn > 4 {
					turn = 8 - turn
				}
				if turn > 1 {
					continue
				}
			}
			k := state{n.x + d[0], n.y + d[1], heading}
			if k.x < minX || k.x > maxX || k.y < minY || k.y > maxY {
				continue
			}
			t := point(k)
			if !within(r, t) {
				continue
			}
			cost := n.cost + distance(p, t)
			old, seen := dist[k]
			if seen && cost >= old-epsilon {
				continue
			}
			if !clear(p, t) {
				continue
			}
			if !seen && result.States >= r.MaxStates {
				result.Reason = "state-budget"
				return result, nil
			}
			if !seen {
				result.States++
			}
			dist[k], parent[k] = cost, key
			sequence++
			heap.Push(q, node{state: k, cost: cost, estimate: cost + distance(t, r.To), sequence: sequence})
		}
	}
	if ctx.Err() != nil {
		return canceled()
	}
	result.Reason = "no-path-within-bounds"
	return result, nil
}

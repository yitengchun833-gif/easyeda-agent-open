package app

import (
	"container/heap"
	"fmt"
	"math"
	"sort"
)

type crystalFenceGapError struct {
	Pitch, Length, MaxGap, Start, End float64
	From, To                          [2]float64
	Sites                             int
}

func (e *crystalFenceGapError) Error() string {
	return fmt.Sprintf("fence cannot satisfy %.3fmil pitch over %.3fmil contour (%d legal sites; longest interval %.3fmil from %.3f,%.3f to %.3f,%.3f)", e.Pitch, e.Length, e.Sites, e.MaxGap, e.From[0], e.From[1], e.To[0], e.To[1])
}

// The links are a non-copper contour. Their endpoints are legal via sites;
// interior points need only avoid member-sensitive regions and the board edge.
// This permits crossing a narrow pad row without pretending to place a via on it.
func crystalFenceSitePath(a, z [2]float64, step, detour, pitch, minDistance float64, linkClear func([2]float64, [2]float64) bool, siteClear func([2]float64) bool) ([][2]float64, error) {
	type key [2]int
	point := func(k key) [2]float64 { return [2]float64{a[0] + float64(k[0])*step, a[1] + float64(k[1])*step} }
	start := key{0, 0}
	dist := map[key]float64{start: 0}
	parent := map[key]key{}
	q := &crystalRouteQueue{{estimate: math.Hypot(z[0]-a[0], z[1]-a[1])}}
	heap.Init(q)
	known := map[key]bool{start: true}
	legal := map[key]bool{start: true}
	var hops []key
	for _, d := range []key{{1, 0}, {1, 1}, {0, 1}, {-1, 1}, {-1, 0}, {-1, -1}, {0, -1}, {1, -1}} {
		for n := 1; float64(n)*step*math.Hypot(float64(d[0]), float64(d[1])) <= pitch+1e-6; n++ {
			if float64(n)*step*math.Hypot(float64(d[0]), float64(d[1])) < minDistance-1e-6 {
				continue
			}
			hops = append(hops, key{d[0] * n, d[1] * n})
		}
	}
	for q.Len() > 0 {
		n := heap.Pop(q).(crystalRouteNode)
		k := key{n.x, n.y}
		if n.cost > dist[k]+1e-6 {
			continue
		}
		p := point(k)
		if length := math.Hypot(z[0]-p[0], z[1]-p[1]); length <= pitch+1e-6 && length >= minDistance-1e-6 && linkClear(p, z) {
			rev := [][2]float64{z, p}
			for k != start {
				k = parent[k]
				rev = append(rev, point(k))
			}
			var path [][2]float64
			for i := len(rev) - 1; i >= 0; i-- {
				path = append(path, rev[i])
			}
			return path, nil
		}
		for _, h := range hops {
			v := key{k[0] + h[0], k[1] + h[1]}
			t := point(v)
			if rectPtDist(math.Min(a[0], z[0]), math.Min(a[1], z[1]), math.Max(a[0], z[0]), math.Max(a[1], z[1]), t[0], t[1]) > detour+1e-6 {
				continue
			}
			cost := n.cost + math.Hypot(t[0]-p[0], t[1]-p[1])
			if old, ok := dist[v]; ok && cost >= old-1e-6 {
				continue
			}
			if !known[v] {
				known[v] = true
				legal[v] = siteClear(t)
			}
			if !legal[v] || !linkClear(p, t) {
				continue
			}
			dist[v] = cost
			parent[v] = key{n.x, n.y}
			heap.Push(q, crystalRouteNode{x: v[0], y: v[1], cost: cost, estimate: cost + math.Hypot(z[0]-t[0], z[1]-t[1])})
		}
		if len(dist) > 100000 {
			return nil, fmt.Errorf("via-site search exceeded 100000 states")
		}
	}
	return nil, fmt.Errorf("no legal via-site chain within %.3fmil detour and %.3fmil pitch (%d legal reachable sites)", detour, pitch, len(dist))
}
func crystalReplaceContourInterval(contour [][2]float64, start, end float64, path [][2]float64) [][2]float64 {
	type vertex struct {
		t float64
		p [2]float64
	}
	var vertices []vertex
	length := 0.0
	for i, p := range contour {
		vertices = append(vertices, vertex{length, p})
		q := contour[(i+1)%len(contour)]
		length += math.Hypot(q[0]-p[0], q[1]-p[1])
	}
	var rest []vertex
	for _, v := range vertices {
		for cycle := 0; cycle < 3; cycle++ {
			t := v.t + float64(cycle)*length
			if t > end+1e-6 && t < start+length-1e-6 {
				rest = append(rest, vertex{t, v.p})
			}
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].t < rest[j].t })
	out := append([][2]float64(nil), path...)
	for _, v := range rest {
		out = append(out, v.p)
	}
	return out
}

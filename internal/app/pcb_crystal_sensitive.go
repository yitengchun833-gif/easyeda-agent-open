package app

import (
	"fmt"
	"math"
	"sort"
)

// The union of conservative stroke rectangles protects each full segment plus
// the stated margin without filling the empty rectangle between MCU and crystal.
// Interior holes are intentionally filled: this can over-protect, never omit
// required sensitive copper. Multiple disconnected outers fail closed.
func crystalSensitiveContour(moved map[string]boardComp, routes []pcbModuleRoute, margin, stroke float64) ([][2]float64, error) {
	var rects []layoutBBox
	for _, ref := range sortedCrystalMemberRefs(moved) {
		rects = append(rects, expandLayoutBBox(*moved[ref].BBox, margin))
	}
	for _, r := range routes {
		for i := 0; i+1 < len(r.Points); i++ {
			a, z := r.Points[i], r.Points[i+1]
			n := int(math.Ceil(math.Hypot(z[0]-a[0], z[1]-a[1]) / 10))
			if n < 1 {
				n = 1
			}
			for j := 0; j < n; j++ {
				p := [2]float64{a[0] + float64(j)/float64(n)*(z[0]-a[0]), a[1] + float64(j)/float64(n)*(z[1]-a[1])}
				q := [2]float64{a[0] + float64(j+1)/float64(n)*(z[0]-a[0]), a[1] + float64(j+1)/float64(n)*(z[1]-a[1])}
				rects = append(rects, expandLayoutBBox(pointsBBox([][2]float64{p, q}), margin+stroke))
			}
		}
	}
	return crystalRectangleUnionOuter(rects)
}
func crystalRectangleUnionOuter(rects []layoutBBox) ([][2]float64, error) {
	var xs, ys []float64
	for _, r := range rects {
		xs = append(xs, r.MinX, r.MaxX)
		ys = append(ys, r.MinY, r.MaxY)
	}
	unique := func(a []float64) []float64 {
		sort.Float64s(a)
		var b []float64
		for _, v := range a {
			if len(b) == 0 || v-b[len(b)-1] > 1e-7 {
				b = append(b, v)
			}
		}
		return b
	}
	xs, ys = unique(xs), unique(ys)
	if len(xs)*len(ys) > 1000000 {
		return nil, fmt.Errorf("sensitive union exceeds 1000000 cells")
	}
	cells := make([]bool, len(xs)*len(ys))
	at := func(x, y int) bool {
		if x < 0 || y < 0 || x >= len(xs)-1 || y >= len(ys)-1 {
			return false
		}
		return cells[y*len(xs)+x]
	}
	for _, r := range rects {
		a, b := sort.SearchFloat64s(xs, r.MinX), sort.SearchFloat64s(xs, r.MaxX)
		c, d := sort.SearchFloat64s(ys, r.MinY), sort.SearchFloat64s(ys, r.MaxY)
		for y := c; y < d; y++ {
			for x := a; x < b; x++ {
				cells[y*len(xs)+x] = true
			}
		}
	}
	type key [2]int
	edges := map[key][]key{}
	add := func(a, b key) { edges[a] = append(edges[a], b) }
	for y := 0; y+1 < len(ys); y++ {
		for x := 0; x+1 < len(xs); x++ {
			if !at(x, y) {
				continue
			}
			if !at(x, y-1) {
				add(key{x, y}, key{x + 1, y})
			}
			if !at(x+1, y) {
				add(key{x + 1, y}, key{x + 1, y + 1})
			}
			if !at(x, y+1) {
				add(key{x + 1, y + 1}, key{x, y + 1})
			}
			if !at(x-1, y) {
				add(key{x, y + 1}, key{x, y})
			}
		}
	}
	var outer [][2]float64
	outers := 0
	for len(edges) > 0 {
		var start key
		first := true
		for k := range edges {
			if first || k[0] < start[0] || k[0] == start[0] && k[1] < start[1] {
				start = k
				first = false
			}
		}
		k := start
		var polygon [][2]float64
		for n := 0; n <= len(cells)*4; n++ {
			polygon = append(polygon, [2]float64{xs[k[0]], ys[k[1]]})
			next := edges[k]
			if len(next) != 1 {
				return nil, fmt.Errorf("sensitive union has ambiguous vertex")
			}
			delete(edges, k)
			k = next[0]
			if k == start {
				break
			}
		}
		area := 0.0
		for i, p := range polygon {
			q := polygon[(i+1)%len(polygon)]
			area += p[0]*q[1] - q[0]*p[1]
		}
		if area > 0 {
			outers++
			outer = polygon
		}
	}
	if outers != 1 {
		return nil, fmt.Errorf("sensitive envelope has %d disconnected outer contours", outers)
	}
	// Remove collinear tessellation vertices, including the cyclic seam.
	changed := true
	for changed && len(outer) > 3 {
		changed = false
		for i, b := range outer {
			a, z := outer[(i+len(outer)-1)%len(outer)], outer[(i+1)%len(outer)]
			if math.Abs((b[0]-a[0])*(z[1]-b[1])-(b[1]-a[1])*(z[0]-b[0])) < 1e-7 {
				outer = append(outer[:i], outer[i+1:]...)
				changed = true
				break
			}
		}
	}
	return outer, nil
}

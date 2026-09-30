package app

// crystalRouteQueue remains a small heap primitive for the two board-specific
// searches that carry extra state outside the public pcbrouting kernel: the
// shared-budget multi-net escape planner and the variable-hop fence-site
// planner. Generic one-net path solving uses pkg/pcbrouting.
type crystalRouteNode struct {
	x, y, heading  int
	cost, estimate float64
}

type crystalRouteQueue []crystalRouteNode

func (q crystalRouteQueue) Len() int { return len(q) }
func (q crystalRouteQueue) Less(i, j int) bool {
	if q[i].estimate != q[j].estimate {
		return q[i].estimate < q[j].estimate
	}
	return q[i].cost > q[j].cost
}
func (q crystalRouteQueue) Swap(i, j int) { q[i], q[j] = q[j], q[i] }
func (q *crystalRouteQueue) Push(v any)   { *q = append(*q, v.(crystalRouteNode)) }
func (q *crystalRouteQueue) Pop() any {
	v := (*q)[len(*q)-1]
	*q = (*q)[:len(*q)-1]
	return v
}

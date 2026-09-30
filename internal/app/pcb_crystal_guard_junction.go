package app

import (
	"fmt"
	"math"
	"sort"
)

// splitCrystalGuardAtGroundJunctions materializes every planned same-layer GND
// junction as an endpoint of the guard line that receives it. EasyEDA may split
// an existing track primitive (and replace its primitiveId) when a later track
// ends in that primitive's interior. The crystal apply playbook captures every
// created primitiveId, so leaving such a T junction implicit would make the
// journal stale immediately after the branch is created.
//
// GroundRoutes remain in their original order and keep their route IDs. Only
// role=guard polylines gain deterministic, along-segment junction vertices;
// rebuildCrystalApply consequently emits one captured pcb.line.create per final
// segment. A branch that crosses a guard in its own segment interior is rejected:
// splitting only the guard cannot give that branch a stable captured primitive.
func splitCrystalGuardAtGroundJunctions(bundle *pcbLayoutModuleBundle) error {
	if bundle == nil {
		return fmt.Errorf("crystal guard bundle is missing")
	}
	for guardIndex := range bundle.GroundRoutes {
		guard := bundle.GroundRoutes[guardIndex]
		if guard.Role != "guard" {
			continue
		}
		if len(guard.Points) < 2 {
			return fmt.Errorf("guard route %s has fewer than two points", guard.ID)
		}

		splitAt := make([][]float64, len(guard.Points)-1)
		for branchIndex, branch := range bundle.GroundRoutes {
			if branchIndex == guardIndex || branch.Role == "guard" || branch.Net != guard.Net || branch.Layer != guard.Layer {
				continue
			}
			for guardSegment := 0; guardSegment+1 < len(guard.Points); guardSegment++ {
				a, z := guard.Points[guardSegment], guard.Points[guardSegment+1]
				guardLength := math.Hypot(z[0]-a[0], z[1]-a[1])
				if guardLength <= netPathGeomEps {
					return fmt.Errorf("guard route %s segment %d is degenerate", guard.ID, guardSegment+1)
				}
				guardNode := pcbNetPathNode{kind: "track", x1: a[0], y1: a[1], x2: z[0], y2: z[1]}
				for branchSegment := 0; branchSegment+1 < len(branch.Points); branchSegment++ {
					p, q := branch.Points[branchSegment], branch.Points[branchSegment+1]
					branchNode := pcbNetPathNode{kind: "track", x1: p[0], y1: p[1], x2: q[0], y2: q[1]}
					intersections, err := netPathTrackTrackIntersections(guardNode, branchNode)
					if err != nil {
						return fmt.Errorf("guard route %s segment %d and %s segment %d have an unstable junction: %w", guard.ID, guardSegment+1, branch.ID, branchSegment+1, err)
					}
					for _, intersection := range intersections {
						junction := [2]float64{intersection.x, intersection.y}
						if !crystalPointsClose(junction, p) && !crystalPointsClose(junction, q) {
							return fmt.Errorf("ground route %s segment %d crosses guard %s segment %d away from a planned branch endpoint", branch.ID, branchSegment+1, guard.ID, guardSegment+1)
						}
						t := ((junction[0]-a[0])*(z[0]-a[0]) + (junction[1]-a[1])*(z[1]-a[1])) / (guardLength * guardLength)
						// Existing guard endpoints are already stable junctions.
						if t*guardLength <= netPathGeomEps || (1-t)*guardLength <= netPathGeomEps {
							continue
						}
						splitAt[guardSegment] = append(splitAt[guardSegment], t)
					}
				}
			}
		}

		points := make([][2]float64, 0, len(guard.Points)+4)
		points = append(points, guard.Points[0])
		for segment := 0; segment+1 < len(guard.Points); segment++ {
			a, z := guard.Points[segment], guard.Points[segment+1]
			ts := splitAt[segment]
			sort.Float64s(ts)
			for _, t := range ts {
				junction := [2]float64{a[0] + t*(z[0]-a[0]), a[1] + t*(z[1]-a[1])}
				if crystalPointsClose(junction, points[len(points)-1]) {
					continue
				}
				points = append(points, junction)
			}
			if !crystalPointsClose(points[len(points)-1], z) {
				points = append(points, z)
			}
		}
		bundle.GroundRoutes[guardIndex].Points = points
	}
	return nil
}

func crystalPointsClose(a, b [2]float64) bool {
	return math.Hypot(a[0]-b[0], a[1]-b[1]) <= netPathGeomEps
}

package pcbsolve

import (
	"fmt"
	"reflect"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

// Existing pad/copper contacts are independent witnesses. A displacement may
// not silently detach a feed just because the declared new demands can route.
// Replacing external copper needs a separate, explicit removal/rebuild model.
func checkExistingPadCopper(base, projected pcbmodel.Board) error {
	for ci, c := range base.Components {
		for pi, p := range c.Pads {
			q := projected.Components[ci].Pads[pi]
			padChanged := !reflect.DeepEqual(p, q)
			for i, old := range base.Tracks {
				next := projected.Tracks[i]
				if !padChanged && reflect.DeepEqual(old, next) {
					continue
				}
				if p.Net == old.Net && padTouchesTrack(p, old, 0) && !padTouchesTrack(q, next, 0) {
					return fmt.Errorf("move breaks existing copper contact %s.%s -> track %s", c.Ref, p.Number, old.ID)
				}
				if q.Net != next.Net && padTouchesTrack(q, next, projected.Rules.Clearance) {
					return fmt.Errorf("moved pad/copper clearance fails %s.%s -> track %s", c.Ref, p.Number, next.ID)
				}
			}
			for i, old := range base.Vias {
				next := projected.Vias[i]
				if !padChanged && reflect.DeepEqual(old, next) {
					continue
				}
				if p.Net == old.Net && padPointDistance(p, old.At) <= old.Diameter/2+1e-7 && padPointDistance(q, next.At) > next.Diameter/2+1e-7 {
					return fmt.Errorf("move breaks existing copper contact %s.%s -> via %s", c.Ref, p.Number, old.ID)
				}
				if q.Net != next.Net && padPointDistance(q, next.At) < next.Diameter/2+projected.Rules.Clearance-1e-7 {
					return fmt.Errorf("moved pad/copper clearance fails %s.%s -> via %s", c.Ref, p.Number, next.ID)
				}
			}
			for i, old := range base.Regions {
				next := projected.Regions[i]
				if old.Kind != "copper" || !padChanged && reflect.DeepEqual(old, next) {
					continue
				}
				if p.Net == old.Net && padTouchesRegion(p, old, 0) && !padTouchesRegion(q, next, 0) {
					return fmt.Errorf("move breaks existing copper contact %s.%s -> region %s", c.Ref, p.Number, old.ID)
				}
				if q.Net != next.Net && padTouchesRegion(q, next, projected.Rules.Clearance) {
					return fmt.Errorf("moved pad/copper clearance fails %s.%s -> region %s", c.Ref, p.Number, next.ID)
				}
			}
		}
	}
	for i, t := range projected.Tracks {
		if reflect.DeepEqual(base.Tracks[i], t) {
			continue
		}
		clear := segmentClear(projected, Demand{Net: t.Net, Width: t.Width}, t.Layer, nil, nil)
		for j := 1; j < len(t.Points); j++ {
			if !clear(t.Points[j-1], t.Points[j]) {
				return fmt.Errorf("moved internal track %s violates clearance", t.ID)
			}
		}
	}
	for i, v := range projected.Vias {
		if !reflect.DeepEqual(base.Vias[i], v) {
			// viaClear uses the board's new-via dimensions. Existing dimensions can
			// differ, so substitute this via's measured dimensions for the check.
			measured := projected
			measured.Rules.ViaDiameter = v.Diameter
			measured.Rules.ViaDrill = v.Drill
			if !viaClear(measured, Demand{Net: v.Net}, v.At, nil, nil) {
				return fmt.Errorf("moved internal via %s violates clearance", v.ID)
			}
		}
	}
	return nil
}

func padTouchesTrack(p pcbmodel.Pad, t pcbmodel.Track, clearance float64) bool {
	if !padOnLayer(p, t.Layer) {
		return false
	}
	for i := 1; i < len(t.Points); i++ {
		if padSegmentDistance(p, t.Points[i-1], t.Points[i]) <= t.Width/2+clearance+1e-7 {
			return true
		}
	}
	return false
}

func padTouchesRegion(p pcbmodel.Pad, r pcbmodel.Region, clearance float64) bool {
	if r.Layer != pcbmodel.LayerMulti && !padOnLayer(p, r.Layer) {
		return false
	}
	contours := r.Contours
	if len(contours) == 0 {
		contours = [][]pcbmodel.Point{r.Points}
	}
	if pointInCompound(p.Center, contours) {
		return true
	}
	for _, poly := range contours {
		for i, a := range poly {
			if padSegmentDistance(p, a, poly[(i+1)%len(poly)]) <= clearance+1e-7 {
				return true
			}
		}
	}
	return false
}

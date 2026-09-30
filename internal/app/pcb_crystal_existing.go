package app

import "fmt"

func bindCrystalExistingAnchorVias(b *pcbLayoutModuleBundle, g *pcbCrystalGuardSpec, s *boardSnapshot) error {
	if len(g.ReuseGroundAnchorViaIDs) == 0 {
		return nil
	}
	_, _, vias, err := crystalSnapshotRouting(s)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	pads, err := crystalCandidatePads(s, nil)
	if err != nil {
		return err
	}
	for _, id := range g.ReuseGroundAnchorViaIDs {
		if id == "" || seen[id] {
			return fmt.Errorf("duplicate/empty existing ground anchor via")
		}
		seen[id] = true
		var actual *pcbViaP
		for i := range vias {
			if vias[i].ID == id {
				if actual != nil {
					return fmt.Errorf("duplicate existing via %s", id)
				}
				actual = &vias[i]
			}
		}
		if actual == nil || actual.Net != g.GroundNet {
			return fmt.Errorf("existing ground via %s absent/wrong net", id)
		}
		connected := false
		for _, p := range pads {
			if containsString(g.GroundAnchors, p.Designator+"."+p.Number) && p.Net == actual.Net {
				gap, err := pcbExactPadSegmentGap(p, [2]float64{actual.X, actual.Y}, [2]float64{actual.X, actual.Y})
				if err != nil {
					return err
				}
				connected = connected || gap <= netPathGeomEps
			}
		}
		if !connected {
			return fmt.Errorf("existing via %s is not inside a declared ground anchor", id)
		}
		matches := 0
		for i, v := range b.Vias {
			if v.Role == "ground-anchor" && moduleViaMatchesExpected(*actual, v) {
				b.Vias[i].ExistingPrimitiveID = id
				matches++
			}
		}
		if matches != 1 {
			return fmt.Errorf("existing ground via %s does not match exactly one generated anchor geometry", id)
		}
	}
	return nil
}

func verifyCrystalExistingAnchorVias(b *pcbLayoutModuleBundle, before, after *boardSnapshot) error {
	_, _, prior, err := crystalSnapshotRouting(before)
	if err != nil {
		return err
	}
	_, _, fresh, err := crystalSnapshotRouting(after)
	if err != nil {
		return err
	}
	for _, v := range b.Vias {
		if v.ExistingPrimitiveID == "" {
			continue
		}
		if v.Role != "ground-anchor" {
			return fmt.Errorf("only explicit ground anchors may reuse vias")
		}
		for _, list := range [][]pcbViaP{prior, fresh} {
			matches := 0
			for _, got := range list {
				if got.ID == v.ExistingPrimitiveID && moduleViaMatchesExpected(got, v) {
					matches++
				}
			}
			if matches != 1 {
				return fmt.Errorf("reused anchor via %s missing or changed", v.ExistingPrimitiveID)
			}
		}
	}
	return nil
}

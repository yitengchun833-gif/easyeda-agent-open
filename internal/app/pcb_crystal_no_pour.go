package app

import "fmt"

func verifyCrystalTracksViasProtection(candidate pcbLayoutCandidate, groundNet string) error {
	b := candidate.Bundle
	if b == nil || b.Kind != "crystal-guard" || b.GroundImplementation != "tracks-vias" {
		return nil
	}
	if len(b.Pours) != 0 || len(b.UnreservedPours) != 0 || len(b.AffectedBaselinePours) != 0 {
		return fmt.Errorf("tracks-vias crystal protection must declare zero local/affected pours")
	}
	for _, step := range candidate.Apply.Steps {
		if step.Action == "pcb.pour.create" || step.Action == "pcb.pour.rebuild" {
			return fmt.Errorf("tracks-vias crystal apply contains forbidden %s", step.Action)
		}
	}
	regions := map[int]pcbModuleRegion{}
	for _, r := range b.Regions {
		if !containsString(r.RuleTypes, "no-pours") {
			continue
		}
		if (r.Layer != 1 && r.Layer != 2) || len(regions) >= 2 || len(regions[r.Layer].Points) != 0 {
			return fmt.Errorf("crystal no-pours regions must be exactly one TOP and one BOTTOM object")
		}
		if _, err := buildCompoundPolygonTopology([][][2]float64{r.Points}); err != nil {
			return fmt.Errorf("crystal no-pours region layer %d is invalid: %w", r.Layer, err)
		}
		regions[r.Layer] = r
	}
	if len(regions) != 2 || len(regions[1].Points) == 0 || len(regions[2].Points) == 0 {
		return fmt.Errorf("tracks-vias crystal requires TOP and BOTTOM no-pours regions")
	}
	if !cyclicPolygonPointsNearDirection(removeCollinearPolygonPoints(regions[1].Points), removeCollinearPolygonPoints(regions[2].Points), 1, netPathGeomEps) &&
		!cyclicPolygonPointsNearDirection(removeCollinearPolygonPoints(regions[1].Points), removeCollinearPolygonPoints(regions[2].Points), -1, netPathGeomEps) {
		return fmt.Errorf("crystal TOP/BOTTOM no-pours geometry differs")
	}
	roles, layers := map[string]int{}, map[int]int{}
	for _, r := range b.GroundRoutes {
		if r.Net != groundNet {
			return fmt.Errorf("crystal GND route %s uses %s", r.ID, r.Net)
		}
		roles[r.Role]++
		layers[r.Layer]++
	}
	if roles["guard"] == 0 || roles["ground-entry-top"] < 2 || roles["ground-entry-bottom"] < 2 || layers[1] == 0 || layers[2] == 0 {
		return fmt.Errorf("tracks-vias crystal needs guard plus two separated TOP/BOTTOM routed GND entries")
	}
	fence, anchors := 0, 0
	for _, v := range b.Vias {
		if v.Net != groundNet {
			return fmt.Errorf("crystal via %s uses %s", v.ID, v.Net)
		}
		switch v.Role {
		case "fence":
			fence++
		case "ground-anchor":
			anchors++
		}
	}
	if fence < 2 || anchors < 2 {
		return fmt.Errorf("tracks-vias crystal needs at least two fence and two anchor vias")
	}
	return nil
}

package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
)

type offlineLayoutGroup struct {
	ID           string   `json:"id"`
	PrimitiveIDs []string `json:"primitiveIds"`
}
type offlineLayoutGroupBox struct {
	ID   string     `json:"id"`
	BBox layoutBBox `json:"bbox"`
}

// Reuse the same native-geometry parser and layout predicates without a daemon read.
func offlineLayoutLint(from string, minGap, pinEps float64, strict, asJSON bool, stdout io.Writer) error {
	if err := validateLayoutDistanceFlag("--min-gap", minGap); err != nil {
		return err
	}
	if err := validateLayoutDistanceFlag("--pin-eps", pinEps); err != nil {
		return err
	}
	raw, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	var snapshot map[string]any
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return err
	}
	_, sheet, parts, err := parseAutolayoutPartsChecked(snapshot, true)
	if err != nil {
		return err
	}
	report := analyzeLayout(parts, mmToSchematicUnits(minGap), mmToSchematicUnits(pinEps))
	report.GridViolations = detectOffGridAnchors(parts, schAnchorGrid, acCoordEps)
	report.AnchorGridRaw = schAnchorGrid
	report.InvalidGeometry = invalidLayoutGeometry(parts)
	report.UnprovenPins = unprovenPinGeometry(parts)
	report.UncheckedPins = uncheckedPinGeometry(parts)
	report.SheetCheckStatus = "unavailable"
	report.ZoneCheckStatus = "not-configured"
	if sheet != nil {
		report.SheetCheckStatus = "checked"
		report.OutOfSheet = detectOutOfSheet(parts, *sheet, sheetEdgeMinGap)
	}
	applyLayoutStrictGate(&report, strict)
	var declaration struct {
		Groups []offlineLayoutGroup `json:"groups"`
	}
	if err := json.Unmarshal(raw, &declaration); err != nil {
		return err
	}
	groups := []offlineLayoutGroupBox{}
	claimed := map[string]bool{}
	for _, group := range declaration.Groups {
		members := []alPart{}
		if group.ID == "" || len(group.PrimitiveIDs) == 0 {
			return fmt.Errorf("functional group needs ID and measured members")
		}
		for _, id := range group.PrimitiveIDs {
			if claimed[id] {
				return fmt.Errorf("functional group member is duplicated: %s", id)
			}
			found := false
			for _, part := range parts {
				if part.ID == id && part.BBox != nil {
					members = append(members, alPart{BBox: *part.BBox, HasBBox: true})
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("functional group member is unmeasured: %s", id)
			}
			claimed[id] = true
		}
		groups = append(groups, offlineLayoutGroupBox{ID: group.ID, BBox: unionBoxes(members)})
	}
	groupOverlaps := []layoutFinding{}
	for i, a := range groups {
		for _, b := range groups[i+1:] {
			if boxesOverlap(a.BBox, b.BBox) {
				groupOverlaps = append(groupOverlaps, layoutFinding{A: a.ID, B: b.ID})
			}
		}
	}
	if len(groupOverlaps) > 0 {
		report.OK = false
	}
	if asJSON {
		result := struct {
			layoutReport
			FunctionalGroups []offlineLayoutGroupBox `json:"functionalGroups"`
			GroupOverlaps    []layoutFinding         `json:"groupOverlaps"`
		}{report, groups, groupOverlaps}
		if err := json.NewEncoder(stdout).Encode(result); err != nil {
			return err
		}
	} else {
		renderLayoutReport(report, stdout)
	}
	if !report.OK || (strict && sheet == nil) {
		return fmt.Errorf("offline layout-lint did not pass; inspect geometry, spacing and sheet evidence")
	}
	return nil
}

package app

import "testing"

func TestOwnedProtectionReplacementPreservesUnrelatedCopper(t *testing.T) {
	_, s := escapeFixture()
	track := map[string]any{"primitiveId": "old-ground", "net": "GND", "layer": float64(1), "startX": 20.0, "startY": 20.0, "endX": 30.0, "endY": 20.0, "lineWidth": 8.0}
	other := map[string]any{"primitiveId": "other-ground", "net": "GND", "layer": float64(1), "startX": 30.0, "startY": 30.0, "endX": 40.0, "endY": 30.0, "lineWidth": 8.0}
	pour := map[string]any{"primitiveId": "old-pour", "net": "GND", "layer": float64(1), "geometryAvailable": true, "source": pcbLayoutPolygonSource(rectPoints(layoutBBox{MinX: 10, MinY: 10, MaxX: 40, MaxY: 40}))}
	s.Copper.Lines = []any{track, other}
	s.Copper.Pours = []any{pour}
	s.Copper.Poured = []any{map[string]any{"primitiveId": "materialized", "pourPrimitiveId": "old-pour"}}
	owned := []pcbModuleOwnedObject{{Kind: "track", PrimitiveID: "old-ground", Expected: track, Source: "prior candidate + journal"}, {Kind: "pour", PrimitiveID: "old-pour", Expected: pour, Source: "prior candidate + journal"}}
	out, err := removePCBModuleOwnedObjects(s, owned)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Copper.Lines) != 1 || asString(out.Copper.Lines[0].(map[string]any)["primitiveId"]) != "other-ground" || out.Copper.Pours == nil || len(out.Copper.Poured) != 0 || out.Copper.Poured == nil {
		t.Fatalf("wrong preservation: %+v", out.Copper)
	}
	if len(s.Copper.Lines) != 2 || len(s.Copper.Poured) != 1 {
		t.Fatal("mutated baseline")
	}
	if _, err := verifyPCBModuleOwnedRemoved(s, s, owned); err == nil {
		t.Fatal("old objects still present accepted")
	}
	if _, err := verifyPCBModuleOwnedRemoved(s, out, owned); err != nil {
		t.Fatal(err)
	}
	if _, err := removePCBModuleOwnedObjects(out, owned); err == nil {
		t.Fatal("blind replay accepted")
	}
	changed := append([]pcbModuleOwnedObject(nil), owned...)
	changed[0].Expected = map[string]any{"primitiveId": "old-ground", "net": "OTHER"}
	if _, err := removePCBModuleOwnedObjects(s, changed); err == nil {
		t.Fatal("changed state accepted")
	}
	if _, err := removePCBModuleOwnedObjects(s, append(owned, owned[0])); err == nil {
		t.Fatal("duplicate ownership accepted")
	}
	s.Copper.Poured[0].(map[string]any)["pourPrimitiveId"] = ""
	if _, err := removePCBModuleOwnedObjects(s, owned); err == nil {
		t.Fatal("unknown parent accepted")
	}
}

func TestMaterializedUnitsMustBeExplicitNotInferredFromBoundary(t *testing.T) {
	b := map[string]any{"source": pcbLayoutPolygonSource(rectPoints(layoutBBox{MinX: 100, MinY: 100, MaxX: 200, MaxY: 200}))}
	f := map[string]any{"source": pcbLayoutPolygonSource(rectPoints(layoutBBox{MinX: 11, MinY: 11, MaxX: 19, MaxY: 19})), "lineWidth": 0.0, "fill": true}
	m := map[string]any{"fills": []any{f}}
	if _, err := normalizeMaterializedPourMap(b, m); err == nil {
		t.Fatal("bbox-based guessed units accepted")
	}
	f["sourceUnits"] = "mil"
	f["lineWidthUnits"] = "mil"
	f["arcSweepUnits"] = "degree"
	out, err := normalizeMaterializedPourMap(b, m)
	if err != nil {
		t.Fatal(err)
	}
	if canonicalJSON(out) != canonicalJSON(m) {
		t.Fatal("explicit mil geometry rescaled based on enclosing boundary")
	}
}

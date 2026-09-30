package app

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestBoardSnapshotSemanticHashIgnoresCaptureMetadataOnly(t *testing.T) {
	s := lpSnapshot(lpComp("u1", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110)))
	s.CapturedAt = "2026-09-22T01:00:00Z"
	s.SemanticSHA256 = "old-self-hash"
	h1, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	s.CapturedAt = "2026-09-22T02:00:00Z"
	s.Project = "renamed-capture-label"
	s.SemanticSHA256 = "another-self-hash"
	h2, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("capture metadata changed semantic hash: %s != %s", h1, h2)
	}
	s.Components[0].X++
	h3, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	if h3 == h2 {
		t.Fatal("component geometry/identity change did not change semantic hash")
	}
}

func TestBoardSnapshotSemanticHashSurvivesDumpRoundTripWithProjectLabel(t *testing.T) {
	s := lpSnapshot(lpComp("u1", "U1", 100, 100, 0, lpBBox(90, 90, 110, 110)))
	h, err := boardSnapshotSemanticSHA256(s)
	if err != nil {
		t.Fatal(err)
	}
	// cmd_pcb_dump attaches this provenance label after the geometry fetch.
	s.Project = "ceshi"
	s.SemanticSHA256 = h
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := loadBoardSnapshotFile(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	got, err := boardSnapshotSemanticSHA256(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SemanticSHA256 != got {
		t.Fatalf("dump invalidated its own semantic hash: stored=%s recomputed=%s", loaded.SemanticSHA256, got)
	}
}

func TestCopperSnapshotAvailabilityDistinguishesKnownEmptyFromUnknown(t *testing.T) {
	knownEmpty := &boardSnapshot{Copper: &boardCopperSnapshot{
		Availability: map[string]string{"routing": "available", "vias": "available", "pours": "available", "poured": "available", "regions": "available", "fills": "available"},
		Lines:        []any{}, Arcs: []any{}, Vias: []any{}, Pours: []any{}, Poured: []any{}, Regions: []any{}, Fills: []any{},
	}}
	available := true
	knownEmpty.Copper.ArcsAvailable = &available
	if got := knownEmpty.Copper.Availability["poured"]; got != "available" || knownEmpty.Copper.Poured == nil {
		t.Fatalf("known-empty materialized copper lost: availability=%q list=%#v", got, knownEmpty.Copper.Poured)
	}
	unknown := &boardSnapshot{Copper: &boardCopperSnapshot{Availability: map[string]string{"poured": "unknown"}}}
	if unknown.Copper.Availability["poured"] != "unknown" {
		t.Fatalf("unknown availability=%q", unknown.Copper.Availability["poured"])
	}
}

func TestBoardSnapshotPreservesExactPadShapeAndSpecialPad(t *testing.T) {
	shape := []any{"RECT", 20.0, 12.0, 2.0}
	special := []any{"L", 1.0, 2.0}
	components := parseBoardComponents(componentsResult(map[string]any{
		"primitiveId": "u1", "designator": "U1", "layer": 1.0,
		"pads": []any{map[string]any{
			"primitiveId": "p1", "padNumber": "1", "net": "GND", "layer": 1.0,
			"x": 10.0, "y": 20.0, "width": 20.0, "height": 12.0,
			"shape": shape, "specialPad": special,
		}},
	}))
	if len(components) != 1 || len(components[0].Pads) != 1 {
		t.Fatalf("components=%+v", components)
	}
	pad := components[0].Pads[0]
	if canonicalJSON(pad.Shape) != canonicalJSON(shape) || canonicalJSON(pad.SpecialPad) != canonicalJSON(special) {
		t.Fatalf("pad geometry was dropped: shape=%#v special=%#v", pad.Shape, pad.SpecialPad)
	}
}

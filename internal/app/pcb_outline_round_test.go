package app

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestRoundedRectSourceUsesFourNativeArcs(t *testing.T) {
	got := roundedRectSource(0, 0, 100, 80, 10)
	want := []any{
		10.0, 0.0,
		"L", 90.0, 0.0,
		"ARC", 90.0, 100.0, 10.0,
		"L", 100.0, 70.0,
		"ARC", 90.0, 90.0, 80.0,
		"L", 10.0, 80.0,
		"ARC", 90.0, 0.0, 70.0,
		"L", 0.0, 10.0,
		"ARC", 90.0, 10.0, 0.0,
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("source=%s\nwant=%s", gotJSON, wantJSON)
	}
}

func TestRoundedRectSourceRadiusClampedAndSharpFallback(t *testing.T) {
	// Radius larger than half the short side is clamped (100×80 → max r = 40).
	_, _, _, _, r := normalizeRoundedRect(0, 0, 100, 80, 999)
	if r != 40 {
		t.Fatalf("radius=%v, want 40", r)
	}
	source := roundedRectSource(0, 0, 100, 80, 999)
	arcCount := 0
	for _, token := range source {
		if token == "ARC" {
			arcCount++
		}
	}
	if arcCount != 4 {
		t.Fatalf("ARC tokens=%d, want 4", arcCount)
	}
	sharp := roundedRectSource(0, 0, 10, 10, 0)
	for _, token := range sharp {
		if token == "ARC" {
			t.Fatal("zero-radius rectangle must not emit degenerate ARC tokens")
		}
	}
}

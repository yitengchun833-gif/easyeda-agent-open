package app

import (
	"math"
	"testing"
)

func TestCrystalStringPullRemovesStaircase(t *testing.T) {
	path := [][2]float64{{0, 0}, {2, 0}, {4, 2}, {6, 2}, {8, 4}, {10, 4}, {12, 6}}
	got := crystalStringPull45(path, 20, func(a, b [2]float64) bool { return true })
	if moduleRouteTurns(got) != 1 || len(got) != 3 {
		t.Fatalf("expected one-turn path, got %v", got)
	}
	if math.Abs(moduleRouteLength(got)-(6+6*math.Sqrt2)) > 1e-6 {
		t.Fatalf("unexpected measured length: %v", got)
	}
}

func TestCrystalStringPullChecksEveryShortcutAgainstObstacle(t *testing.T) {
	path := [][2]float64{{0, 0}, {2, 2}, {4, 2}, {6, 0}}
	checks := 0
	clear := func(a, b [2]float64) bool {
		checks++
		// A circular obstacle blocks the apparent straight shortcut.
		return segPtDist(3, 0, a[0], a[1], b[0], b[1]) >= 1.25
	}
	got := crystalStringPull45(path, 3, clear)
	if checks == 0 || moduleRouteTurns(got) < 2 {
		t.Fatalf("shortcut crossed obstacle: %v (%d checks)", got, checks)
	}
	for i := 1; i < len(got); i++ {
		if !clear(got[i-1], got[i]) {
			t.Fatalf("unsafe output segment: %v", got)
		}
		if _, ok := crystal45Heading(got[i-1], got[i]); !ok {
			t.Fatalf("non-45-degree output: %v", got)
		}
	}
}

func TestCrystalStringPullPrefersTurnsBeforeLength(t *testing.T) {
	// The valid outer path has four turns. Shortcuts between its existing
	// vertices may shorten it, but cannot add turns to win on length alone.
	path := [][2]float64{{0, 0}, {2, 0}, {6, 4}, {10, 4}, {14, 0}, {16, 0}}
	clear := func(a, b [2]float64) bool {
		return segPtDist(8, 0, a[0], a[1], b[0], b[1]) >= 3
	}
	got := crystalStringPull45(path, 5, clear)
	if moduleRouteTurns(got) > moduleRouteTurns(path) || moduleRouteLength(got) > moduleRouteLength(path)+1e-6 {
		t.Fatalf("optimization regressed valid path: %v", got)
	}
	for i := 1; i+1 < len(got); i++ {
		a, okA := crystal45Heading(got[i-1], got[i])
		b, okB := crystal45Heading(got[i], got[i+1])
		if !okA || !okB || crystalHeadingDelta(a, b) > 1 {
			t.Fatalf("sharp turn introduced: %v", got)
		}
	}
}

func TestCrystalStringPullDoesNotInventUncheckedFallback(t *testing.T) {
	path := [][2]float64{{0, 0}, {3, 3}, {6, 3}}
	got := crystalStringPull45(path, 0, func(a, b [2]float64) bool { return false })
	if len(got) != len(path) {
		t.Fatalf("rejected shortcuts must preserve input: %v", got)
	}
	for i := range path {
		if got[i] != path[i] {
			t.Fatalf("rejected shortcuts changed input: %v", got)
		}
	}
}

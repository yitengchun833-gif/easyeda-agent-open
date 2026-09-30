package app

import (
	"strings"
	"testing"
)

func TestAtSizeToRectSpec(t *testing.T) {
	t.Run("basic conversion", func(t *testing.T) {
		got, err := atSizeToRectSpec("100,-200", "250,150")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "100,-200,350,-50" {
			t.Fatalf("got %q, want %q", got, "100,-200,350,-50")
		}
	})
	t.Run("spaces tolerated", func(t *testing.T) {
		got, err := atSizeToRectSpec(" 0 , 0 ", " 10 , 20 ")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "0,0,10,20" {
			t.Fatalf("got %q, want %q", got, "0,0,10,20")
		}
	})
	t.Run("at without size", func(t *testing.T) {
		if _, err := atSizeToRectSpec("100,100", ""); err == nil {
			t.Fatal("want error when --size missing")
		}
	})
	t.Run("size without at", func(t *testing.T) {
		if _, err := atSizeToRectSpec("", "10,10"); err == nil {
			t.Fatal("want error when --at missing")
		}
	})
	t.Run("non-numeric", func(t *testing.T) {
		if _, err := atSizeToRectSpec("a,b", "10,10"); err == nil {
			t.Fatal("want error for non-numeric --at")
		}
	})
	t.Run("wrong arity", func(t *testing.T) {
		if _, err := atSizeToRectSpec("1,2,3", "10,10"); err == nil {
			t.Fatal("want error for 3-value --at")
		}
	})
	t.Run("non-positive size", func(t *testing.T) {
		if _, err := atSizeToRectSpec("0,0", "-10,10"); err == nil {
			t.Fatal("want error for negative width")
		}
		if _, err := atSizeToRectSpec("0,0", "10,0"); err == nil {
			t.Fatal("want error for zero height")
		}
	})
}

func TestCheckFillAreaGuard(t *testing.T) {
	// esp32Mini-scale board: 3100 x 2200 mil = 6,820,000 mil²; 25% = 1,705,000.
	board := 3100.0 * 2200.0
	small := rectCorners(2150, -1550, 2400, -1400) // 250x150 = 37,500 mil²
	huge := rectCorners(100, -2100, 2100, -300)    // 2000x1800 = 3,600,000 mil²
	medium := rectCorners(0, 0, 1900, 2000)        // 3,800,000 mil² (< 4M abs cap)
	overAbs := rectCorners(0, 0, 2100, 2000)       // 4,200,000 mil² (> 4M abs cap)

	t.Run("small fill passes with board", func(t *testing.T) {
		if err := checkFillAreaGuard(small, board, true, false); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("huge fill rejected with board", func(t *testing.T) {
		err := checkFillAreaGuard(huge, board, true, false)
		if err == nil {
			t.Fatal("want rejection for fill > 25% of board bbox")
		}
		if !strings.Contains(err.Error(), "--force-large") {
			t.Fatalf("error should mention --force-large: %v", err)
		}
		if !strings.Contains(err.Error(), "x0,y0,x1,y1") {
			t.Fatalf("error should teach the two-corner semantics: %v", err)
		}
	})
	t.Run("force-large bypasses", func(t *testing.T) {
		if err := checkFillAreaGuard(huge, board, true, true); err != nil {
			t.Fatalf("--force-large must bypass the guard: %v", err)
		}
		if err := checkFillAreaGuard(overAbs, 0, false, true); err != nil {
			t.Fatalf("--force-large must bypass the absolute cap too: %v", err)
		}
	})
	t.Run("no board outline uses absolute cap", func(t *testing.T) {
		if err := checkFillAreaGuard(medium, 0, false, false); err != nil {
			t.Fatalf("3.8M mil² under the 4M abs cap should pass: %v", err)
		}
		if err := checkFillAreaGuard(overAbs, 0, false, false); err == nil {
			t.Fatal("4.2M mil² over the 4M abs cap should be rejected")
		}
	})
	t.Run("board fraction stricter than abs cap", func(t *testing.T) {
		// medium (3.8M) is under the abs cap but over 25% of this board.
		if err := checkFillAreaGuard(medium, board, true, false); err == nil {
			t.Fatal("3.8M mil² on a 6.82M board (55%) should be rejected")
		}
	})
	t.Run("at+size conversion feeds the guard", func(t *testing.T) {
		// The x,y,w,h trap scenario: intended a 250x150 fill at (2150,-1550);
		// as two corners that is what --at/--size produces — small, passes.
		spec, err := atSizeToRectSpec("2150,-1550", "250,150")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		x0, y0, x1, y1, err := parseRectSpec(spec)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := checkFillAreaGuard(rectCorners(x0, y0, x1, y1), board, true, false); err != nil {
			t.Fatalf("intended small fill must pass: %v", err)
		}
	})
}

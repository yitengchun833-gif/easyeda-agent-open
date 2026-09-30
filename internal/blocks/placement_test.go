package blocks

import (
	"encoding/json"
	"testing"
)

// TestPlacementDocKeyDoesNotDropBlock reproduces the exact bug where a block whose
// `placement` (or `parts`) carries a schema `_doc` STRING key made the old
// map[string]PlacementHint unmarshal fail → the WHOLE block's hints were silently
// dropped. The RawMessage maps + per-key parse must tolerate it.
func TestPlacementDocKeyDoesNotDropBlock(t *testing.T) {
	raw := []byte(`{"parts":{"_doc":"x","SW1":{"part":"sw.tact"}},"placement":{"_doc":"doc string","SW1":{"board_edge":true,"edge":"user-facing"}}}`)
	var bpr blockPlacementRaw
	if err := json.Unmarshal(raw, &bpr); err != nil {
		t.Fatalf("blockPlacementRaw must unmarshal a block with _doc keys; got %v", err)
	}
	sw1, ok := bpr.Placement["SW1"]
	if !ok {
		t.Fatal("SW1 placement entry missing")
	}
	var h PlacementHint
	if err := json.Unmarshal(sw1, &h); err != nil || !h.BoardEdge {
		t.Errorf("SW1 hint must parse (board_edge=true); err=%v h=%+v", err, h)
	}
}

func TestLoadPlacementIndex(t *testing.T) {
	idx, err := LoadPlacementIndex()
	if err != nil {
		t.Fatalf("LoadPlacementIndex: %v", err)
	}

	// JP701 (RS485 120R terminator jumper) is board_edge=false + anchor=true in
	// the RS485 block — the exact misfire issue #95 fixes. Its DISTINCTIVE "JP"
	// prefix must resolve to that anchored, non-edge hint (a placed part never
	// exposes the block role-id, so the prefix is what the placer matches on).
	jp, ok := idx.ByRefPrefix["JP"]
	if !ok {
		t.Fatal("JP prefix (JP701) missing from ByRefPrefix index")
	}
	if jp.BoardEdge {
		t.Errorf("JP701 hint should be board_edge=false; got true")
	}
	if !jp.Anchor {
		t.Errorf("JP701 hint should be anchor=true (deliberate non-edge); got false")
	}

	// ANT (RF u.FL) is a board-edge part.
	if ant, ok := idx.ByRefPrefix["ANT"]; !ok || !ant.BoardEdge {
		t.Errorf("ANT prefix should be indexed board_edge=true; ok=%v edge=%v", ok, ant.BoardEdge)
	}
	// SW is a CONFLICTED prefix across blocks (tactile buttons board_edge=false vs
	// axp2101's power button board_edge=true) → the index must DROP it (don't guess
	// an ambiguous prefix); the regex fallback still resolves SW* → user-facing.
	if _, ok := idx.ByRefPrefix["SW"]; ok {
		t.Errorf("SW prefix must be dropped (conflicting board_edge across blocks); still indexed")
	}

	// Generic single-letter prefixes must NOT be indexed (would misfile whole
	// component classes — e.g. snapping every U* IC to a board edge).
	for _, p := range []string{"U", "C", "R", "X", "J"} {
		if _, ok := idx.ByRefPrefix[p]; ok {
			t.Errorf("generic prefix %q must not be in ByRefPrefix", p)
		}
	}
}

func TestRefPrefix(t *testing.T) {
	cases := map[string]string{"JP701": "JP", "J4": "J", "SW_BOOT": "SW", "LED1": "LED", "701": "", "": ""}
	for in, want := range cases {
		if got := refPrefix(in); got != want {
			t.Errorf("refPrefix(%q)=%q, want %q", in, got, want)
		}
	}
}

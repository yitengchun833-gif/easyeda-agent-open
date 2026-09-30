package schguard

import (
	"strings"
	"testing"
)

func topologySnapshot(lines ...[]float64) map[string]any {
	wires := []any{}
	for _, p := range lines {
		wires = append(wires, map[string]any{"points": p})
	}
	return map[string]any{"wiresAvailable": true, "wires": wires}
}

func TestVerifyWireContactTopology(t *testing.T) {
	h, v := []float64{-20, 0, 20, 0}, []float64{0, -20, 0, 20}
	before := topologySnapshot(h)
	proposal := map[string]any{"points": v}
	for _, tc := range []struct {
		name  string
		after map[string]any
		want  string
	}{
		{"proper-cross", topologySnapshot(h, v), ""},
		{"reversed", topologySnapshot([]float64{20, 0, -20, 0}, []float64{0, 20, 0, -20}), ""},
		{"split-away-from-cross", topologySnapshot([]float64{-20, 0, -10, 0}, []float64{-10, 0, 20, 0}, v), ""},
		{"unexpected-junction", topologySnapshot([]float64{-20, 0, 0, 0}, []float64{0, 0, 20, 0}, v), "merged"},
		{"unexpected-gap", topologySnapshot([]float64{-20, 0, -5, 0}, []float64{5, 0, 20, 0}, v), "split"},
		{"unavailable", map[string]any{"wiresAvailable": false}, "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := VerifyWireTopology(before, tc.after, proposal)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("got %v; want %q", err, tc.want)
			}
		})
	}
}

func TestVerifyWireTopologyTAndCollinearVertex(t *testing.T) {
	h := []float64{-20, 0, 20, 0}
	before := topologySnapshot(h)
	// An explicit separate action endpoint is a real T, independent of names.
	top := []float64{0, 0, 0, 20}
	if err := VerifyWireTopology(before, topologySnapshot([]float64{-20, 0, 0, 0}, []float64{0, 0, 20, 0}, top), map[string]any{"points": top}); err != nil {
		t.Fatal(err)
	}
	// A redundant middle coordinate inside ONE create is simplified by EDA.
	if err := VerifyWireTopology(before, topologySnapshot(h, []float64{0, -20, 0, 20}), map[string]any{"points": []float64{0, -20, 0, 0, 0, 20}}); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyWireTopologyCannotLoseExistingJunction(t *testing.T) {
	before := topologySnapshot([]float64{-20, 0, 0, 0}, []float64{0, 0, 20, 0}, []float64{0, -20, 0, 20})
	added := []float64{20, 0, 30, 0}
	after := topologySnapshot([]float64{-20, 0, 30, 0}, []float64{0, -20, 0, 20})
	if err := VerifyWireTopology(before, after, map[string]any{"points": added}); err == nil || !strings.Contains(err.Error(), "split") {
		t.Fatalf("lost existing connected X: %v", err)
	}
}

package connectivity

import (
	"math"
	"testing"
)

func TestDesignSinglePageMembershipIsDerivedOnlyFromDocument(t *testing.T) {
	expected := designFixture()
	actual := cloneDesignatorDocument(expected)
	for i := range actual.Components {
		actual.Components[i].PageID = ""
	}
	diff, err := CompareDesignEvidence(designEvidence(t, expected), designEvidence(t, actual))
	if err != nil || diff.Status != "synced" || len(diff.Changes) != 0 || diff.ExpectedRevision != diff.ActualRevision {
		t.Fatalf("single-page membership not equivalent: %+v %v", diff, err)
	}
	if actual.Components[0].PageID != "" {
		t.Fatal("normalization mutated original component")
	}
	actual.Components[0].PageID = "other-page"
	if _, err := DecodeDesignEvidence(designRaw(t, actual)); err == nil {
		t.Fatal("explicit opposing page accepted")
	}
	actual.Components[0].PageID = ""
	actual.DocumentID = ""
	if _, err := DecodeDesignEvidence(designRaw(t, actual)); err == nil {
		t.Fatal("missing page inferred without a single-page document")
	}
	actual.DocumentID = "other-page"
	diff, err = CompareDesignEvidence(designEvidence(t, expected), designEvidence(t, actual))
	if err != nil || diff.Status != "wrong-target" {
		t.Fatalf("wrong document normalized away: %+v %v", diff, err)
	}
	// In an all-pages snapshot, explicit per-component membership stays meaningful.
	expected.DocumentID, actual.DocumentID = "", ""
	for i := range actual.Components {
		actual.Components[i].PageID = "page"
	}
	actual.Components[0].PageID = "other-page"
	diff, err = CompareDesignEvidence(designEvidence(t, expected), designEvidence(t, actual))
	if err != nil || diff.Status != "different" || len(diff.Changes) != 1 || diff.Changes[0].Path != "/components/a/pageId" {
		t.Fatalf("true per-component page move missed: %+v %v", diff, err)
	}
}

func TestDesignReadbackRoundoffUsesStableRevisionPrecision(t *testing.T) {
	a := designFixture()
	a.Components[0].Placement.BBox.MinX = 4.5
	b := cloneDesignatorDocument(a)
	b.Components[0].Placement.BBox.MinX = 4.50000000000001
	b.Components[0].Placement.Y = math.Nextafter(20, math.Inf(1))
	b.Components[0].Pins[0].X = math.Nextafter(5, math.Inf(-1))
	diff, err := CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
	if err != nil || diff.Status != "synced" || len(diff.Changes) != 0 || diff.ExpectedRevision != diff.ActualRevision {
		t.Fatalf("API arithmetic tails changed content: %+v %v", diff, err)
	}
	for _, delta := range []float64{5, 0.01, 1e-6, 1e-9} {
		b = cloneDesignatorDocument(a)
		b.Components[0].Placement.X += delta
		diff, err = CompareDesignEvidence(designEvidence(t, a), designEvidence(t, b))
		if err != nil || diff.Status != "different" || len(diff.Changes) != 1 || diff.ExpectedRevision == diff.ActualRevision {
			t.Fatalf("real movement %g disappeared: %+v %v", delta, diff, err)
		}
	}
}

func TestDesignNumberQuantizationBoundaryAndIdempotence(t *testing.T) {
	// This contract is fixed decimal quantization, not a nontransitive epsilon.
	if NormalizeDesignNumber(10+0.49e-9) != 10 || NormalizeDesignNumber(10+0.51e-9) == 10 {
		t.Fatal("nine-decimal quantization boundary changed")
	}
	for _, value := range []float64{math.Copysign(0, -1), 318.5000000000001, 535.4999999999999, -424.5000000000001, 1e-6, 1e9 + 0.001, math.MaxFloat64} {
		n := NormalizeDesignNumber(value)
		if n != NormalizeDesignNumber(n) || math.IsInf(n, 0) || math.IsNaN(n) {
			t.Fatalf("non-idempotent or overflowing normalization of %g: %g", value, n)
		}
		if n == 0 && math.Signbit(n) {
			t.Fatal("negative zero retained")
		}
	}
}

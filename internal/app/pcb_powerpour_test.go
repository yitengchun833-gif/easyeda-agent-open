package app

import (
	"reflect"
	"testing"
)

func TestPlanPowerPour(t *testing.T) {
	pads := []pcbPadP{
		{Net: "GND", X: 100, Y: 100}, {Net: "GND", X: 500, Y: 500}, {Net: "GND", X: 900, Y: 900},
		{Net: "3V3", X: 200, Y: 200}, {Net: "3V3", X: 300, Y: 300},
		{Net: "+5V", X: 700, Y: 100}, {Net: "+5V", X: 800, Y: 200},
		{Net: "SDA", X: 400, Y: 400}, {Net: "SDA", X: 450, Y: 450}, // signal — ignored
		{Net: "VREF", X: 600, Y: 600}, // power but single pad — skipped
	}
	outline := [4]float64{0, 0, 1000, 1000}
	plans := planPowerPour(pads, outline, []int{1, 2}, "pour", 60)

	// GND first, on both layers, full outline.
	if len(plans) != 4 {
		t.Fatalf("got %d plans, want 4 (GND×2 + 3V3 + 5V); plans=%+v", len(plans), plans)
	}
	if plans[0].Net != "GND" || plans[0].Kind != "gnd-plane" || plans[1].Net != "GND" {
		t.Errorf("GND must be planned first on both layers; got %+v, %+v", plans[0], plans[1])
	}
	gndLayers := map[int]bool{plans[0].Layer: true, plans[1].Layer: true}
	if !gndLayers[1] || !gndLayers[2] {
		t.Errorf("GND must pour on layers 1 and 2; got %v", gndLayers)
	}
	wantOutline := [][]float64{{0, 0}, {1000, 0}, {1000, 1000}, {0, 1000}}
	if !reflect.DeepEqual(plans[0].Points, wantOutline) {
		t.Errorf("GND points = %v, want full outline %v", plans[0].Points, wantOutline)
	}

	// Rails: local bbox + margin, clamped, on layer 1.
	rails := map[string]powerPourPlan{}
	for _, p := range plans {
		if p.Kind == "rail-local" {
			rails[p.Net] = p
		}
	}
	if len(rails) != 2 {
		t.Fatalf("want 2 rail-local plans (3V3, +5V); got %v", rails)
	}
	want3v3 := [][]float64{{140, 140}, {360, 140}, {360, 360}, {140, 360}}
	if r := rails["3V3"]; r.Layer != 1 || !reflect.DeepEqual(r.Points, want3v3) {
		t.Errorf("3V3 rail = %+v, want layer 1 points %v", r, want3v3)
	}
	if _, ok := rails["SDA"]; ok {
		t.Error("SDA is a signal — must not be poured")
	}
	if _, ok := rails["VREF"]; ok {
		t.Error("VREF has a single pad — must be skipped")
	}
}

func TestPlanPowerPourRailsSkipAndClamp(t *testing.T) {
	pads := []pcbPadP{
		{Net: "GND", X: 50, Y: 50}, {Net: "GND", X: 60, Y: 60},
		{Net: "3V3", X: 10, Y: 10}, {Net: "3V3", X: 20, Y: 20}, // near edge → margin clamps to outline
	}
	outline := [4]float64{0, 0, 500, 500}

	// rails=skip → only GND (one layer here).
	skip := planPowerPour(pads, outline, []int{2}, "skip", 60)
	if len(skip) != 1 || skip[0].Net != "GND" || skip[0].Layer != 2 {
		t.Fatalf("rails=skip should yield one GND-bottom plan; got %+v", skip)
	}

	// rails=pour → 3V3 local rect clamped so it never leaves the inset outline.
	pour := planPowerPour(pads, outline, []int{2}, "pour", 60)
	var r *powerPourPlan
	for i := range pour {
		if pour[i].Net == "3V3" {
			r = &pour[i]
		}
	}
	if r == nil {
		t.Fatal("expected a 3V3 rail plan")
	}
	// x0/y0 clamp to 0 (10-60 < 0); x1/y1 = 20+60 = 80.
	want := [][]float64{{0, 0}, {80, 0}, {80, 80}, {0, 80}}
	if !reflect.DeepEqual(r.Points, want) {
		t.Errorf("clamped 3V3 rect = %v, want %v", r.Points, want)
	}
}

func TestParseGndLayers(t *testing.T) {
	cases := []struct {
		spec string
		want []int
		err  bool
	}{
		{"both", []int{1, 2}, false}, {"", []int{1, 2}, false},
		{"top", []int{1}, false}, {"bottom", []int{2}, false},
		{"inner", nil, true},
	}
	for _, c := range cases {
		got, err := parseGndLayers(c.spec)
		if (err != nil) != c.err || (!c.err && !reflect.DeepEqual(got, c.want)) {
			t.Errorf("parseGndLayers(%q) = (%v, %v), want (%v, err=%v)", c.spec, got, err, c.want, c.err)
		}
	}
}

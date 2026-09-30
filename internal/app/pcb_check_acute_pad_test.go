package app

import "testing"

func acutePadFixtureTracks(x, y float64) []pcbTrack {
	return []pcbTrack{
		{ID: "cap-branch", Net: "OSC_IN", Layer: 1, X1: x - 95.685, Y1: y, X2: x, Y2: y, Width: 8},
		{ID: "mcu-branch", Net: "OSC_IN", Layer: 1, X1: x - 61.134, Y1: y + 61.134, X2: x, Y2: y, Width: 8},
	}
}

func TestPcbCheckAcuteAngleExemptsExactSameNetPadBranch(t *testing.T) {
	// Round-1 X1.1 geometry: a horizontal capacitor branch and a 45-degree MCU
	// branch terminate inside the same rectangular OSC_IN pad. Copper fills the
	// apparent 45-degree wedge, so it is not an acid trap.
	const x, y = 1793.434, 756.937
	pads := []pcbPadP{{
		ID: "x1-pad-1", Designator: "X1", Number: "1", Net: "OSC_IN", Layer: 1,
		X: 1793.4, Y: 757, W: 55.1, H: 47.2,
		Shape: "RECT", ShapeW: 55.1, ShapeH: 47.2, ShapeOK: true,
	}}
	rep := analyzePcbCheck(pads, acutePadFixtureTracks(x, y), nil, 0)
	if got := countType(rep, "acute-angle"); got != 0 {
		t.Fatalf("same-net X1.1 pad branch acute-angle=%d, want 0: %+v", got, rep.Findings)
	}
}

func TestPcbCheckAcuteAngleOutsidePadStillWarns(t *testing.T) {
	const x, y = 100.0, 100.0
	tests := []struct {
		name string
		pad  pcbPadP
	}{
		{
			name: "same net but outside copper",
			pad: pcbPadP{Designator: "X1", Number: "1", Net: "OSC_IN", Layer: 1,
				X: 0, Y: 0, Shape: "RECT", ShapeW: 40, ShapeH: 40, ShapeOK: true},
		},
		{
			name: "foreign net pad at vertex",
			pad: pcbPadP{Designator: "X1", Number: "1", Net: "OTHER", Layer: 1,
				X: x, Y: y, Shape: "RECT", ShapeW: 40, ShapeH: 40, ShapeOK: true},
		},
		{
			name: "unknown legacy shape at vertex",
			pad: pcbPadP{Designator: "X1", Number: "1", Net: "OSC_IN", Layer: 1,
				X: x, Y: y, W: 40, H: 40, ShapeOK: false},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := analyzePcbCheck([]pcbPadP{tc.pad}, acutePadFixtureTracks(x, y), nil, 0)
			if got := countType(rep, "acute-angle"); got != 1 {
				t.Fatalf("acute-angle=%d, want 1: %+v", got, rep.Findings)
			}
		})
	}
}

func TestPcbCheckAcuteAngleUsesRotatedPadCopperNotBBox(t *testing.T) {
	// (20,-20) is inside the nominal 50x50 AABB but outside this 45-degree,
	// 60x8mil rectangular copper shape. A bbox-only exemption would hide the
	// real acute corner.
	const x, y = 20.0, -20.0
	pad := pcbPadP{
		Designator: "X1", Number: "1", Net: "OSC_IN", Layer: 1,
		X: 0, Y: 0, W: 50, H: 50, Rotation: 45,
		Shape: "RECT", ShapeW: 60, ShapeH: 8, ShapeOK: true,
	}
	rep := analyzePcbCheck([]pcbPadP{pad}, acutePadFixtureTracks(x, y), nil, 0)
	if got := countType(rep, "acute-angle"); got != 1 {
		t.Fatalf("rotated-pad AABB falsely exempted acute angle: got %d findings=%+v", got, rep.Findings)
	}
}

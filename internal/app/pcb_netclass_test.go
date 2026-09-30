package app

import (
	"math"
	"testing"
)

func TestNetRole(t *testing.T) {
	cases := []struct {
		net  string
		want string
	}{
		// Ground — always gnd, regardless of prefix.
		{"GND", roleGnd}, {"AGND", roleGnd}, {"DGND", roleGnd}, {"PGND", roleGnd}, {"GND1", roleGnd},
		// Connector-input / battery / bus rails → high-current (full board current).
		{"VBUS", roleHighCurrent}, {"VIN", roleHighCurrent}, {"VBAT", roleHighCurrent}, {"VSYS", roleHighCurrent},
		{"+VIN", roleHighCurrent},
		// High voltage → high-current.
		{"+12V", roleHighCurrent}, {"+9V", roleHighCurrent}, {"24V", roleHighCurrent},
		// Main rail 5–9V → trunk.
		{"+5V", rolePowerTrunk}, {"5V", rolePowerTrunk}, {"VCC5V0", rolePowerTrunk},
		// Regulated downstream rail <5V → branch.
		{"3V3", rolePowerBranch}, {"+3V3", rolePowerBranch}, {"1V8", rolePowerBranch}, {"1V2", rolePowerBranch},
		{"VDD_3V3", rolePowerBranch},
		// Voltage-less power names → branch.
		{"VCC", rolePowerBranch}, {"VDD", rolePowerBranch}, {"VREF", rolePowerBranch}, {"VOUT", rolePowerBranch},
		// Signals.
		{"USB_DP", roleSignal}, {"SDA", roleSignal}, {"MISO", roleSignal}, {"D0", roleSignal},
		{"5VUSB", roleSignal}, {"", roleSignal}, {"   ", roleSignal},
	}
	for _, c := range cases {
		if got := netRole(c.net); got != c.want {
			t.Errorf("netRole(%q) = %q, want %q", c.net, got, c.want)
		}
	}
}

func TestRailVoltage(t *testing.T) {
	cases := []struct {
		net string
		v   float64
		ok  bool
	}{
		{"+5V", 5, true}, {"5V", 5, true}, {"3V3", 3.3, true}, {"1V8", 1.8, true},
		{"+12V", 12, true}, {"5V0", 5, true}, {"VCC", 0, false}, {"GND", 0, false}, {"SDA", 0, false},
	}
	for _, c := range cases {
		v, ok := railVoltage(c.net)
		if ok != c.ok || (ok && math.Abs(v-c.v) > 1e-9) {
			t.Errorf("railVoltage(%q) = (%g, %v), want (%g, %v)", c.net, v, ok, c.v, c.ok)
		}
	}
}

func TestNetClassWidthTable(t *testing.T) {
	// Default rules (signal 10 / power 20 / min 5) → the §1.2 metric ladder:
	// branch 0.25mm (9.84mil) / trunk 0.4mm (15.75mil) / high 0.5mm (19.69mil).
	// Signal stays the raw live default (10mil), never metric-rounded.
	tbl := netClassWidthTable(defaultPcbRules())
	want := map[string]float64{
		roleSignal:      10,
		rolePowerBranch: 0.25 * mmToMil, // 9.84
		rolePowerTrunk:  0.40 * mmToMil, // 15.75
		roleHighCurrent: 0.50 * mmToMil, // 19.69
		roleGnd:         0.50 * mmToMil, // 19.69
	}
	for role, w := range want {
		if math.Abs(tbl[role]-w) > 1e-9 {
			t.Errorf("width[%s] = %g, want %g", role, tbl[role], w)
		}
	}

	// Monotonicity, even for a loose live default: power rungs strictly
	// monotonic; the signal→branch edge may dip by metric quantization only
	// (≤ half a 0.05mm step ≈ 0.98mil < pcbWidthTolMil — see netClassWidthTable).
	loose := pcbRules{trackWidthMil: 12, powerWidthMil: 20, trackWidthMinMil: 5}
	lt := netClassWidthTable(loose)
	if lt[rolePowerBranch] < lt[roleSignal]-pcbWidthTolMil {
		t.Errorf("branch(%g) below signal(%g) beyond quantization tolerance", lt[rolePowerBranch], lt[roleSignal])
	}
	order := []string{rolePowerBranch, rolePowerTrunk, roleHighCurrent}
	for i := 1; i < len(order); i++ {
		if lt[order[i]] < lt[order[i-1]] {
			t.Errorf("non-monotonic power rungs: %s(%g) < %s(%g)", order[i], lt[order[i]], order[i-1], lt[order[i-1]])
		}
	}

	// Every width floored at the legal minimum.
	tiny := pcbRules{trackWidthMil: 1, powerWidthMil: 1, trackWidthMinMil: 5}
	for role, w := range netClassWidthTable(tiny) {
		if w < 5 {
			t.Errorf("width[%s] = %g below clamp floor 5", role, w)
		}
	}
}

// TestNetClassWidthTableMetric — 规范 §1.2: every POWER rung of the ladder must
// be a metric-round width (an exact multiple of 0.05mm), and the power rungs
// must be monotonic, across default/loose/odd live rules. Signal is exempt (it
// mirrors the live rule value verbatim).
func TestNetClassWidthTableMetric(t *testing.T) {
	rules := []pcbRules{
		defaultPcbRules(),
		{trackWidthMil: 12, powerWidthMil: 20, trackWidthMinMil: 5},
		{trackWidthMil: 6, powerWidthMil: 25, trackWidthMinMil: 3.5},
		{trackWidthMil: 17, powerWidthMil: 17, trackWidthMinMil: 5},
		// Clamp floor ABOVE the branch metric base (9.84): nearest-rounding of a
		// clamped 10 would give 9.84 < floor → must step up to 0.30mm (11.81).
		{trackWidthMil: 10, powerWidthMil: 20, trackWidthMinMil: 10},
	}
	powerRoles := []string{rolePowerBranch, rolePowerTrunk, roleHighCurrent, roleGnd}
	for _, r := range rules {
		tbl := netClassWidthTable(r)
		for _, role := range powerRoles {
			mm := tbl[role] / mmToMil
			steps := math.Round(mm / metricStepMM)
			if math.Abs(mm-steps*metricStepMM) > 1e-9 {
				t.Errorf("rules %+v: width[%s] = %gmil = %gmm is not a 0.05mm multiple", r, role, tbl[role], mm)
			}
			if tbl[role] < r.trackWidthMinMil {
				t.Errorf("rules %+v: width[%s] = %g below clamp floor %g", r, role, tbl[role], r.trackWidthMinMil)
			}
		}
		order := []string{rolePowerBranch, rolePowerTrunk, roleHighCurrent}
		for i := 1; i < len(order); i++ {
			if tbl[order[i]] < tbl[order[i-1]] {
				t.Errorf("rules %+v: non-monotonic power rungs: %s(%g) < %s(%g)", r, order[i], tbl[order[i]], order[i-1], tbl[order[i-1]])
			}
		}
	}
}

func TestRoundMetricMil(t *testing.T) {
	cases := []struct {
		in, want float64
	}{
		{10, 0.25 * mmToMil},     // 0.254mm → nearest 0.25mm (9.84) — legacy branch base
		{15, 0.40 * mmToMil},     // 0.381mm → nearest 0.40mm (15.75) — legacy trunk base
		{20, 0.50 * mmToMil},     // 0.508mm → nearest 0.50mm (19.69) — legacy high base
		{12, 0.30 * mmToMil},     // 0.3048mm → 0.30mm (11.81)
		{19.685, 0.50 * mmToMil}, // already on-grid → idempotent
		{2.5, 0.10 * mmToMil},    // nearest (0.05mm=1.97) loses >10% → steps up to 0.10mm
		{0, 0},                   // non-positive passes through
	}
	for _, c := range cases {
		if got := roundMetricMil(c.in); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("roundMetricMil(%g) = %g, want %g", c.in, got, c.want)
		}
	}
}

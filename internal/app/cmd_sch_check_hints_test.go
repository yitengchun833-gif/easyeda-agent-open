package app

import (
	"bytes"
	"strings"
	"testing"
)

// A check hint whose fix is a shipped command must name that command. The two
// below were the outliers: the floating-pin hint stopped at "mark intentional
// ones NC" while the per-pin breakdown it prints is exactly what
// `sch no-connect` consumes, and the marker-overlap hint described the manual
// fix while `sch destagger` plans the whole batch and rolls itself back on any
// electrical regression. Leaving the reader to discover them is the dead end
// issue #227 reported for the geometry guard.
func TestRenderCheckReportHintsNameTheFixingCommand(t *testing.T) {
	for _, tc := range []struct {
		name    string
		summary checkSummary
		want    []string
		absent  []string
	}{
		{
			name:    "floating pins name sch no-connect",
			summary: checkSummary{Total: 1, FloatingPins: 1, ComponentsWithFloating: 1},
			want:    []string{"floating pins", "sch no-connect", "--designator", "--pin"},
		},
		{
			name:    "marker overlap names sch destagger",
			summary: checkSummary{Total: 1, MarkerOverlaps: 1},
			want:    []string{"marker overlap", "sch destagger", "--apply"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			renderCheckReport(checkReport{Summary: tc.summary}, &b)
			out := b.String()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("hint missing %q:\n%s", w, out)
				}
			}
			for _, a := range tc.absent {
				if strings.Contains(out, a) {
					t.Errorf("hint still says %q:\n%s", a, out)
				}
			}
		})
	}
}

// A clean report must not advertise either command: a hint that fires with no
// finding behind it is how "恒报" noise starts (issue #181).
func TestRenderCheckReportCleanPageAdvertisesNoFixer(t *testing.T) {
	var b bytes.Buffer
	renderCheckReport(checkReport{Passed: true, Summary: checkSummary{}}, &b)
	out := b.String()
	for _, cmd := range []string{"sch destagger", "sch no-connect"} {
		if strings.Contains(out, cmd) {
			t.Errorf("clean report advertises %q:\n%s", cmd, out)
		}
	}
}

// The per-finding line is what an agent reads first, so it carries the command
// too — not only the trailing hint block.
func TestMarkerOverlapFindingNamesDestagger(t *testing.T) {
	var b bytes.Buffer
	renderCheckReport(checkReport{
		Summary: checkSummary{Total: 1, MarkerOverlaps: 1},
		Findings: []checkFinding{{
			Type:    "marker-overlap",
			Level:   "WARN",
			Message: "+3V3(netflag) 与 GND(netflag) 视觉重叠 28.50×12.50 — `sch destagger` 可自动搬(默认只算不动),或换方向/offset",
		}},
	}, &b)
	if !strings.Contains(b.String(), "sch destagger") {
		t.Errorf("per-finding line must carry the command:\n%s", b.String())
	}
}

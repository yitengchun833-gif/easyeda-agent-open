package app

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func bapManifestWarnings(t *testing.T, stdout []byte) []string {
	t.Helper()
	var man struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(stdout, &man); err != nil {
		t.Fatalf("decode manifest: %v\nstdout=%s", err, stdout)
	}
	return man.Warnings
}

func bapHasWarningWith(warnings []string, parts ...string) bool {
	for _, w := range warnings {
		hit := true
		for _, p := range parts {
			if !strings.Contains(w, p) {
				hit = false
				break
			}
		}
		if hit {
			return true
		}
	}
	return false
}

// Without --project/--window nothing is read, so every designator looks free and
// the origin dodges nothing. On a page that already held LED1/R1 this printed
// "[ready] instance=LED1" with LED1/R1 at 400,300 and said nothing -- the
// cross-page designator collision issue #136 calls net-attribution poison,
// dressed as a clean plan. The manifest has to carry the caveat too: a --json
// consumer that only reads stdout would otherwise still see a green plan.
func TestRunBlockApplyDryRunWithoutRoutingReportsTheEmptyPagePlan(t *testing.T) {
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		t.Errorf("unexpected action %q: with no --project/--window there is nothing to read", call.Action)
		return `{"ok":true,"result":{}}`
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	if err := runBlockApply(cfg, "", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), true, true, 0, &stdout, &stderr); err != nil {
		t.Fatalf("dry-run err=%v, want a plan with a caveat", err)
	}
	if calls := daemon.snapshot(); len(calls) != 0 {
		t.Fatalf("calls=%+v, want none", calls)
	}
	warnings := bapManifestWarnings(t, stdout.Bytes())
	if !bapHasWarningWith(warnings, "EMPTY page", "--project") {
		t.Errorf("manifest warnings must name the empty-page plan and the way out; got %q", warnings)
	}
	if !strings.Contains(stderr.String(), "EMPTY page") {
		t.Errorf("stderr must say it too:\n%s", stderr.String())
	}
}

// The other way to end up planning against an empty page -- the read was
// attempted and failed -- used to warn on stderr only, so --json readers got the
// same false green. Both paths now land in the manifest.
func TestRunBlockApplyDryRunPageReadFailureLandsInTheManifest(t *testing.T) {
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		return `{"ok":false,"error":{"code":"EDA_CALL_FAILED","message":"no active schematic"}}`
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	// This test isolates the page-read warning. #241 added an earlier, independent
	// device-library preflight; skip it here so the daemon's intentional catch-all
	// read failure reaches the page-read path under test.
	if err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{SkipDevicePreflight: true},
		blockApplyPartsFixture(t), true, true, 0, &stdout, &stderr); err != nil {
		t.Fatalf("dry-run err=%v, want a plan with a caveat", err)
	}
	warnings := bapManifestWarnings(t, stdout.Bytes())
	if !bapHasWarningWith(warnings, "EMPTY page", "no active schematic") {
		t.Errorf("manifest warnings must carry the read failure verbatim; got %q", warnings)
	}
}

// A routed dry-run did read the page, so it must not be labelled an empty-page plan.
func TestRunBlockApplyRoutedDryRunHasNoEmptyPageCaveat(t *testing.T) {
	cfg, _, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
		return `{"ok":true,"result":{"components":[]}}`
	})
	defer cleanup()

	var stdout, stderr bytes.Buffer
	if err := runBlockApply(cfg, "w1", "led_indicator_gpio", bapInput{},
		blockApplyPartsFixture(t), true, true, 0, &stdout, &stderr); err != nil {
		t.Fatalf("dry-run err=%v", err)
	}
	if w := bapManifestWarnings(t, stdout.Bytes()); bapHasWarningWith(w, "EMPTY page") {
		t.Errorf("a routed dry-run read the page; it must not claim otherwise: %q", w)
	}
}

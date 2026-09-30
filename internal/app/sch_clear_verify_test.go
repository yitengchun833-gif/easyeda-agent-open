package app

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func schClearVerifiedFixture() map[string]any {
	// Empty enumeration classes are intentionally absent in connector output.
	return map[string]any{"deletedIds": map[string]any{}, "remaining": 0.0}
}

func TestSchClearVerificationRejectsUnknownOrRemainingPrimitives(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"surviving-wire", func(r map[string]any) { r["remaining"] = 1.0 }},
		{"missing-count", func(r map[string]any) { delete(r, "remaining") }},
		{"null-count", func(r map[string]any) { r["remaining"] = nil }},
		{"negative-count", func(r map[string]any) { r["remaining"] = -1.0 }},
		{"nonfinite-count", func(r map[string]any) { r["remaining"] = math.NaN() }},
		{"missing-enumeration", func(r map[string]any) { delete(r, "deletedIds") }},
		{"null-enumeration", func(r map[string]any) { r["deletedIds"] = nil }},
		{"null-wire-enumeration", func(r map[string]any) { r["deletedIds"].(map[string]any)["wires"] = nil }},
		{"malformed-enumeration", func(r map[string]any) { r["deletedIds"].(map[string]any)["texts"] = "unknown" }},
		{"failed-wire-enumeration", func(r map[string]any) { r["warnings"] = []any{"enumerate wires failed"} }},
		{"failed-delete", func(r map[string]any) { r["warnings"] = []any{"delete texts failed"} }},
		{"malformed-warning", func(r map[string]any) { r["warnings"] = "enumeration unavailable" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := schClearVerifiedFixture()
			tc.edit(r)
			if err := verifySchClearResult(r, true); err == nil {
				t.Fatal("unproven empty page was accepted")
			}
		})
	}
}

func TestSchClearVerificationAcceptsCompleteSparseEnumeration(t *testing.T) {
	r := schClearVerifiedFixture()
	if err := verifySchClearResult(r, true); err != nil {
		t.Fatalf("empty sparse inventory is the connector's valid empty page: %v", err)
	}
	r["deletedIds"] = map[string]any{"components": []any{"removed-U1"}, "wires": []any{"removed-wire"}}
	r["warnings"] = []any{}
	if err := verifySchClearResult(r, true); err != nil {
		t.Fatalf("IDs enumerate original deleted primitives, not remaining primitives: %v", err)
	}
}

func TestSchClearDryRunReportsInventoryWithoutPretendingEmpty(t *testing.T) {
	r := schClearVerifiedFixture()
	r["remaining"] = 3.0
	r["deletedIds"] = map[string]any{"wires": []any{"a", "b", "c"}}
	if err := verifySchClearResult(r, false); err != nil {
		t.Fatalf("ordinary dry-run may report a nonempty page: %v", err)
	}
	if err := verifySchClearResult(r, true); err == nil {
		t.Fatal("dry-run --expect-empty accepted remaining wires")
	}
	r["warnings"] = []any{"could not enumerate text primitives"}
	if err := verifySchClearResult(r, false); err == nil {
		t.Fatal("dry-run must not present partial enumeration as complete")
	}
}

func TestSchClearCLIExpectEmptyHonorsEnumerationFailure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		remaining   float64
		warning     bool
		wantSuccess bool
	}{{"empty", 0, false, true}, {"surviving-wire", 1, false, false}, {"unknown-text-inventory", 0, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			r := schClearVerifiedFixture()
			r["remaining"] = tc.remaining
			if tc.warning {
				r["warnings"] = []any{"enumerate texts failed"}
			}
			b, _ := json.Marshal(r)
			cfg, daemon, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
				return `{"ok":true,"result":` + string(b) + `}`
			})
			defer cleanup()
			var stdout, stderr bytes.Buffer
			code := Run([]string{"sch", "clear", "--dry-run", "--expect-empty", "--host", cfg.host, "--ports", cfg.ports, "--window", "w1"}, &stdout, &stderr)
			if (code == 0) != tc.wantSuccess {
				t.Fatalf("CLI exit %d does not reflect complete-empty gate: stdout=%s stderr=%s", code, stdout.String(), stderr.String())
			}
			found := false
			for _, call := range daemon.snapshot() {
				if call.Action == "schematic.page.clear" {
					found = true
					if call.Payload["dryRun"] != true || call.Payload["preserveSheet"] != true {
						t.Fatalf("verification mutated the page or removed sheet protection: %+v", call)
					}
				}
			}
			if !found {
				t.Fatal("test did not reach the page enumeration action")
			}
		})
	}
}

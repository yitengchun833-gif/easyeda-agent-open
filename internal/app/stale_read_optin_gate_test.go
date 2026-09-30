package app

import "testing"

func TestScopedStaleReadCompatibilityDoesNotSendBypass(t *testing.T) {
	cfg := &appConfig{}
	restore := setDispatchStaleReadReason("legacy apply verification")
	defer restore()
	if got := staleReadForceReason(staleReadOptIn(cfg, "legacy write/read"), "pcb.nets.list", nil); got != "" {
		t.Fatalf("stale read compatibility path must be advisory-only, got forceReason %q", got)
	}
}

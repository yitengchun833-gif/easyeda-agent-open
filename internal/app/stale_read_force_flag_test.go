package app

import "testing"

func TestForceStaleReadCompatibilityOptionIsNoOp(t *testing.T) {
	cfg := &appConfig{forceStaleRead: "legacy reason"}
	if got := staleReadForceReason(cfg, "pcb.components.list", nil); got != "" {
		t.Fatalf("deprecated option must not send forceReason, got %q", got)
	}
	if got := staleReadOptIn(cfg, "after write"); got != cfg {
		t.Fatal("staleReadOptIn should preserve the original config")
	}
}

func TestStaleReadCompatibilityCodeStillRecognized(t *testing.T) {
	err := &actionError{Action: "pcb.components.list", Code: staleReadCode, Message: "old daemon refusal"}
	if !isStaleRead(err) {
		t.Fatal("old daemon STALE_READ response should remain diagnosable")
	}
}

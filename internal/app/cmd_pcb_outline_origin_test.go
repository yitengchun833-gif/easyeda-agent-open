package app

import (
	"bytes"
	"testing"
)

func TestPcbOutlineRoundDispatchesNativeArcSource(t *testing.T) {
	cfg, captured, cleanup := newCapturingDaemon(t)
	defer cleanup()

	var stdout, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"outline-round", "--rect", "0,0,100,80", "--radius", "10"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
	}

	captured.mu.Lock()
	defer captured.mu.Unlock()
	if captured.action != "pcb.outline.set" {
		t.Fatalf("action=%q, want pcb.outline.set", captured.action)
	}
	if captured.payload["lineWidth"] != float64(10) {
		t.Fatalf("lineWidth=%v, want 10", captured.payload["lineWidth"])
	}
	source, ok := captured.payload["source"].([]any)
	if !ok {
		t.Fatalf("source=%T, want JSON array", captured.payload["source"])
	}
	arcCount := 0
	for _, token := range source {
		if token == "ARC" {
			arcCount++
		}
	}
	if arcCount != 4 {
		t.Fatalf("ARC tokens=%d, want 4; source=%v", arcCount, source)
	}
	if _, exists := captured.payload["points"]; exists {
		t.Fatal("outline-round must send the native source, not sampled points")
	}
}

func TestPcbOriginCommandsWireTypedActions(t *testing.T) {
	t.Run("get", func(t *testing.T) {
		cfg, captured, cleanup := newCapturingDaemon(t)
		defer cleanup()
		var stdout, stderr bytes.Buffer
		cmd := newPcbCmd(cfg, &stdout, &stderr)
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"origin", "get"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		captured.mu.Lock()
		defer captured.mu.Unlock()
		if captured.action != "pcb.origin.get" {
			t.Fatalf("action=%q, want pcb.origin.get", captured.action)
		}
	})

	t.Run("set", func(t *testing.T) {
		cfg, captured, cleanup := newCapturingDaemon(t)
		defer cleanup()
		var stdout, stderr bytes.Buffer
		cmd := newPcbCmd(cfg, &stdout, &stderr)
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"origin", "set", "--x", "118.11", "--y", "-25"})
		if err := cmd.Execute(); err != nil {
			t.Fatalf("execute: %v (stderr=%s)", err, stderr.String())
		}
		captured.mu.Lock()
		defer captured.mu.Unlock()
		if captured.action != "pcb.origin.set" {
			t.Fatalf("action=%q, want pcb.origin.set", captured.action)
		}
		if captured.payload["offsetX"] != 118.11 || captured.payload["offsetY"] != -25.0 {
			t.Fatalf("payload=%v", captured.payload)
		}
	})
}

func TestPcbOriginSetRequiresBothOffsets(t *testing.T) {
	cfg, _, cleanup := newCapturingDaemon(t)
	defer cleanup()
	var stdout, stderr bytes.Buffer
	cmd := newPcbCmd(cfg, &stdout, &stderr)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"origin", "set", "--x", "0"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected --y to be required")
	}
}

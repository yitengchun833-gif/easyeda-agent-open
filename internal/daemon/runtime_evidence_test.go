package daemon

import (
	"fmt"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"testing"
)

func TestRuntimeReadOnlyBatchRetainsDrcEvidence(t *testing.T) {
	s := newRuntimeStore()
	s.observe(auditEntry{RequestID: "batch", WindowID: "w", Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}, Action: "debug.batch", OK: true,
		Payload: map[string]any{"steps": []any{map[string]any{"action": "schematic.drc.check"}}},
		Result:  map[string]any{"ok": true, "results": []any{map[string]any{"action": "schematic.drc.check", "index": 0, "ok": true, "result": map[string]any{"countsAvailable": true, "summary": map[string]any{"error": 0, "fatal": 0, "unknown": 0, "warn": 10}}}}}})
	if !s.receipts[0].DRCPassed {
		t.Fatal("successful read-only batch lost final DRC evidence")
	}
}

func TestRuntimeExportEvidenceRequiresFileFreshnessAndScope(t *testing.T) {
	for _, name := range []string{"valid", "missing-file", "stale", "partial", "wrong-scope"} {
		t.Run(name, func(t *testing.T) {
			s := New(Options{})
			ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
			d := s.runtime.document("w", ctx)
			task := &runtimeReceipt{RequestID: "task", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Generation: d.Generation, Checks: map[string]any{"status": "pass", "scope": []any{"r"}}}
			s.runtime.receipts = append(s.runtime.receipts, task)
			id := "r"
			if name == "wrong-scope" {
				id = "other"
			}
			e := auditEntry{RequestID: "image", WindowID: "w", Context: ctx, Action: "schematic.export.image", OK: true, Payload: map[string]any{"primitiveIds": []any{id}}, Result: map[string]any{"format": "png", "scope": "selection", "selectedCount": 1, "artifactId": "a"}, Artifacts: []protocol.Artifact{{ID: "a", Kind: "schematic_export", Path: "render.png", Size: 100, SHA256: "hash", MimeType: "image/png"}}}
			if name == "missing-file" {
				e.Artifacts = nil
			}
			if name == "partial" {
				e.Result["partial"] = true
			}
			s.runtime.observe(e)
			if name == "stale" {
				d.Generation++
			}
			body := fmt.Sprintf(`{"operation":"visual_review","window":"w","requestId":"task","generation":%d,"screenshotRequestId":"image","source":"ai"}`, d.Generation)
			got := runtimeTestControl(s, body).Code
			want := 409
			if name == "valid" {
				want = 200
			}
			if got != want {
				t.Fatalf("got %d want %d", got, want)
			}
		})
	}
}

func TestRuntimeBatchEvidenceRejectsWritesAndIncompleteResults(t *testing.T) {
	for _, name := range []string{"write-after-check", "partial", "step-failed", "missing-step"} {
		t.Run(name, func(t *testing.T) {
			steps := []any{map[string]any{"action": "schematic.drc.check"}, map[string]any{"action": "document.current"}}
			results := []any{map[string]any{"index": 0, "action": "schematic.drc.check", "ok": true, "result": map[string]any{"countsAvailable": true, "summary": map[string]any{"error": 0, "fatal": 0, "unknown": 0}}}, map[string]any{"index": 1, "action": "document.current", "ok": true}}
			e := auditEntry{Action: "debug.batch", OK: true, Payload: map[string]any{"steps": steps}, Result: map[string]any{"ok": true, "results": results}}
			switch name {
			case "write-after-check":
				runtimeMap(steps[1])["action"] = "schematic.attribute.modify"
				runtimeMap(results[1])["action"] = "schematic.attribute.modify"
			case "partial":
				e.Result["partial"] = true
			case "step-failed":
				runtimeMap(results[1])["ok"] = false
			case "missing-step":
				e.Result["results"] = results[:1]
			}
			r := &runtimeReceipt{}
			r.observeEvidence(e, true)
			if r.DRCPassed {
				t.Fatal("unsafe batch certified")
			}
		})
	}
}

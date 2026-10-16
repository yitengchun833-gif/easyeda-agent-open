package daemon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRuntimeObsoleteReviewDoesNotExhaustCapacity(t *testing.T) {
	for _, action := range []string{"debug.exec_js", "debug.batch"} {
		t.Run(action, func(t *testing.T) {
			s := newRuntimeStore()
			ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
			dir := t.TempDir()
			audit := newAuditWriter(dir)
			audit.observe = s.observe
			var first *runtimeReceipt
			for i := 0; i < 320; i++ {
				payload := map[string]any{}
				result := map[string]any{"rules": map[string]any{"status": "unknown"}}
				if action == "debug.batch" {
					payload["steps"] = []any{map[string]any{"action": "schematic.component.modify"}}
					result["completed"], result["total"] = 1, 1
					result["results"] = []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true}}
				}
				req := protocol.Request{Envelope: protocol.Envelope{ID: fmt.Sprint(i), WindowID: "w"}, Action: action, Payload: payload}
				if err := s.start(req, ctx); err != nil {
					t.Fatalf("settled old review blocked request %d: %v", i, err)
				}
				audit.Append(auditEntry{Timestamp: time.Unix(1, 0).UTC(), RequestID: req.ID, WindowID: "w", Context: ctx, Action: action, Payload: payload, OK: true, Result: result})
				if i == 0 {
					first = s.receipts[0]
				}
			}
			latest := s.receipts[len(s.receipts)-1]
			if latest.Status != "needs_review" || !s.protectedReceipts()[latest.RequestID] || !s.documents[s.current["w"]].ChangesSinceSave {
				t.Fatal("current review/save requirement was lost")
			}
			if first.Status != "needs_review" || runtimeMap(first.Checks)["status"] != "unknown" {
				t.Fatal("retirement must not claim that old unknown checks passed")
			}
			data, err := os.ReadFile(filepath.Join(dir, "1970-01-01.jsonl"))
			if err != nil || strings.Count(string(data), "\n") != 320 || !strings.Contains(string(data), `"status":"unknown"`) {
				t.Fatalf("original audit evidence was not preserved: %v", err)
			}
		})
	}
}

func TestRuntimeReviewRetentionKeepsUnsettledAndDependencies(t *testing.T) {
	s := newRuntimeStore()
	d := s.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.Generation = 9
	for _, status := range []string{"queued", "running", "resuming", "saving", "paused", "needs_readback", "needs_review"} {
		r := &runtimeReceipt{started: true, RequestID: status, WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: status, Generation: 8}
		if status == "needs_review" {
			r.Generation = 9
		}
		s.receipts = append(s.receipts, r)
	}
	s.receipts = append(s.receipts,
		&runtimeReceipt{started: true, RequestID: "old-review", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "needs_review", Generation: 8},
		&runtimeReceipt{started: true, RequestID: "parent-review", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "needs_review", Generation: 7},
		&runtimeReceipt{started: true, RequestID: "child", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "paused", Generation: 9, ParentRequestID: "parent-review"},
		&runtimeReceipt{started: true, RequestID: "missing-doc", WindowID: "other", Status: "needs_review", Generation: 1},
	)
	protected := s.protectedReceipts()
	for _, r := range s.receipts {
		if protected[r.RequestID] != (r.RequestID != "old-review") {
			t.Fatalf("wrong protection for %s", r.RequestID)
		}
	}
}

func TestRuntimeUnresolvedReadbackStillRefusesCapacity(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	s.document("w", ctx).Generation = 999
	for i := 0; i < 256; i++ {
		s.receipts = append(s.receipts, &runtimeReceipt{started: true, RequestID: fmt.Sprint(i), WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "needs_readback", Generation: 1})
	}
	if err := s.admit(protocol.Request{Envelope: protocol.Envelope{ID: "new", WindowID: "w"}}, ctx); !errors.Is(err, errRuntimeCapacity) {
		t.Fatalf("unresolved evidence was evicted: %v", err)
	}
	if len(s.receipts) != 256 {
		t.Fatal("refused admission changed the receipt store")
	}
}

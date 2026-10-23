package daemon

import (
	"encoding/json"
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
				req := protocol.Request{Envelope: protocol.Envelope{ID: action + "-" + fmt.Sprint(i), WindowID: "w"}, Action: action, Payload: payload}
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

func TestRuntimeUnresolvedReceiptCountDoesNotRefuseAdmission(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	for i := 0; i < 300; i++ {
		s.receipts = append(s.receipts, &runtimeReceipt{started: true, RequestID: fmt.Sprint(i), WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "needs_readback", Error: "preserve me"})
	}
	before, _ := json.Marshal(s.receipts)
	if err := s.admit(protocol.Request{Envelope: protocol.Envelope{ID: "new", WindowID: "w"}}, ctx); err != nil {
		t.Fatal("receipt count blocked admission", err)
	}
	after, _ := json.Marshal(s.receipts)
	if string(before) != string(after) {
		t.Fatal("admission removed original evidence")
	}
}

func TestRuntimeReceiptCountDoesNotBlockReadsOrWrites(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	for i := 0; i < 300; i++ {
		s.receipts = append(s.receipts, &runtimeReceipt{started: true, RequestID: fmt.Sprint(i), WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", Status: "needs_readback", Error: "preserve me"})
	}
	before, _ := json.Marshal(s.receipts)
	for _, action := range []string{"document.current", "schematic.component.modify", "schematic.save", "document.open", "debug.exec_js"} {
		req := protocol.Request{Envelope: protocol.Envelope{ID: action, WindowID: "w"}, Action: action}
		if err := s.start(req, ctx); err != nil {
			t.Fatalf("%s blocked by count: %v", action, err)
		}
	}
	after, _ := json.Marshal(s.receipts[:300])
	if string(before) != string(after) {
		t.Fatal("fresh calls overwrote existing receipts")
	}
	if err := s.start(protocol.Request{Envelope: protocol.Envelope{ID: "0", WindowID: "w"}, Action: "document.current"}, ctx); !errors.Is(err, errRuntimeDuplicateRequest) {
		t.Fatal("original request ID could be replayed", err)
	}
}

func TestRuntimeCapacityReadbackDoesNotReclaimFullDocumentStore(t *testing.T) {
	s := newRuntimeStore()
	for i := 0; i < 8; i++ {
		window := fmt.Sprintf("w%d", i)
		d := s.document(window, &protocol.Context{ProjectUUID: fmt.Sprintf("p%d", i), DocumentUUID: fmt.Sprintf("d%d", i)})
		d.ChangesSinceSave = true
		d.Objects["preserve"] = &runtimeObject{Data: map[string]any{"primitiveId": fmt.Sprint(i)}, StaleFields: map[string]bool{"pins": true}}
	}
	beforeDocuments, err := json.Marshal(s.documents)
	if err != nil {
		t.Fatal(err)
	}
	read := protocol.Request{Envelope: protocol.Envelope{ID: "read", WindowID: "new"}, Action: "document.current"}
	context := &protocol.Context{ProjectUUID: "new-project", DocumentUUID: "new-document"}
	if err := s.start(read, context); err != nil {
		t.Fatalf("readback blocked by full document store: %v", err)
	}
	s.observe(auditEntry{RequestID: read.ID, WindowID: read.WindowID, Context: context, Action: read.Action, OK: true, Result: map[string]any{"ok": true}})
	afterDocuments, err := json.Marshal(s.documents)
	if err != nil || string(afterDocuments) != string(beforeDocuments) || len(s.receipts) != 0 {
		t.Fatalf("audit-only readback reclaimed a document or added evidence: %v", err)
	}
	write := protocol.Request{Envelope: protocol.Envelope{ID: "write", WindowID: "new"}, Action: "schematic.component.modify"}
	if err := s.start(write, context); !errors.Is(err, errRuntimeCapacity) {
		t.Fatalf("write bypassed a full document store: %v", err)
	}
}

func TestRuntimeCapacityReadOnlyBatchRejectsNestedContinuationMetadata(t *testing.T) {
	for _, step := range []map[string]any{
		{"action": "document.current", "_taskParent": "old"},
		{"action": "document.current", "_taskRevision": 1},
		{"action": "document.current", "_finishGeneration": 1},
		{"action": "document.current", "payload": map[string]any{"_taskParent": "old"}},
		{"action": "document.current", "payload": map[string]any{"_finishGeneration": 1}},
	} {
		if capacityReadOnly(protocol.Request{Action: "debug.batch", Payload: map[string]any{"steps": []any{step}}}) {
			t.Fatalf("nested continuation metadata bypassed capacity: %#v", step)
		}
	}
}

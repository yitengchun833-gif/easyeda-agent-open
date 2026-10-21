package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRuntimeCheckpointTaskAndLocalRecovery(t *testing.T) {
	opts := Options{AuditDir: t.TempDir(), PortStart: 60932}
	s := New(opts)
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	observe := func(server *Server, window, id string, components []any) {
		server.runtime.observe(auditEntry{Timestamp: time.Now(), RequestID: id, WindowID: window, Context: ctx, Action: "schematic.components.list", OK: true, Result: map[string]any{"components": components, "_scene": map[string]any{"instance": "connector-a", "sequence": float64(0)}, "readScope": map[string]any{"complete": false}}})
	}
	observe(s, "old", "read1", []any{map[string]any{"primitiveId": "r1", "x": float64(10)}, map[string]any{"primitiveId": "r2", "x": float64(20)}})
	body := `{"operation":"task_update","window":"old","target":{"projectUuid":"p","documentUuid":"d"},"expectedRevision":0,"task":{"goal":"调整 R1 标签","primitiveIds":["r1"],"remaining":["局部看图","结束时保存"]}}`
	update := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/runtime/control", strings.NewReader(body))
		r.Header.Set("X-Easyeda-Control", "1")
		w := httptest.NewRecorder()
		s.handleRuntimeControl(w, r)
		return w
	}
	w := update(body)
	var updateResult map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &updateResult)
	if w.Code != 200 || updateResult["persisted"] != true {
		t.Fatal(w.Code, w.Body.String())
	}
	if len(s.runtime.receipts) != 1 {
		t.Fatal("intent update dispatched EDA work")
	}
	if update(body).Code != 409 {
		t.Fatal("stale task revision overwritten")
	}
	if update(strings.Replace(body, `"documentUuid":"d"`, `"documentUuid":"wrong"`, 1)).Code != 409 {
		t.Fatal("foreign document accepted")
	}
	req := protocol.Request{Envelope: protocol.Envelope{ID: "batch", WindowID: "old"}, Action: "debug.batch", Payload: map[string]any{"steps": []any{}}}
	if err := s.runtime.start(req, ctx); err != nil {
		t.Fatal(err)
	}
	if s.runtime.receipts[1].TaskRevision != 1 {
		t.Fatal("task revision missing from actual receipt")
	}
	observe(s, "old", "read2", []any{map[string]any{"primitiveId": "r1", "x": float64(30)}})
	if err := s.runtime.flushCheckpoint(); err != nil {
		t.Fatal(err)
	}
	restarted := New(opts)
	d := restarted.runtime.documents[restarted.runtime.current["old"]]
	if d == nil || d.Task.Goal != "调整 R1 标签" || d.Task.Revision != 1 || len(d.Objects) != 2 || d.Objects["r1"].Data["x"] != float64(30) || !d.Objects["r2"].StaleFields["geometry"] || !d.Restored {
		t.Fatalf("state not safely restored: %+v", d)
	}
	if len(restarted.runtime.requests) != 0 || len(restarted.runtime.baselines) != 0 || d.Checks != nil || len(d.Coverage) != 0 {
		t.Fatal("restored proof or queue")
	}
	for _, receipt := range restarted.runtime.receipts {
		if !receipt.Historical || receipt.ImageAvailable || receipt.DRCPassed || receipt.ObservedFacts != nil {
			t.Fatal("historical receipt trusted")
		}
	}
	if restarted.runtime.receipts[1].Status != "needs_readback" {
		t.Fatal("interrupted write treated as running or complete")
	}
	observe(restarted, "new", "read3", []any{map[string]any{"primitiveId": "r1", "x": float64(35)}})
	d = restarted.runtime.documents[restarted.runtime.current["new"]]
	if len(restarted.runtime.documents) != 1 || d.Restored || d.Task.Revision != 1 || d.Objects["r1"].StaleFields["geometry"] || !d.Objects["r2"].StaleFields["geometry"] {
		t.Fatal("rebinding lost scope or refreshed unrelated objects")
	}
	projected := restarted.runtime.snapshot("new", map[string]bool{"r1": true})
	doc := runtimeMap(runtimeArray(projected["documents"])[0])
	if len(runtimeMap(doc["objects"])) != 1 || doc["freshness"] != "external_unconfirmed" {
		t.Fatal("projection/freshness wrong")
	}
	if len(runtimeArray(projected["receipts"])) != 4 {
		t.Fatal("new window lost same-document historical receipts")
	}
	other := restarted.runtime.document("second", ctx)
	if other == d || len(other.Objects) != 0 {
		t.Fatal("live windows share mutable cache")
	}
}

func TestRuntimeCheckpointLoopAndFailure(t *testing.T) {
	s := newRuntimeStore()
	path := filepath.Join(t.TempDir(), "runtime.json")
	if s.restoreCheckpoint(path) {
		t.Fatal("missing file restored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.checkpointLoop(ctx); close(done) }()
	s.observe(auditEntry{RequestID: "read", WindowID: "w", Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}, Action: "document.current", OK: true})
	deadline := time.Now().Add(2 * time.Second)
	for {
		s.mu.Lock()
		saved := s.checkpointAt != nil
		s.mu.Unlock()
		if saved {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			<-done
			t.Fatal("background checkpoint not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	var saved runtimeCheckpoint
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &saved) != nil || len(saved.Receipts) != 1 {
		t.Fatal("shutdown did not flush", err)
	}
	// A damaged checkpoint must not be accepted as a fresh empty state.
	if err := os.WriteFile(path, []byte(`{"version":999}`), 0600); err != nil {
		t.Fatal(err)
	}
	next := newRuntimeStore()
	if next.restoreCheckpoint(path) || next.checkpointError == "" {
		t.Fatal("bad checkpoint hidden")
	}
	next.checkpointPath = filepath.Join(path, "unwritable.json")
	if next.flushCheckpoint() == nil || next.checkpointError == "" {
		t.Fatal("persistence failure hidden")
	}
}

func TestRuntimeTaskRevisionBlocksOldContinuation(t *testing.T) {
	s := New(Options{})
	d := s.runtime.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.Task = &runtimeTask{Revision: 2, Goal: "new goal"}
	s.runtime.receipts = append(s.runtime.receipts, &runtimeReceipt{RequestID: "old", WindowID: "w", ProjectUUID: "p", DocumentUUID: "d", TaskRevision: 1})
	r := httptest.NewRequest("POST", "/runtime/control", strings.NewReader(`{"operation":"resume","window":"w","requestId":"old"}`))
	r.Header.Set("X-Easyeda-Control", "1")
	w := httptest.NewRecorder()
	s.handleRuntimeControl(w, r)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "intent changed") {
		t.Fatal(w.Code, w.Body.String())
	}
}

package daemon

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRuntimeRecoveryWaitsForNativeIdentity(t *testing.T) {
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	s := newRuntimeStore()
	old := s.document("old-new-version", ctx)
	old.Task = &runtimeTask{Revision: 3, Goal: "current goal"}
	old.SceneInstance = "new-connector-session"
	old.Restored = true
	legacy := s.document("old-legacy-empty", ctx)
	legacy.Restored = true
	s.connectionChanged("legacy-first", "context", ctx)
	s.connectionChanged("new-second", "context", ctx)
	if s.documents[s.current["legacy-first"]].Task != nil || !old.Restored {
		t.Fatal("connection arrival stole historical task")
	}
	s.observe(auditEntry{RequestID: "native", WindowID: "new-second", Context: ctx, Action: "document.current", OK: true, Result: map[string]any{"_scene": map[string]any{"instance": "new-connector-session"}}})
	if s.documents[s.current["new-second"]].Task != old.Task || s.documents[s.current["legacy-first"]].Task != nil {
		t.Fatal("native session identity did not select correct history")
	}
}

func TestRuntimeRecoveryAmbiguityAndExplicitSelection(t *testing.T) {
	s := New(Options{})
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	for _, window := range []string{"history-a", "history-b"} {
		d := s.runtime.document(window, ctx)
		d.Task = &runtimeTask{Revision: 1, Goal: window}
		d.Restored = true
		d.Objects["r"] = &runtimeObject{Data: map[string]any{"primitiveId": "r", "x": float64(10)}, StaleFields: map[string]bool{}}
	}
	s.runtime.connectionChanged("new", "context", ctx)
	s.runtime.observe(auditEntry{RequestID: "native", WindowID: "new", Context: ctx, Action: "document.current", OK: true, Result: map[string]any{"_scene": map[string]any{"instance": "current-session"}}})
	d := s.runtime.documents[s.runtime.current["new"]]
	if d.Task != nil || runtimeMap(d.Recovery)["status"] != "selection_required" {
		t.Fatal("ambiguous history silently chosen")
	}
	post := func(source, target string, generation uint64) *httptest.ResponseRecorder {
		body := fmt.Sprintf(`{"operation":"restore","window":"new","sourceWindow":%q,"target":{"projectUuid":"p","documentUuid":%q},"generation":%d}`, source, target, generation)
		r := httptest.NewRequest("POST", "/runtime/control", strings.NewReader(body))
		r.Header.Set("X-Easyeda-Control", "1")
		w := httptest.NewRecorder()
		s.handleRuntimeControl(w, r)
		return w
	}
	if post("history-b", "foreign", d.Generation).Code != 409 || post("history-b", "d", d.Generation+1).Code != 409 {
		t.Fatal("foreign/stale binding accepted")
	}
	w := post("history-b", "d", d.Generation)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	d = s.runtime.documents[s.runtime.current["new"]]
	if d.Task.Goal != "history-b" || !d.Objects["r"].StaleFields["geometry"] || d.SceneInstance != "current-session" || len(s.runtime.requests) != 0 {
		t.Fatal("selection lost intent or trusted old facts")
	}
	if post("history-a", "d", d.Generation).Code != 409 {
		t.Fatal("live work overwritten")
	}
	if s.runtime.documents[s.runtime.current["history-a"]].Task.Goal != "history-a" {
		t.Fatal("other history merged/deleted")
	}
}

func TestRuntimeRecoveryPreservesSessionAcrossCheckpoint(t *testing.T) {
	opts := Options{AuditDir: t.TempDir()}
	s := New(opts)
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.runtime.document("source", ctx)
	d.SceneInstance = "stable-session"
	d.Task = &runtimeTask{Revision: 5, Goal: "resume"}
	s.runtime.document("empty", ctx)
	if err := s.runtime.flushCheckpoint(); err != nil {
		t.Fatal(err)
	}
	restarted := New(opts)
	if len(restarted.runtime.documents) != 1 {
		t.Fatal("empty connection context remained a recovery candidate")
	}
	restarted.runtime.connectionChanged("legacy", "context", ctx)
	restarted.runtime.connectionChanged("actual", "context", ctx)
	restarted.runtime.observe(auditEntry{WindowID: "actual", Context: ctx, Action: "document.current", OK: true, Result: map[string]any{"_scene": map[string]any{"instance": "stable-session"}}})
	if restarted.runtime.documents[restarted.runtime.current["actual"]].Task.Revision != 5 {
		t.Fatal("checkpoint session could not restore task")
	}
}

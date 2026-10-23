package daemon

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestConnectionEpochRejectsRetiredFramesAndDisconnect(t *testing.T) {
	s := New(Options{})
	old, current := newConn(nil, time.Now()), newConn(nil, time.Now())
	register := protocol.Register{Type: protocol.TypeRegister, WindowID: "same-window"}
	frame, _ := json.Marshal(register)
	s.handleFrame(context.Background(), old, frame)
	s.handleFrame(context.Background(), current, frame)
	s.handleFrame(context.Background(), old, frame)
	if selected, _ := s.hub.target("same-window"); selected != current {
		t.Fatal("retired register replaced the current connection")
	}
	identity := protocol.ContextMessage{Type: protocol.TypeContext, WindowID: "same-window", ProjectUUID: "project", DocumentUUID: "current"}
	frame, _ = json.Marshal(identity)
	s.handleFrame(context.Background(), current, frame)
	d := s.runtime.documents[s.runtime.current["same-window"]]
	generation := d.Generation
	identity.DocumentUUID = "old"
	frame, _ = json.Marshal(identity)
	s.handleFrame(context.Background(), old, frame)
	frame = []byte(`{"type":"event","payload":{"kind":"scene_changed","stamp":{"instance":"retired","sequence":3}}}`)
	s.handleFrame(context.Background(), old, frame)
	if s.hub.removeCurrent(old) {
		t.Fatal("retired socket removed its replacement")
	}
	s.runtime.connectionChanged("same-window", "disconnected", nil, old.epoch)
	if d.Generation != generation || s.runtime.current["same-window"] == "" || current.snapshot().Context.DocumentUUID != "current" {
		t.Fatal("retired frame changed the current connection")
	}
	if old.epoch == current.epoch {
		t.Fatal("new connections share an epoch")
	}
}

func TestRuntimeOldEpochReceiptCannotRefreshNewCache(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "project", DocumentUUID: "document"}
	s.connectionChanged("window", "registered", nil, "old")
	s.connectionChanged("window", "context", ctx, "old")
	req := protocol.Request{Envelope: protocol.Envelope{ID: "old-read", WindowID: "window"}, Action: "schematic.components.list", Payload: map[string]any{"_connectionEpoch": "old"}}
	if err := s.start(req, ctx); err != nil {
		t.Fatal(err)
	}
	s.connectionChanged("window", "registered", nil, "new")
	d := s.documents[s.current["window"]]
	generation := d.Generation
	s.observe(auditEntry{RequestID: req.ID, WindowID: req.WindowID, Context: ctx, Payload: req.Payload, Action: req.Action, OK: true, Result: map[string]any{"components": []any{map[string]any{"primitiveId": "old-only"}}}})
	if d.Generation != generation || d.Objects["old-only"] != nil || !s.receipts[0].Historical {
		t.Fatal("old response refreshed the new connection's document")
	}
}

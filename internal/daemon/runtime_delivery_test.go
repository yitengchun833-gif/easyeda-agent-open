package daemon

import (
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	"testing"
)

func TestRuntimeCloseKeepsClosedIdentityWhenAnotherTabActivates(t *testing.T) {
	s := newRuntimeStore()
	before := &protocol.Context{ProjectUUID: "p", DocumentUUID: "a", DocumentType: "schematic"}
	request := protocol.Request{Envelope: protocol.Envelope{ID: "close", WindowID: "w"}, Action: "document.close", Payload: map[string]any{"uuid": "a", "tabId": "tab-a"}}
	if err := s.start(request, before); err != nil {
		t.Fatal(err)
	}
	s.observe(auditEntry{WindowID: "w", RequestID: "close", Action: "document.close", OK: true,
		Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "b", DocumentType: "schematic"}, Result: map[string]any{"closed": true, "uuid": "a", "tabId": "tab-a"}})
	row := s.receipts[0]
	if row.StartedContext == nil || row.StartedContext.DocumentUUID != "a" || row.ClosedDocumentUUID != "a" || row.DocumentUUID != "b" {
		t.Fatalf("close target lost or post-close context overwritten: %+v", row)
	}
}

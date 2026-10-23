package daemon

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Exercise the retained, server-authored continuation path, not a fresh action.
func acceptanceContinuationAction(t *testing.T, s *Server, id, window, action string, payload map[string]any) protocol.Response {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/action", strings.NewReader(""))
	s.runtimeDispatch(recorder, request, protocol.Request{Envelope: protocol.Envelope{ID: id, WindowID: window}, Action: action, Payload: payload}, runtimeDispatchProof{ParentID: "scoped-continuation"})
	var response protocol.Response
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	return response
}

func TestFreshRequestsIgnoreHistoricalWorkflowLocks(t *testing.T) {
	dir := t.TempDir()
	acceptanceLegacy(t, dir, 1, 300)
	s, base := acceptanceServer(t, dir)
	_, calls := acceptanceConnector(t, s, base, "fresh-owner", "history-project-0", "history-document-0", nil)
	if err := s.runtime.recovery.SaveAttempt(&recoveryAttempt{RequestID: "historical-unknown", WindowID: "historical-owner", State: recoveryAttemptUnknown, Dangerous: true, ProjectWide: true, Context: protocol.Context{ProjectUUID: "history-project-0", DocumentUUID: "history-document-0"}}); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(acceptanceUnresolved(t, s, "historical-unknown"))
	s.runtime.mu.Lock()
	d := s.runtime.document("fresh-owner", &protocol.Context{ProjectUUID: "history-project-0", DocumentUUID: "history-document-0"})
	d.Task = &runtimeTask{ID: "obsolete-task", Revision: 99, Remaining: []string{"unfinished old work"}}
	d.Checks = map[string]any{"status": "fail"}
	d.ChangesSinceSave = true
	s.runtime.mu.Unlock()
	for i, action := range []string{"schematic.component.modify", "schematic.save", "document.open", "project.open", "debug.exec_js"} {
		response := acceptanceAction(t, s, fmt.Sprintf("fresh-authorized-%d", i), "fresh-owner", action,
			map[string]any{"_taskRevision": 0, "_taskParent": "history-000", "_finishGeneration": 0})
		if !response.OK {
			t.Fatalf("fresh %s rejected: %+v", action, response.Error)
		}
	}
	duplicate := acceptanceAction(t, s, "historical-unknown", "fresh-owner", "debug.exec_js")
	if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || calls.Load() != 5 {
		t.Fatal("original request replayed or fresh requests were not dispatched exactly once")
	}
	after, _ := json.Marshal(acceptanceUnresolved(t, s, "historical-unknown"))
	if string(before) != string(after) {
		t.Fatal("new work rewrote historical unknown evidence")
	}
	for i := 0; i < 300; i++ {
		found, err := s.runtime.recovery.HasRequest(fmt.Sprintf("history-%03d", i))
		if err != nil || !found {
			t.Fatal("historical request lost", i, err)
		}
	}
}

func TestFreshRequestsPreserveNativeInFlightAcrossWindows(t *testing.T) {
	s, base := acceptanceServer(t, "")
	var nativeInFlight atomic.Bool
	nativeInFlight.Store(true)
	_, _ = acceptanceConnector(t, s, base, "old-owner", "same-project", "old-page", func(req protocol.Request) protocol.Response {
		if req.Action == "debug.control" {
			ids := []any{}
			if nativeInFlight.Load() {
				ids = append(ids, "unknown-native-call")
			}
			return protocol.Response{OK: true, Result: map[string]any{"targetChecked": true, "paused": false, "pending": 0, "inFlight": ids}}
		}
		return protocol.Response{OK: true, Result: map[string]any{"partial": true, "completed": 0, "total": 1}}
	})
	_, writes := acceptanceConnector(t, s, base, "new-owner", "same-project", "new-page", nil)
	_ = acceptanceAction(t, s, "unknown-native-call", "old-owner", "debug.exec_js")
	before, _ := json.Marshal(acceptanceUnresolved(t, s, "unknown-native-call"))
	blocked := acceptanceAction(t, s, "new-while-in-flight", "new-owner", "schematic.save")
	if blocked.OK || blocked.Error == nil || blocked.Error.Code != "RUNTIME_SCOPE_BUSY" || writes.Load() != 0 {
		t.Fatal("timed-out native call lost cross-window exclusion")
	}
	nativeInFlight.Store(false)
	acceptanceWantOK(t, acceptanceAction(t, s, "new-after-native-settled", "new-owner", "schematic.save"))
	if writes.Load() != 1 {
		t.Fatal("settled native handler still occupied the execution resource")
	}
	after, _ := json.Marshal(acceptanceUnresolved(t, s, "unknown-native-call"))
	if string(before) != string(after) {
		t.Fatal("queue status rewrote the unknown execution result")
	}
}

func TestReceiptCountDoesNotRejectFreshRequests(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	for i := 0; i < 600; i++ {
		id := fmt.Sprintf("retained-%d", i)
		s.receipts = append(s.receipts, &runtimeReceipt{RequestID: id, WindowID: "old-window", Status: "needs_readback", started: true})
	}
	if err := s.start(protocol.Request{Envelope: protocol.Envelope{ID: "new-save", WindowID: "new-window"}, Action: "schematic.save"}, ctx); err != nil {
		t.Fatal("receipt count rejected a new save", err)
	}
	if len(s.receipts) != 601 {
		t.Fatal("receipt evidence was removed to manufacture admission")
	}
}

func TestFreshProjectOpenStillExcludesAnActiveDestination(t *testing.T) {
	s, base := acceptanceServer(t, "")
	_, calls := acceptanceConnector(t, s, base, "source", "source-project", "source-page", nil)
	s.runtime.mu.Lock()
	s.runtime.attempts["active-destination"] = &recoveryAttempt{RequestID: "active-destination", WindowID: "peer", State: recoveryAttemptDispatched, Dangerous: true, Context: protocol.Context{ProjectUUID: "destination-project"}}
	s.runtime.mu.Unlock()
	response := acceptanceAction(t, s, "open-active-destination", "source", "project.open", map[string]any{"projectUuid": "destination-project", "allowDiscardUnsaved": true})
	if response.OK || response.Error == nil || response.Error.Code != "RUNTIME_SCOPE_BUSY" || calls.Load() != 0 {
		t.Fatal("project navigation ignored an active destination write")
	}
}

func TestDurableReceiptHistoryBeyond256SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	s, base := acceptanceServer(t, dir)
	connector, calls := acceptanceConnector(t, s, base, "receipt-owner", "p", "d", nil)
	for i := 0; i < 320; i++ {
		acceptanceWantOK(t, acceptanceAction(t, s, fmt.Sprintf("durable-%03d", i), "receipt-owner", "schematic.component.modify"))
	}
	if calls.Load() != 320 || len(s.runtime.receipts) > 2 {
		t.Fatal("durable history leaked into the active cache or a request was replayed")
	}
	connector.CloseNow()
	acceptanceWaitDisconnected(t, s, "receipt-owner")
	if err := s.runtime.closeRecovery(); err != nil {
		t.Fatal(err)
	}
	restarted, nextBase := acceptanceServer(t, dir)
	_, nextCalls := acceptanceConnector(t, restarted, nextBase, "next-owner", "p", "d", nil)
	for i := 0; i < 320; i++ {
		found, err := restarted.runtime.recovery.HasRequest(fmt.Sprintf("durable-%03d", i))
		if err != nil || !found {
			t.Fatalf("durable record %d lost: %v", i, err)
		}
	}
	duplicate := acceptanceAction(t, restarted, "durable-000", "next-owner", "schematic.component.modify")
	if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || nextCalls.Load() != 0 {
		t.Fatal("an old ID outside the former cache limit was executed again")
	}
	acceptanceWantOK(t, acceptanceAction(t, restarted, "durable-new-save", "next-owner", "schematic.save"))
}

package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func acceptanceFixture(t *testing.T) (*Server, map[string]any, *[]protocol.Request) {
	return acceptanceFixtureWithReply(t, nil)
}

func acceptanceFixtureWithReply(t *testing.T, alter func(protocol.Request, map[string]any), adjust ...func(protocol.Request, *protocol.Response)) (*Server, map[string]any, *[]protocol.Request) {
	t.Helper()
	paused := false
	activeDocument := "d"
	calls := []protocol.Request{}
	s := replacementFixture(t, func(req protocol.Request) protocol.Response {
		calls = append(calls, req)
		if req.Action == "debug.control" {
			if req.Payload["operation"] == "pause" {
				paused = true
			}
			if req.Payload["operation"] == "resume" {
				paused = false
			}
			response := replacementResponse(paused)
			if alter != nil {
				alter(req, response.Result)
			}
			return response
		}
		result := map[string]any{"_scene": map[string]any{"instance": "fresh-scene", "sequence": 0}}
		switch req.Action {
		case "project.current":
			result["uuid"] = "p"
		case "schematic.pages.list":
			page := map[string]any{"uuid": "d", "parentSchematicUuid": "sch"}
			result["schematics"] = []any{map[string]any{"uuid": "sch", "parentProjectUuid": "p", "page": []any{page}}}
			result["pages"] = []any{page}
		case "board.list":
			result["boards"], result["count"] = []any{map[string]any{"name": "board", "parentProjectUuid": "p", "schematicUuid": "sch", "pcbUuid": "pcb"}}, 1
		case "schematic.save":
			result["saved"] = true
		case "schematic.titleblock.modify":
			result["modified"] = true
		case "document.close":
			result["closed"], result["uuid"], result["tabId"] = true, "d", "tab-d"
			activeDocument = "other"
		case "document.open":
			result["ready"], result["tabId"] = true, "tab-d"
			activeDocument = runtimeString(req.Payload["uuid"])
		case "debug.batch":
			if req.ID == "accepted-paused" {
				result["paused"], result["completed"], result["total"] = true, 1, 2
				result["results"] = []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true}}
				result["_baseline"] = map[string]any{"marker": "accepted-task-baseline"}
				result["_readback"] = []any{map[string]any{"action": "schematic.components.list", "result": map[string]any{"components": []any{map[string]any{"primitiveId": "completed"}}}}}
			} else {
				result["completed"], result["total"] = 1, 1
				result["results"] = []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true}}
			}
		}
		if alter != nil {
			alter(req, result)
		}
		response := protocol.Response{OK: true, Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: activeDocument, DocumentType: "schematic", TabID: "tab-" + activeDocument}, Result: result}
		if len(adjust) > 0 {
			adjust[0](req, &response)
		}
		return response
	})
	store := s.runtime.recovery.(*recoveryStore)
	old := &recoveryAttempt{RequestID: "old-write", WindowID: "old-window", Context: protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}, ScopeKey: "old-window|p|d", State: recoveryAttemptUnknown, TaskID: "old-task", Dangerous: true, ContentWrite: true, ProjectWide: true,
		Request: protocol.Request{Envelope: protocol.Envelope{ID: "old-write", WindowID: "old-window"}, Action: "debug.exec_js", Payload: map[string]any{"_execution": map[string]any{"clientId": "old-client", "revision": 0}, "code": "uncertain mutation"}}}
	if err := store.SaveAttempt(old); err != nil {
		t.Fatal(err)
	}
	oldScope := &recoveryScope{Key: "old-window|p|d", Document: &runtimeDocument{WindowID: "old-window", Context: old.Context, ConnectionEpoch: "old-epoch", ChangesSinceSave: true}, Requests: map[string]protocol.Request{}, Baselines: map[string]any{}}
	if err := store.SaveScope(oldScope); err != nil {
		t.Fatal(err)
	}
	status := auditEntry{RequestID: "old-terminal-status", WindowID: "terminal-collector", Action: "debug.control", OK: true, Payload: map[string]any{"operation": "status", "requestId": "old-write"}, Result: map[string]any{"requests": []any{map[string]any{"id": "old-write", "clientId": "old-client", "revision": 0, "started": true, "settled": true, "state": "settled_after_cancel", "arrivedAt": 1000, "finishedAt": 2000, "error": "exec_js failed."}}}}
	raw, _ := json.Marshal(status)
	initial := status
	initial.RequestID, initial.WindowID = "old-initial-status", "initial-collector"
	initial.Result = map[string]any{"requests": []any{map[string]any{"id": "old-write", "clientId": "old-client", "revision": 0, "started": true, "settled": false, "state": "expired_in_flight", "arrivedAt": 1000}}}
	initialRaw, _ := json.Marshal(initial)
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = recoveryEvidence(tx, "legacy-audit", status.RequestID, raw, "test-original-audit"); err != nil {
		t.Fatal(err)
	}
	if err = recoveryEvidence(tx, "legacy-audit", initial.RequestID, initialRaw, "test-original-audit"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	_, digest, err := store.LoadAttemptEvidence(old.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	d := s.runtime.documents["w|p|d"]
	input := map[string]any{"operation": "task_accept_current_baseline", "window": "w", "target": map[string]any{"projectUuid": "p", "documentUuid": "d"}, "expectedRevision": 1, "generation": d.Generation,
		"task": map[string]any{"goal": "new page edit from accepted current state", "primitiveIds": []string{}, "remaining": []string{"edit current page", "save"}},
		"acceptance": map[string]any{"acceptCurrentState": true, "connectionEpoch": d.ConnectionEpoch, "reason": "User accepts observed page as the new starting point",
			"attempts":          []any{map[string]any{"requestId": "old-write", "revision": old.Revision, "sha256": digest, "scopeKey": old.ScopeKey, "windowId": old.WindowID}},
			"unsavedScopes":     []any{map[string]any{"key": oldScope.Key, "revision": oldScope.Revision, "connectionEpoch": "old-epoch"}},
			"nativeEndEvidence": map[string]any{"type": "manual_session_termination_attestation", "attestedBy": "user", "oldWindow": "old-window", "statement": "I confirm the old host session has ended and accept the observed current page state", "terminal": []any{map[string]any{"requestId": old.RequestID, "statusRequestId": status.RequestID, "sha256": recoveryHash(raw), "initialStatusRequestId": initial.RequestID, "initialSha256": recoveryHash(initialRaw)}}}}}
	preview := acceptanceCall(s, map[string]any{"operation": "task_accept_current_baseline", "window": "w", "target": input["target"], "acceptance": map[string]any{"acceptCurrentState": false, "requestIds": []string{"old-write"}}})
	var observed map[string]any
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &observed) != nil {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	runtimeMap(input["acceptance"])["expectedBaselineSha256"] = observed["baselineSha256"]
	input["generation"] = runtimeMap(observed["current"])["generation"]
	calls = nil
	return s, input, &calls
}

func acceptanceCall(s *Server, input map[string]any) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(input)
	return runtimeTestControl(s, string(raw))
}

func TestRuntimeManualAcceptancePublicResponseKeepsInternalScene(t *testing.T) {
	for _, failure := range []string{"", "changed", "missing", "changed-during-action"} {
		t.Run(failure, func(t *testing.T) {
			active := false
			s, input, _ := acceptanceFixtureWithReply(t, func(req protocol.Request, result map[string]any) {
				if active && req.Action == "board.list" {
					switch failure {
					case "changed":
						runtimeMap(result["_scene"])["sequence"] = 1
					case "missing":
						delete(result, "_scene")
					case "changed-during-action":
						runtimeMap(result["_scene"])["changedDuringAction"] = true
					}
				}
			})
			active = true
			call := func(body map[string]any) *httptest.ResponseRecorder {
				raw, _ := json.Marshal(body)
				r := httptest.NewRequest("POST", "/runtime/control", strings.NewReader(string(raw)))
				r.Header.Set("X-Easyeda-Control", "1")
				r.Header.Set("X-Easyeda-Response", "public-v1")
				w := httptest.NewRecorder()
				s.handleRuntimeControl(w, r)
				if r.Header.Get("X-Easyeda-Response") != "public-v1" {
					t.Fatal("internal read changed the outer response header")
				}
				if strings.Contains(w.Body.String(), "_scene") {
					t.Fatal("private scene leaked into control response")
				}
				return w
			}
			preview := map[string]any{"operation": "task_accept_current_baseline", "window": "w", "target": input["target"], "acceptance": map[string]any{"acceptCurrentState": false, "requestIds": []string{"old-write"}}}
			want := 200
			if failure != "" {
				want = 409
			}
			for _, body := range []map[string]any{preview, input} {
				if response := call(body); response.Code != want {
					t.Fatalf("control status %d, want %d: %s", response.Code, want, response.Body.String())
				}
			}
			if failure == "" {
				task := s.runtime.documents["w|p|d"].Task
				stored, err := s.runtime.recovery.(*recoveryStore).LoadDisposition("w|p|d", task.ID, task.Revision)
				if err != nil || stored == nil || runtimeMap(runtimeMap(runtimeMap(stored.Basis)["project.current"])["result"])["_scene"] == nil {
					t.Fatalf("public projection changed durable scene evidence: %v", err)
				}
			}
		})
	}
}

func TestRuntimeManualAcceptanceAllowsOnlyNewTypedTaskAndPreservesUnknown(t *testing.T) {
	s, input, calls := acceptanceFixture(t)
	response := acceptanceCall(s, input)
	if response.Code != 200 {
		t.Fatalf("acceptance: %d %s", response.Code, response.Body.String())
	}
	if len(*calls) != 6 {
		t.Fatalf("unexpected native work: %+v", *calls)
	}
	for i, action := range []string{"debug.control", "project.current", "schematic.pages.list", "board.list", "debug.control", "debug.control"} {
		if (*calls)[i].Action != action {
			t.Fatal("acceptance mutated EDA or changed read/control order")
		}
	}
	d := s.runtime.documents["w|p|d"]
	if d.Task.ID == "" || d.Task.Revision != 2 || len(d.Task.Remaining) != 2 {
		t.Fatal("new task identity/remaining intent was lost")
	}
	old, err := s.runtime.recovery.LoadAttempt("old-write")
	if err != nil || old.State != recoveryAttemptUnknown {
		t.Fatal("manual acceptance settled the old unknown result")
	}
	index, _ := s.runtime.recovery.ListIndex()
	for _, scope := range index {
		if scope.Key == "old-window|p|d" && !scope.ChangesSinceSave {
			t.Fatal("old unsaved owner was cleared")
		}
	}
	if response := runtimeTestControl(s, `{"operation":"resume","window":"w","requestId":"old-write"}`); response.Code != 409 {
		t.Fatal("unknown request resumed")
	}
	if response := runtimeTestControl(s, `{"operation":"resume_queue","window":"w"}`); response.Code != 200 {
		t.Fatal("explicit queue resume failed")
	}
	for _, action := range []string{"schematic.titleblock.modify", "schematic.save"} {
		probe := httptest.NewRecorder()
		request := httptest.NewRequest("POST", "/action", strings.NewReader(`{"id":"new-`+action+`","windowId":"w","action":"`+action+`","payload":{"_target":{"projectUuid":"p","documentUuid":"d"}}}`))
		s.handleAction(probe, request)
		if probe.Code != 200 {
			t.Fatalf("new typed task blocked: %d %s", probe.Code, probe.Body.String())
		}
	}
	if d.ChangesSinceSave || d.SavedAt == nil {
		t.Fatal("normal save did not retain its existing result semantics")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "close-accepted", "w", "document.close", map[string]any{"uuid": "d", "tabId": "tab-d", "_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}}))
	acceptanceWantOK(t, acceptanceAction(t, s, "reopen-accepted", "w", "document.open", map[string]any{"uuid": "d", "_target": map[string]any{"projectUuid": "p", "documentUuid": "other"}}))
	if response := acceptanceCall(s, input); response.Code != 409 {
		t.Fatal("stale acceptance replaced current task")
	}
}

func TestRuntimeManualAcceptanceFailsClosedBeforeControls(t *testing.T) {
	for _, failure := range []string{"no-attestation", "queue-only-proof", "wrong-terminal-hash", "wrong-attempt-hash", "stale-generation", "old-host-live", "foreign-project"} {
		t.Run(failure, func(t *testing.T) {
			s, input, calls := acceptanceFixture(t)
			acceptance := runtimeMap(input["acceptance"])
			proof := runtimeMap(acceptance["nativeEndEvidence"])
			switch failure {
			case "no-attestation":
				proof["attestedBy"] = "assistant"
			case "queue-only-proof":
				proof["type"] = "queue-idle"
			case "wrong-terminal-hash":
				runtimeMap(runtimeArray(proof["terminal"])[0])["sha256"] = strings.Repeat("0", 64)
			case "wrong-attempt-hash":
				runtimeMap(runtimeArray(acceptance["attempts"])[0])["sha256"] = strings.Repeat("0", 64)
			case "stale-generation":
				input["generation"] = 100
			case "old-host-live":
				proof["oldWindow"] = "w"
			case "foreign-project":
				runtimeMap(input["target"])["projectUuid"] = "foreign"
			}
			response := acceptanceCall(s, input)
			if response.Code != 409 || len(*calls) != 0 || s.runtime.documents["w|p|d"].Task.Revision != 1 {
				t.Fatalf("unproven acceptance changed state: %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRuntimeManualAcceptanceDoesNotBypassOtherUnknownOrTaskEpoch(t *testing.T) {
	s, input, _ := acceptanceFixture(t)
	if response := acceptanceCall(s, input); response.Code != 200 {
		t.Fatalf("acceptance: %d %s", response.Code, response.Body.String())
	}
	d := s.runtime.documents["w|p|d"]
	req := protocol.Request{Envelope: protocol.Envelope{ID: "new", WindowID: "w"}, Action: "schematic.save", Payload: map[string]any{"_taskParent": "scoped-continuation", "_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}}}
	check := func() error { return s.runtime.checkExecutionScopeLocked(req, &d.Context) }
	if err := check(); err != nil {
		t.Fatalf("current task should be admitted: %v", err)
	}
	for _, action := range []string{"debug.exec_js", "project.create", "document.open", "schematic.page.create"} {
		original := req.Action
		req.Action = action
		if err := check(); !errors.Is(err, errRuntimeScopeUnknown) {
			t.Fatalf("scope acceptance granted %s: %v", action, err)
		}
		req.Action = original
	}
	originalRevision := d.Task.Revision
	d.Task.Revision++
	if err := check(); !errors.Is(err, errRuntimeScopeUnknown) {
		t.Fatal("task revision change retained acceptance")
	}
	d.Task.Revision = originalRevision
	originalEpoch := s.runtime.connectionEpoch["w"]
	s.runtime.connectionEpoch["w"] = "another-epoch"
	if err := check(); !errors.Is(err, errRuntimeScopeUnknown) {
		t.Fatal("reconnect retained acceptance")
	}
	s.runtime.connectionEpoch["w"] = originalEpoch
	other := &recoveryAttempt{RequestID: "another-unknown", State: recoveryAttemptUnknown, Dangerous: true, Context: d.Context, WindowID: "w", UpdatedAt: time.Now().UTC()}
	if err := s.runtime.recovery.SaveAttempt(other); err != nil {
		t.Fatal(err)
	}
	if err := check(); !errors.Is(err, errRuntimeScopeUnknown) {
		t.Fatal("acceptance bypassed a different unknown write")
	}
}

func TestRuntimeManualAcceptanceContinuationRejectsRawDryRunAndBatchedNavigation(t *testing.T) {
	s, input, calls := acceptanceFixture(t)
	if response := acceptanceCall(s, input); response.Code != 200 {
		t.Fatalf("acceptance: %d %s", response.Code, response.Body.String())
	}
	before := len(*calls)
	d := s.runtime.documents["w|p|d"]
	d.ChangesSinceSave = true
	for index, test := range []struct {
		action  string
		payload map[string]any
	}{
		{"debug.exec_js", map[string]any{"dryRun": true, "code": "native write"}},
		{"project.create", map[string]any{"dryRun": true, "friendlyName": "not-a-preview"}},
		{"document.close", map[string]any{"uuid": "d", "tabId": "tab-d"}},
		{"debug.batch", map[string]any{"steps": []any{map[string]any{"action": "document.close", "payload": map[string]any{"uuid": "d", "tabId": "tab-d"}}}}},
		{"debug.batch", map[string]any{"steps": []any{map[string]any{"action": "document.open", "payload": map[string]any{"uuid": "other"}}}}},
	} {
		test.payload["_target"] = map[string]any{"projectUuid": "p", "documentUuid": "d"}
		response := acceptanceContinuationAction(t, s, fmt.Sprintf("forbidden-%d", index), "w", test.action, test.payload)
		if response.OK || response.Error == nil || response.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || len(*calls) != before {
			t.Fatalf("forbidden action dispatched: %+v", response.Error)
		}
	}
	for _, action := range []string{"debug.exec_js", "project.create", "schematic.component.place", "schematic.component.modify", "schematic.attribute.modify", "schematic.save"} {
		payload := map[string]any{"dryRun": true}
		if !runtimeDangerous(protocol.Request{Action: action, Payload: payload}) || (mutatesAction[action] && !strings.HasSuffix(action, ".save") && !runtimeContentWrite(action, payload)) {
			t.Fatalf("invented dryRun muted %s", action)
		}
	}
}

func TestRuntimeLocalRejectionsCannotRewriteRecoveredAttempts(t *testing.T) {
	s := New(Options{AuditDir: t.TempDir()})
	t.Cleanup(func() { _ = s.runtime.closeRecovery() })
	ctx := protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	d := s.runtime.document("w", &ctx)
	for _, state := range []recoveryAttemptState{recoveryAttemptPending, recoveryAttemptUnknown} {
		id := string(state)
		original := &recoveryAttempt{RequestID: id, State: state, WindowID: "w", Context: ctx, Request: protocol.Request{Envelope: protocol.Envelope{ID: id, WindowID: "w"}, Action: "debug.exec_js"}, Receipt: &runtimeReceipt{RequestID: id, Status: "needs_readback"}, Dangerous: true}
		if err := s.runtime.recovery.SaveAttempt(original); err != nil {
			t.Fatal(err)
		}
		_, hash, err := s.runtime.recovery.LoadAttemptEvidence(id)
		if err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"DUPLICATE_REQUEST", "RUNTIME_SCOPE_UNKNOWN"} {
			s.runtime.observe(auditEntry{runtimeNotDispatched: true, RequestID: id, WindowID: "w", Action: "debug.exec_js", ErrorCode: code, Context: &ctx})
			retained, newHash, err := s.runtime.recovery.LoadAttemptEvidence(id)
			if err != nil || newHash != hash || retained.State != state || retained.Revision != original.Revision || len(s.runtime.receipts) != 0 {
				t.Fatal("local refusal rewrote recovered attempt/receipt", err)
			}
		}
	}
	for _, marked := range []bool{false, true} {
		id := fmt.Sprintf("fresh-%t", marked)
		s.runtime.observe(auditEntry{runtimeNotDispatched: marked, RequestID: id, WindowID: "w", Action: "debug.exec_js", ErrorCode: "RUNTIME_SCOPE_UNKNOWN", Context: &ctx})
		if err := s.runtime.saveScopeLocked(d); err != nil {
			t.Fatal(err)
		}
		attempt, _ := s.runtime.recovery.LoadAttempt(id)
		if marked {
			if attempt.State != recoveryAttemptNotDispatched || !attempt.ExecutionKnown || attempt.Executed {
				t.Fatal("explicit local rejection lost known-unsent result")
			}
		} else if attempt.State != recoveryAttemptUnknown {
			t.Fatal("unmarked error acquired no-dispatch proof")
		}
	}
}

func TestRuntimeManualAcceptanceBindsReviewedBaselineAndRollsBackFailure(t *testing.T) {
	s, input, calls := acceptanceFixture(t)
	runtimeMap(input["acceptance"])["expectedBaselineSha256"] = strings.Repeat("0", 64)
	response := acceptanceCall(s, input)
	if response.Code != 409 || len(*calls) != 4 || s.runtime.documents["w|p|d"].Task.Revision != 1 {
		t.Fatalf("unreviewed baseline committed: %d %s", response.Code, response.Body.String())
	}
}

func TestRuntimeManualAcceptanceReopenKeepsSourceScopeGuards(t *testing.T) {
	for _, kind := range []string{"unknown", "unsaved"} {
		t.Run(kind, func(t *testing.T) {
			s, input, calls := acceptanceFixture(t)
			if response := acceptanceCall(s, input); response.Code != 200 {
				t.Fatalf("acceptance: %d %s", response.Code, response.Body.String())
			}
			acceptanceWantOK(t, acceptanceAction(t, s, "save-before-close", "w", "schematic.save", map[string]any{"_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}}))
			acceptanceWantOK(t, acceptanceAction(t, s, "close-before-source-guard", "w", "document.close", map[string]any{"uuid": "d", "tabId": "tab-d", "_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}}))
			ctx := protocol.Context{ProjectUUID: "p", DocumentUUID: "other", DocumentType: "schematic"}
			if kind == "unknown" {
				if err := s.runtime.recovery.SaveAttempt(&recoveryAttempt{RequestID: "source-unknown", WindowID: "source-owner", State: recoveryAttemptUnknown, Context: ctx, Dangerous: true}); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := s.runtime.recovery.SaveScope(&recoveryScope{Key: "source-owner|p|other", Document: &runtimeDocument{WindowID: "source-owner", Context: ctx, ChangesSinceSave: true, ConnectionEpoch: "source-epoch"}}); err != nil {
					t.Fatal(err)
				}
			}
			before := len(*calls)
			response := acceptanceContinuationAction(t, s, "guarded-reopen", "w", "document.open", map[string]any{"uuid": "d", "_target": map[string]any{"projectUuid": "p", "documentUuid": "other"}})
			if response.OK || response.Error == nil || response.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || len(*calls) != before {
				t.Fatal("reopen bypassed source scope", response.Error)
			}
		})
	}
}

func TestRuntimeManualAcceptanceNewTaskResumesOnlyConfirmedPrefix(t *testing.T) {
	s, input, calls := acceptanceFixture(t)
	if response := acceptanceCall(s, input); response.Code != 200 {
		t.Fatalf("acceptance: %d %s", response.Code, response.Body.String())
	}
	if response := runtimeTestControl(s, `{"operation":"resume_queue","window":"w"}`); response.Code != 200 {
		t.Fatal("explicit queue resume failed")
	}
	target := map[string]any{"projectUuid": "p", "documentUuid": "d"}
	steps := []any{map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "completed"}}, map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "remaining"}}}
	acceptanceWantOK(t, acceptanceAction(t, s, "accepted-paused", "w", "debug.batch", map[string]any{"_target": target, "steps": steps, "_edit": map[string]any{"primitiveIds": []any{"completed", "remaining"}}}))
	before := len(*calls)
	response := runtimeTestControl(s, `{"operation":"resume","window":"w","requestId":"accepted-paused"}`)
	if response.Code != 200 || len(*calls) != before+2 {
		t.Fatalf("new task resume rejected: %d %s", response.Code, response.Body.String())
	}
	continuation := (*calls)[len(*calls)-1]
	remaining := runtimeArray(continuation.Payload["steps"])
	if continuation.Action != "debug.batch" || len(remaining) != 1 || runtimeMap(runtimeMap(remaining[0])["payload"])["primitiveId"] != "remaining" || runtimeMap(continuation.Payload["_resumeBaseline"])["marker"] != "accepted-task-baseline" {
		t.Fatal("new task replayed a confirmed prefix or lost its basis")
	}
	before = len(*calls)
	for _, id := range []string{"accepted-paused", "old-write"} {
		response := runtimeTestControl(s, `{"operation":"resume","window":"w","requestId":"`+id+`"}`)
		if response.Code != 409 || len(*calls) != before {
			t.Fatal("consumed or unknown prefix replayed")
		}
	}
}

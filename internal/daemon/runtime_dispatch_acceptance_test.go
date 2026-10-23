package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Every transport and database belongs to this test; knowledge is unavailable.
func acceptanceServer(t *testing.T, auditDir string) (*Server, string) {
	t.Helper()
	if auditDir == "" {
		auditDir = t.TempDir()
	}
	s := New(Options{AuditDir: auditDir, ArtifactDir: t.TempDir()})
	s.knowledgeConnect = func(context.Context) (knowledgeConnection, error) {
		return knowledgeConnection{}, errors.New("knowledge disabled for acceptance")
	}
	t.Cleanup(func() { _ = s.runtime.closeRecovery() })
	server := httptest.NewServer(s.routes(0))
	t.Cleanup(server.Close)
	return s, strings.TrimPrefix(server.URL, "http://")
}

func acceptanceConnector(t *testing.T, s *Server, base, window, project, document string, reply func(protocol.Request) protocol.Response) (*websocket.Conn, *atomic.Int32) {
	t.Helper()
	connector := dialConnector(t, base, window)
	t.Cleanup(func() { connector.CloseNow() })
	waitForWindow(t, base, window)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	identity := protocol.Context{ProjectUUID: project, DocumentUUID: document, DocumentType: "schematic"}
	if err := wsjson.Write(ctx, connector, protocol.ContextMessage{Type: protocol.TypeContext, WindowID: window, ProjectUUID: project, DocumentUUID: document, DocumentType: "schematic"}); err != nil {
		t.Fatal("send synthetic context:", err)
	}
	windowIdentityBarrier(t, ctx, connector, "acceptance-context-"+window)
	count := new(atomic.Int32)
	go func() {
		for {
			var req protocol.Request
			if wsjson.Read(ctx, connector, &req) != nil {
				return
			}
			if req.Type != protocol.TypeRequest {
				continue
			}
			count.Add(1)
			response := protocol.Response{OK: true, Result: map[string]any{"saved": req.Action == "schematic.save", "paused": false}}
			if reply != nil {
				response = reply(req)
			}
			response.ID, response.Type = req.ID, protocol.TypeResponse
			if response.Context == nil {
				response.Context = &identity
			}
			if wsjson.Write(ctx, connector, response) != nil {
				return
			}
		}
	}()
	return connector, count
}

func acceptanceAction(t *testing.T, s *Server, id, window, action string, payload ...map[string]any) protocol.Response {
	t.Helper()
	input := map[string]any{"primitiveId": "synthetic-r"}
	if len(payload) != 0 {
		input = payload[0]
	}
	body, err := json.Marshal(map[string]any{"id": id, "windowId": window, "action": action, "payload": input})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/action", strings.NewReader(string(body)))
	request.Header.Set("X-Easyeda-Response", "public-v1")
	recorder := httptest.NewRecorder()
	s.handleAction(recorder, request)
	var response protocol.Response
	if json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
		t.Fatal("invalid public response")
	}
	return response
}

func acceptanceWantOK(t *testing.T, response protocol.Response) {
	t.Helper()
	if !response.OK {
		code := "missing"
		if response.Error != nil {
			code = response.Error.Code
		}
		t.Fatalf("synthetic action rejected: code=%s", code)
	}
}

func TestDispatchAcceptanceHistoryDoesNotConsumeActiveCapacity(t *testing.T) {
	dir := t.TempDir()
	legacy := acceptanceLegacy(t, dir, 8, 256)
	before := acceptanceFileHash(t, legacy)
	s, base := acceptanceServer(t, dir)
	_, calls := acceptanceConnector(t, s, base, "new", "new-project", "new-document", nil)
	acceptanceWantOK(t, acceptanceAction(t, s, "new-write", "new", "schematic.component.modify"))
	acceptanceWantOK(t, acceptanceAction(t, s, "new-save", "new", "schematic.save"))
	if calls.Load() != 2 || acceptanceFileHash(t, legacy) != before {
		t.Fatal("new work changed the legacy backup or was not dispatched exactly once")
	}
	for i := 0; i < 256; i++ {
		found, err := s.runtime.recovery.HasRequest(fmt.Sprintf("history-%03d", i))
		if err != nil || !found {
			t.Fatalf("historical request %d lost", i)
		}
	}
	if len(s.runtime.documents) > 8 || len(s.runtime.receipts) > 2 {
		t.Fatal("settled historical receipts accumulated in the active cache")
	}
}

func TestDispatchProjectInventoryKeepsUnknownScopeAndReplayGuards(t *testing.T) {
	s, base := acceptanceServer(t, "")
	_, calls := acceptanceConnector(t, s, base, "inventory-window", "inventory-project", "inventory-document", func(req protocol.Request) protocol.Response {
		if req.Action == "debug.control" {
			return protocol.Response{OK: true, Result: map[string]any{"targetChecked": true, "paused": false, "pending": 0, "inFlight": []any{}}}
		}
		if req.ID == "inventory-old-unknown" {
			return protocol.Response{OK: true, Result: map[string]any{"partial": true, "completed": 0, "total": 1}}
		}
		if req.ID == "inventory-read" && (req.Action != "project.find" || req.Payload["includeProjects"] != true || req.Payload["teamUuid"] != "authorized-team") {
			t.Error("unexpected action or inventory payload dispatched")
		}
		return protocol.Response{OK: true, Result: map[string]any{"projects": []any{}, "enumeration": map[string]any{"complete": false}}}
	})
	_ = acceptanceAction(t, s, "inventory-old-unknown", "inventory-window", "debug.exec_js", map[string]any{"code": "return null"})
	before := acceptanceUnresolved(t, s, "inventory-old-unknown")
	if before.State != recoveryAttemptUnknown || !before.ProjectWide {
		t.Fatal("fixture did not retain a project-wide unknown")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "inventory-read", "inventory-window", "project.find", map[string]any{
		"friendlyName": "Exact", "teamUuid": "authorized-team", "includeProjects": true,
	}))
	for _, action := range []string{"debug.exec_js", "project.open", "schematic.component.modify"} {
		acceptanceWantOK(t, acceptanceAction(t, s, "new-inventory-"+action, "inventory-window", action, map[string]any{"code": "return null", "projectUuid": "other-project", "allowDiscardUnsaved": true}))
	}
	_ = acceptanceAction(t, s, "inventory-old-unknown", "inventory-window", "debug.exec_js", map[string]any{"code": "return null"})
	if calls.Load() != 8 {
		t.Fatalf("expected original, inventory, three idle probes and three new calls only, got %d", calls.Load())
	}
	after := acceptanceUnresolved(t, s, "inventory-old-unknown")
	beforeJSON, _ := json.Marshal(before)
	afterJSON, _ := json.Marshal(after)
	if string(beforeJSON) != string(afterJSON) {
		t.Fatal("inventory or rejected replay changed original unknown evidence")
	}
}

func TestDispatchAcceptanceHistoryDoesNotBlockNewDocumentWork(t *testing.T) {
	dir := t.TempDir()
	acceptanceLegacy(t, dir, 1, 1)
	s, base := acceptanceServer(t, dir)
	_, blockedCalls := acceptanceConnector(t, s, base, "affected", "history-project-0", "history-document-0", nil)
	_, unrelatedCalls := acceptanceConnector(t, s, base, "unrelated", "new-project", "new-document", nil)
	_, otherDocumentCalls := acceptanceConnector(t, s, base, "other-document", "history-project-0", "different-document", nil)
	acceptanceWantOK(t, acceptanceAction(t, s, "new-write", "affected", "schematic.component.modify"))
	acceptanceWantOK(t, acceptanceAction(t, s, "affected-read", "affected", "schematic.components.list"))
	acceptanceWantOK(t, acceptanceAction(t, s, "unrelated-write", "unrelated", "schematic.component.modify"))
	acceptanceWantOK(t, acceptanceAction(t, s, "same-project-other-document-write", "other-document", "schematic.component.modify"))
	if blockedCalls.Load() != 2 || unrelatedCalls.Load() != 1 || otherDocumentCalls.Load() != 1 {
		t.Fatal("readback/unrelated write was not dispatched once")
	}
}

func TestDispatchAcceptanceReconnectRestartDoesNotLeakCapacity(t *testing.T) {
	dir := t.TempDir()
	for restart := 0; restart < 3; restart++ {
		s, base := acceptanceServer(t, dir)
		for round := 0; round < 10; round++ {
			window := fmt.Sprintf("round-%d-%d", restart, round)
			connector, _ := acceptanceConnector(t, s, base, window, "synthetic-project", "synthetic-document", nil)
			acceptanceWantOK(t, acceptanceAction(t, s, "write-"+window, window, "schematic.component.modify"))
			acceptanceWantOK(t, acceptanceAction(t, s, "save-"+window, window, "schematic.save"))
			connector.CloseNow()
			deadline := time.Now().Add(3 * time.Second)
			for {
				if _, connected := s.hub.target(window); !connected {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("synthetic connector did not disconnect")
				}
				time.Sleep(time.Millisecond)
			}
		}
		if err := s.runtime.closeRecovery(); err != nil {
			t.Fatal("cannot close synthetic recovery store")
		}
	}
}

func TestDispatchAcceptanceDuplicateRejectsButOldOwnerDoesNotBlockNewWork(t *testing.T) {
	s, base := acceptanceServer(t, "")
	connector, firstCalls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", nil)
	_, peerCalls := acceptanceConnector(t, s, base, "peer", "synthetic-project", "synthetic-document", nil)
	acceptanceWantOK(t, acceptanceAction(t, s, "owner-write", "owner", "schematic.component.modify"))
	duplicate := acceptanceAction(t, s, "owner-write", "peer", "schematic.component.modify")
	if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" {
		t.Fatal("duplicate request did not preserve its rejection")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "peer-write", "peer", "schematic.component.modify"))
	if firstCalls.Load() != 1 || peerCalls.Load() != 1 {
		t.Fatal("new explicitly routed peer write or duplicate executed incorrectly")
	}
	connector.CloseNow()
	acceptanceWaitDisconnected(t, s, "owner")
	_, reconnectedCalls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", nil)
	acceptanceWantOK(t, acceptanceAction(t, s, "unsaved-owner-reconnected", "owner", "schematic.component.modify"))
	if reconnectedCalls.Load() != 1 {
		t.Fatal("new request after reconnect did not dispatch exactly once")
	}
}

func acceptanceWaitDisconnected(t *testing.T, s *Server, window string) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); ; {
		if _, connected := s.hub.target(window); !connected {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("synthetic transport did not disconnect")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDispatchAcceptanceInflightOwnerBlocksOnlyRelatedProject(t *testing.T) {
	s, base := acceptanceServer(t, "")
	started, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	_, ownerCalls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(protocol.Request) protocol.Response {
		close(started)
		<-release
		return protocol.Response{OK: true, Result: map[string]any{"nativeResult": "completed"}}
	})
	_, peerCalls := acceptanceConnector(t, s, base, "peer", "synthetic-project", "synthetic-document", nil)
	_, unrelatedCalls := acceptanceConnector(t, s, base, "unrelated", "unrelated-project", "unrelated-document", nil)
	done := make(chan protocol.Response, 1)
	go func() { done <- acceptanceAction(t, s, "held-write", "owner", "schematic.component.modify") }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("owner write did not reach the synthetic connector")
	}
	peer := acceptanceAction(t, s, "conflicting-write", "peer", "schematic.component.modify")
	if peer.OK || peer.Error == nil || peer.Error.Code != "RUNTIME_SCOPE_BUSY" || peerCalls.Load() != 0 {
		t.Fatal("conflicting in-flight write reached the second window")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "independent-write", "unrelated", "schematic.component.modify"))
	close(release)
	select {
	case response := <-done:
		acceptanceWantOK(t, response)
	case <-time.After(3 * time.Second):
		t.Fatal("original owner did not settle")
	}
	if ownerCalls.Load() != 1 || unrelatedCalls.Load() != 1 {
		t.Fatal("owner or independent request dispatched more than once")
	}
}

func TestDispatchAcceptanceLateReplyDoesNotOverwriteSettledUnknown(t *testing.T) {
	s, base := acceptanceServer(t, "")
	started, release, replied := make(chan struct{}), make(chan struct{}), make(chan struct{})
	_, _ = acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(req protocol.Request) protocol.Response {
		if req.ID == "interrupted-write" {
			close(started)
			<-release
			close(replied)
		}
		return protocol.Response{OK: true, Result: map[string]any{"nativeResult": "synthetic-success"}}
	})
	ctx, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest("POST", "/action", strings.NewReader(`{"id":"interrupted-write","windowId":"owner","action":"schematic.component.modify","payload":{"primitiveId":"synthetic-r"}}`)).WithContext(ctx)
	request.Header.Set("X-Easyeda-Response", "public-v1")
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		recorder := httptest.NewRecorder()
		s.handleAction(recorder, request)
		done <- recorder
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("synthetic write not dispatched")
	}
	cancel()
	select {
	case recorder := <-done:
		if recorder.Code < 400 {
			t.Fatal("canceled write presented as completed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("interrupted request did not terminate")
	}
	s.runtime.mu.Lock()
	var old *runtimeReceipt
	for _, receipt := range s.runtime.receipts {
		if receipt.RequestID == "interrupted-write" {
			old = receipt
		}
	}
	before, _ := json.Marshal(old)
	s.runtime.mu.Unlock()
	if old == nil || old.Status != "needs_readback" {
		t.Fatal("interrupted write lost its unknown outcome")
	}
	close(release)
	<-replied
	acceptanceWantOK(t, acceptanceAction(t, s, "fresh-read", "owner", "document.current"))
	s.runtime.mu.Lock()
	after, _ := json.Marshal(old)
	s.runtime.mu.Unlock()
	if string(before) != string(after) {
		t.Fatal("late native reply rewrote settled unknown evidence")
	}
}

type acceptanceFailingStorage struct {
	recoveryStorage
	fail atomic.Bool
}

func (s *acceptanceFailingStorage) SaveScope(scope *recoveryScope) error {
	if s.fail.Load() {
		return errors.New("synthetic durable write failure")
	}
	return s.recoveryStorage.SaveScope(scope)
}

func (s *acceptanceFailingStorage) SaveAttempt(attempt *recoveryAttempt) error {
	if s.fail.Load() {
		return errors.New("synthetic durable write failure")
	}
	return s.recoveryStorage.SaveAttempt(attempt)
}

func TestDispatchAcceptancePublicPersistenceFailureRetainsExecution(t *testing.T) {
	s, base := acceptanceServer(t, "")
	storage := &acceptanceFailingStorage{recoveryStorage: s.runtime.recovery}
	s.runtime.recovery = storage
	_, calls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(protocol.Request) protocol.Response {
		storage.fail.Store(true)
		return protocol.Response{OK: true, Result: map[string]any{"nativeMarker": "synthetic-execution-completed", "saved": true}}
	})
	response := acceptanceAction(t, s, "executed-but-unpersisted", "owner", "schematic.component.modify")
	execution := runtimeMap(response.Result["execution"])
	original := runtimeMap(execution["response"])
	if response.ID != "executed-but-unpersisted" || response.OK || response.Error == nil || response.Error.Code != "RECOVERY_PERSISTENCE_FAILED" || runtimeMap(response.Result["persistence"])["ok"] != false || response.Result["retryable"] != false {
		t.Fatal("public response hid the persistence failure or original request identity")
	}
	if execution["known"] != true || execution["requestId"] != response.ID || original["ok"] != true || runtimeMap(original["result"])["nativeMarker"] != "synthetic-execution-completed" {
		t.Fatal("public projection lost the actual native result")
	}
	second := acceptanceAction(t, s, "forbidden-retry", "owner", "schematic.component.modify")
	if second.OK || calls.Load() != 1 {
		t.Fatal("uncommitted execution was replayed")
	}
}

func TestDispatchAcceptanceUnprovedContinuationNeverDispatches(t *testing.T) {
	for _, status := range []string{"needs_readback", "historical-paused", "stale-revision", "missing-proof"} {
		t.Run(status, func(t *testing.T) {
			s, base := acceptanceServer(t, "")
			_, calls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", nil)
			identity := &protocol.Context{ProjectUUID: "synthetic-project", DocumentUUID: "synthetic-document"}
			s.runtime.mu.Lock()
			d := s.runtime.document("owner", identity)
			d.Task = &runtimeTask{ID: "synthetic-task", Revision: 2, Goal: "synthetic current intent"}
			receipt := &runtimeReceipt{RequestID: "unproved", WindowID: "owner", ProjectUUID: identity.ProjectUUID, DocumentUUID: identity.DocumentUUID, Status: "paused", TaskRevision: 2, Generation: d.Generation, Progress: map[string]any{"completed": 0, "results": []any{}}, ObservedFacts: map[string]any{"synthetic": true}}
			switch status {
			case "needs_readback":
				receipt.Status = "needs_readback"
			case "historical-paused":
				receipt.Historical = true
			case "stale-revision":
				receipt.TaskRevision = 1
			case "missing-proof":
				receipt.ObservedFacts = nil
			}
			s.runtime.receipts = append(s.runtime.receipts, receipt)
			s.runtime.requests["unproved"] = protocol.Request{Envelope: protocol.Envelope{ID: "unproved", WindowID: "owner"}, Action: "debug.batch", Payload: map[string]any{"steps": []any{map[string]any{"action": "schematic.component.modify"}}}}
			s.runtime.baselines["unproved"] = map[string]any{"synthetic": true}
			s.runtime.mu.Unlock()
			response := runtimeTestControl(s, `{"operation":"resume","window":"owner","requestId":"unproved"}`)
			if response.Code != 409 || calls.Load() != 0 {
				t.Fatal("unproved continuation dispatched")
			}
		})
	}
}

func TestDispatchAcceptanceProcessInterruptionFailsClosed(t *testing.T) {
	if dir := os.Getenv("EASYEDA_DISPATCH_FAULT_CHILD_DIR"); dir != "" {
		s, base := acceptanceServer(t, dir)
		_, _ = acceptanceConnector(t, s, base, "interrupted-owner", "synthetic-project", "synthetic-document", func(protocol.Request) protocol.Response {
			os.Exit(0) // Dispatch intent is durable; no native completion can be inferred.
			return protocol.Response{}
		})
		_ = acceptanceAction(t, s, "process-interrupted-write", "interrupted-owner", "schematic.component.modify")
		t.Fatal("fault subprocess did not exit after dispatch")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDispatchAcceptanceProcessInterruptionFailsClosed$", "-test.timeout=10s")
	command.Env = append(os.Environ(), "EASYEDA_DISPATCH_FAULT_CHILD_DIR="+dir, "TMP="+dir, "TEMP="+dir)
	if err := command.Run(); err != nil {
		t.Fatal("synthetic dispatch fault subprocess failed")
	}
	s, base := acceptanceServer(t, dir)
	_, calls := acceptanceConnector(t, s, base, "restarted", "synthetic-project", "synthetic-document", nil)
	response := acceptanceAction(t, s, "unsafe-replay", "restarted", "schematic.component.modify")
	if response.OK || response.Error == nil || (response.Error.Code != "RUNTIME_SCOPE_BUSY" && response.Error.Code != "RUNTIME_SCOPE_UNKNOWN") || calls.Load() != 0 {
		t.Fatal("process interruption released an unresolved dispatched write")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "post-interruption-read", "restarted", "document.current"))
}

func TestDispatchAcceptanceEvictedPausedProofRequiresSameConnection(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart-%v", restart), func(t *testing.T) {
			dir := t.TempDir()
			s, base := acceptanceServer(t, dir)
			seen := make(chan protocol.Request, 8)
			reply := func(req protocol.Request) protocol.Response {
				seen <- req
				if req.ID == "pausing-batch" {
					return protocol.Response{OK: true, Result: map[string]any{"paused": true, "completed": 1, "total": 2, "results": []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true}}, "_baseline": map[string]any{"marker": "synthetic-durable-baseline"}, "_readback": []any{map[string]any{"action": "schematic.components.list", "result": map[string]any{"components": []any{map[string]any{"primitiveId": "completed"}}}}}}}
				}
				return protocol.Response{OK: true, Result: map[string]any{"paused": false}}
			}
			connector, _ := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", reply)
			steps := []any{map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "completed"}}, map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "remaining"}}}
			acceptanceWantOK(t, acceptanceAction(t, s, "pausing-batch", "owner", "debug.batch", map[string]any{"steps": steps, "_edit": map[string]any{"primitiveIds": []any{"completed", "remaining"}}}))
			<-seen
			s.runtime.mu.Lock()
			key := s.runtime.current["owner"]
			if err := s.runtime.saveScopeLocked(s.runtime.documents[key]); err != nil {
				s.runtime.mu.Unlock()
				t.Fatal("could not persist synthetic paused scope")
			}
			s.runtime.reclaimDocument(key)
			s.runtime.mu.Unlock()
			if restart {
				connector.CloseNow()
				if err := s.runtime.closeRecovery(); err != nil {
					t.Fatal("could not close synthetic paused scope")
				}
				s, base = acceptanceServer(t, dir)
				_, _ = acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", reply)
			}
			response := runtimeTestControl(s, `{"operation":"resume","window":"owner","requestId":"pausing-batch"}`)
			if restart {
				if response.Code != 409 || len(seen) != 0 {
					t.Fatal("persisted prefix became fresh replay authorization after restart")
				}
				return
			}
			if response.Code != 200 || len(seen) != 2 {
				t.Fatalf("same-connection cache eviction lost confirmed continuation: status=%d dispatches=%d", response.Code, len(seen))
			}
			<-seen
			continued := <-seen
			remaining := runtimeArray(continued.Payload["steps"])
			if continued.Action != "debug.batch" || len(remaining) != 1 || runtimeMap(runtimeMap(remaining[0])["payload"])["primitiveId"] != "remaining" || runtimeMap(continued.Payload["_resumeBaseline"])["marker"] != "synthetic-durable-baseline" {
				t.Fatal("evicted continuation lost private basis or replayed a completed action")
			}
		})
	}
}

func acceptanceUnresolved(t *testing.T, s *Server, id string) recoveryAttempt {
	t.Helper()
	attempts, err := s.runtime.recovery.ListUnresolved()
	if err != nil {
		t.Fatal("could not inspect synthetic recovery attempts")
	}
	for _, attempt := range attempts {
		if attempt.RequestID == id {
			return attempt
		}
	}
	t.Fatal("synthetic unresolved execution was not retained")
	return recoveryAttempt{}
}

func TestDispatchAcceptanceSaveLostAcknowledgmentRemainsUnknown(t *testing.T) {
	s, base := acceptanceServer(t, "")
	nativeSaved := new(atomic.Bool)
	var socket *websocket.Conn
	socket, nativeCalls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(req protocol.Request) protocol.Response {
		if req.Action == "schematic.save" {
			nativeSaved.Store(true)
			socket.CloseNow() // The native save finished; its response never reaches the daemon.
		}
		return protocol.Response{OK: true, Result: map[string]any{"saved": true}}
	})
	response := acceptanceAction(t, s, "save-response-lost", "owner", "schematic.save")
	if response.OK || response.Error == nil || response.Error.Code != "DISPATCH_FAILED" || !nativeSaved.Load() || nativeCalls.Load() != 1 {
		t.Fatal("lost save acknowledgment was retried or presented as confirmed")
	}
	attempt := acceptanceUnresolved(t, s, "save-response-lost")
	if attempt.State != recoveryAttemptUnknown || attempt.ExecutionKnown || attempt.Executed {
		t.Fatal("save without an acknowledgment lost its unknown execution boundary")
	}
	if _, retained := s.runtime.attempts["save-response-lost"]; retained {
		t.Fatal("persisted unknown save retained its full active payload")
	}
	_, newCalls := acceptanceConnector(t, s, base, "reconnected", "synthetic-project", "synthetic-document", nil)
	retry := acceptanceAction(t, s, "save-response-lost", "reconnected", "schematic.save")
	if retry.OK || retry.Error == nil || retry.Error.Code != "DUPLICATE_REQUEST" || newCalls.Load() != 0 {
		t.Fatal("unknown save was automatically replayed after reconnect")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "save-readback", "reconnected", "document.current"))
	if newCalls.Load() != 1 {
		t.Fatal("unknown save blocked independent readback")
	}
}

func TestDispatchAcceptanceUnconfirmedSaveRemainsUnknownWithoutBlockingNewWork(t *testing.T) {
	for _, outcome := range []string{"partial", "error", "saved-null"} {
		t.Run(outcome, func(t *testing.T) {
			s, base := acceptanceServer(t, "")
			_, calls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(req protocol.Request) protocol.Response {
				if req.Action == "debug.control" {
					return protocol.Response{OK: true, Result: map[string]any{"targetChecked": true, "paused": false, "pending": 0, "inFlight": []any{}}}
				}
				if req.Action != "schematic.save" {
					return protocol.Response{OK: true, Result: map[string]any{}}
				}
				response := protocol.Response{OK: true, Result: map[string]any{"saved": nil}}
				switch outcome {
				case "partial":
					response.Result["partial"], response.Result["saved"] = true, true
				case "error":
					response.OK = false
					response.Error = &protocol.ErrorInfo{Code: "SYNTHETIC_SAVE_FAILED", Message: "synthetic save failure"}
				}
				return response
			})
			_ = acceptanceAction(t, s, "unconfirmed-save", "owner", "schematic.save")
			attempt := acceptanceUnresolved(t, s, "unconfirmed-save")
			if attempt.State != recoveryAttemptUnknown || attempt.ContentWrite || !attempt.Dangerous || !attempt.ExecutionKnown {
				t.Fatal("unconfirmed non-content save was discarded as settled")
			}
			if _, retained := s.runtime.attempts["unconfirmed-save"]; retained {
				t.Fatal("persisted unknown outcome retained its full active payload")
			}
			acceptanceWantOK(t, acceptanceAction(t, s, "new-after-save", "owner", "schematic.component.modify"))
			if calls.Load() != 3 {
				t.Fatal("expected save, native idle probe and one new action")
			}
			if acceptanceUnresolved(t, s, "unconfirmed-save").State != recoveryAttemptUnknown {
				t.Fatal("new action changed unconfirmed save into success")
			}
			acceptanceWantOK(t, acceptanceAction(t, s, "after-save-read", "owner", "document.current"))
		})
	}
}

func TestDispatchAcceptanceSavedOwnerReconnectAllowsRepeatedWrites(t *testing.T) {
	for _, restart := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart-%v", restart), func(t *testing.T) {
			dir := t.TempDir()
			s, base := acceptanceServer(t, dir)
			connector, _ := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", nil)
			acceptanceWantOK(t, acceptanceAction(t, s, "before-reconnect-write", "owner", "schematic.component.modify"))
			acceptanceWantOK(t, acceptanceAction(t, s, "before-reconnect-save", "owner", "schematic.save"))
			connector.CloseNow()
			acceptanceWaitDisconnected(t, s, "owner")
			if restart {
				if err := s.runtime.closeRecovery(); err != nil {
					t.Fatal("could not close the saved owner recovery store")
				}
				s, base = acceptanceServer(t, dir)
			}
			_, calls := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", nil)
			acceptanceWantOK(t, acceptanceAction(t, s, "after-reconnect-first-write", "owner", "schematic.component.modify"))
			acceptanceWantOK(t, acceptanceAction(t, s, "after-reconnect-second-write", "owner", "schematic.component.modify"))
			acceptanceWantOK(t, acceptanceAction(t, s, "after-reconnect-save", "owner", "schematic.save"))
			if calls.Load() != 3 {
				t.Fatal("saved owner reconnect did not execute two writes and one save exactly once")
			}
		})
	}
}

func TestDispatchAcceptanceRestartUnknownHonorsDocumentAndRawScope(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(fmt.Sprintf("raw-%v", raw), func(t *testing.T) {
			dir := t.TempDir()
			s, base := acceptanceServer(t, dir)
			connector, _ := acceptanceConnector(t, s, base, "old-owner", "synthetic-project", "affected-document", func(protocol.Request) protocol.Response {
				return protocol.Response{OK: true, Result: map[string]any{"partial": true, "completed": 0, "total": 1}}
			})
			action := "schematic.component.modify"
			payload := map[string]any{"primitiveId": "synthetic-r"}
			if raw {
				action = "debug.batch"
				payload = map[string]any{"steps": []any{map[string]any{"action": "debug.exec_js", "payload": map[string]any{"code": "return null"}}}}
			}
			_ = acceptanceAction(t, s, "uncertain-original", "old-owner", action, payload)
			if acceptanceUnresolved(t, s, "uncertain-original").State != recoveryAttemptUnknown {
				t.Fatal("original uncertain action was not retained")
			}
			connector.CloseNow()
			if s.runtime.closeRecovery() != nil {
				t.Fatal("close synthetic uncertain scope")
			}
			restarted, nextBase := acceptanceServer(t, dir)
			_, calls := acceptanceConnector(t, restarted, nextBase, "new-owner", "synthetic-project", "different-document", nil)
			response := acceptanceAction(t, restarted, "different-document-write", "new-owner", "schematic.component.modify")
			acceptanceWantOK(t, response)
			if calls.Load() != 1 || acceptanceUnresolved(t, restarted, "uncertain-original").State != recoveryAttemptUnknown {
				t.Fatal("new request blocked or old uncertainty rewritten after restart")
			}
		})
	}
}

func TestDispatchAcceptanceEvictedReceiptReloadsOriginalAttemptOnly(t *testing.T) {
	for _, mode := range []string{"same-connection", "different-epoch", "corrupt-attempt"} {
		t.Run(mode, func(t *testing.T) {
			s, base := acceptanceServer(t, "")
			seen := make(chan protocol.Request, 8)
			reply := func(req protocol.Request) protocol.Response {
				seen <- req
				if req.ID == "evicted-paused" {
					return protocol.Response{OK: true, Result: map[string]any{"paused": true, "completed": 1, "total": 2, "results": []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true}}, "_baseline": map[string]any{"marker": "original-attempt-basis"}, "_readback": []any{map[string]any{"action": "schematic.components.list", "result": map[string]any{"components": []any{map[string]any{"primitiveId": "completed"}}}}}}}
				}
				return protocol.Response{OK: true, Result: map[string]any{"paused": false}}
			}
			connector, _ := acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", reply)
			// A read-only suffix keeps generation unchanged, so consumption must block replay.
			steps := []any{map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "completed"}}, map[string]any{"action": "schematic.components.list", "payload": map[string]any{"primitiveId": "remaining"}}}
			acceptanceWantOK(t, acceptanceAction(t, s, "evicted-paused", "owner", "debug.batch", map[string]any{"steps": steps, "_edit": map[string]any{"primitiveIds": []any{"completed", "remaining"}}}))
			<-seen
			s.runtime.mu.Lock()
			key := s.runtime.current["owner"]
			err := s.runtime.reclaimReceipt("evicted-paused")
			_, requestRetained := s.runtime.requests["evicted-paused"]
			_, basisRetained := s.runtime.baselines["evicted-paused"]
			documentRetained := s.runtime.documents[key] != nil
			s.runtime.mu.Unlock()
			if err != nil || requestRetained || basisRetained || !documentRetained {
				t.Fatal("fixture did not evict only the paused receipt and its private cache")
			}
			acceptanceWantOK(t, acceptanceAction(t, s, "same-scope-later-read", "owner", "document.current"))
			<-seen
			scope, err := s.runtime.recovery.LoadScope(key)
			if err != nil || scope == nil {
				t.Fatal("later scope activity did not persist")
			}
			for _, receipt := range scope.Receipts {
				if receipt.RequestID == "evicted-paused" {
					t.Fatal("latest scope package still contains the evicted receipt")
				}
			}
			if scope.Requests["evicted-paused"].ID != "" || scope.Baselines["evicted-paused"] != nil {
				t.Fatal("latest scope package still contains the evicted private basis")
			}
			switch mode {
			case "different-epoch":
				connector.CloseNow()
				for deadline := time.Now().Add(3 * time.Second); ; {
					if _, connected := s.hub.target("owner"); !connected {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("old synthetic transport did not disconnect")
					}
					time.Sleep(time.Millisecond)
				}
				_, _ = acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", reply)
			case "corrupt-attempt":
				store := s.runtime.recovery.(*recoveryStore)
				if _, err := store.db.Exec(`UPDATE recovery_attempts SET hash='synthetic-invalid-hash' WHERE request_id=?`, "evicted-paused"); err != nil {
					t.Fatal("could not corrupt the temporary attempt integrity fixture")
				}
			}
			if mode == "same-connection" {
				s.runtime.mu.Lock()
				for i := 0; i < 300; i++ {
					s.runtime.receipts = append(s.runtime.receipts, &runtimeReceipt{RequestID: fmt.Sprintf("diagnostic-%d", i), Historical: true})
				}
				s.runtime.mu.Unlock()
			}
			response := runtimeTestControl(s, `{"operation":"resume","window":"owner","requestId":"evicted-paused"}`)
			if mode != "same-connection" {
				if response.Code < 400 || len(seen) != 0 {
					t.Fatal("foreign-epoch or corrupt attempt authorized replay")
				}
				return
			}
			if response.Code != 200 || len(seen) != 2 {
				t.Fatalf("original attempt was not reloadable after later scope persistence: status=%d dispatches=%d", response.Code, len(seen))
			}
			<-seen
			continued := <-seen
			remaining := runtimeArray(continued.Payload["steps"])
			if continued.Action != "debug.batch" || len(remaining) != 1 || runtimeMap(remaining[0])["action"] != "schematic.components.list" || runtimeMap(runtimeMap(remaining[0])["payload"])["primitiveId"] != "remaining" || runtimeMap(continued.Payload["_resumeBaseline"])["marker"] != "original-attempt-basis" {
				t.Fatal("reloaded original attempt lost private basis or replayed the completed prefix")
			}
			s.runtime.mu.Lock()
			err = s.runtime.reclaimReceipt("evicted-paused")
			s.runtime.mu.Unlock()
			if err != nil {
				t.Fatal("could not evict the consumed parent receipt")
			}
			acceptanceWantOK(t, acceptanceAction(t, s, "activity-after-continuation", "owner", "document.current"))
			<-seen
			scope, err = s.runtime.recovery.LoadScope(key)
			if err != nil || scope == nil {
				t.Fatal("could not inspect the scope after consumed-parent eviction")
			}
			for _, receipt := range scope.Receipts {
				if receipt.RequestID == "evicted-paused" {
					t.Fatal("latest scope package still contains the consumed parent")
				}
			}
			replayed := runtimeTestControl(s, `{"operation":"resume","window":"owner","requestId":"evicted-paused"}`)
			if replayed.Code != 409 || len(seen) != 0 {
				t.Fatal("consumed original attempt regained replay permission after cache eviction")
			}
		})
	}
}

func TestDispatchAcceptanceMissingDatabaseFailsClosed(t *testing.T) {
	dir := t.TempDir()
	s, _ := acceptanceServer(t, dir)
	if err := s.runtime.closeRecovery(); err != nil {
		t.Fatal("close synthetic recovery")
	}
	// Move only this temporary database; its existing identity marker remains.
	if err := acceptanceRename(filepath.Join(dir, "recovery-0.sqlite")); err != nil {
		t.Fatal("hide synthetic database:", err)
	}
	restarted, base := acceptanceServer(t, dir)
	_, calls := acceptanceConnector(t, restarted, base, "new", "synthetic-project", "synthetic-document", nil)
	response := acceptanceAction(t, restarted, "missing-db-write", "new", "schematic.component.modify")
	if response.OK || response.Error == nil || response.Error.Code != "RECOVERY_STORE_FAILED" || calls.Load() != 0 {
		t.Fatal("missing recovery database allowed a dangerous dispatch")
	}
}

func TestDispatchAcceptanceConfirmedResumeOnlyDispatchesRemainingPrefix(t *testing.T) {
	s, base := acceptanceServer(t, "")
	seen := make(chan protocol.Request, 4)
	_, _ = acceptanceConnector(t, s, base, "owner", "synthetic-project", "synthetic-document", func(req protocol.Request) protocol.Response {
		seen <- req
		return protocol.Response{OK: true, Result: map[string]any{"paused": false}}
	})
	identity := &protocol.Context{ProjectUUID: "synthetic-project", DocumentUUID: "synthetic-document", DocumentType: "schematic"}
	s.runtime.mu.Lock()
	d := s.runtime.document("owner", identity)
	receipt := &runtimeReceipt{started: true, RequestID: "paused", WindowID: "owner", ProjectUUID: identity.ProjectUUID, DocumentUUID: identity.DocumentUUID, Generation: d.Generation, Status: "paused", Progress: map[string]any{"completed": 1, "results": []any{map[string]any{"ok": true}}}, ObservedFacts: map[string]any{"marker": "synthetic-observed"}}
	s.runtime.receipts = append(s.runtime.receipts, receipt)
	s.runtime.requests["paused"] = protocol.Request{Envelope: protocol.Envelope{ID: "paused", WindowID: "owner"}, Action: "debug.batch", Payload: map[string]any{"steps": []any{map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "completed"}}, map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "remaining"}}}, "_scope": []any{"completed", "remaining"}}}
	s.runtime.baselines["paused"] = map[string]any{"marker": "synthetic-baseline"}
	s.runtime.mu.Unlock()
	body := `{"operation":"resume","window":"owner","requestId":"paused"}`
	response := runtimeTestControl(s, body)
	if response.Code != 200 {
		t.Fatalf("confirmed continuation rejected: status=%d", response.Code)
	}
	if len(seen) != 2 {
		t.Fatal("continuation did not dispatch queue acknowledgment and one batch")
	}
	<-seen
	batch := <-seen
	steps := runtimeArray(batch.Payload["steps"])
	if batch.Action != "debug.batch" || len(steps) != 1 || runtimeMap(runtimeMap(steps[0])["payload"])["primitiveId"] != "remaining" {
		t.Fatal("confirmed prefix replayed")
	}
	if runtimeTestControl(s, body).Code != 409 || len(seen) != 0 {
		t.Fatal("consumed continuation replayed")
	}
}

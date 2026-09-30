package daemon

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

const replaceTaskBody = `{"operation":"task_replace","window":"w","target":{"projectUuid":"p","documentUuid":"d"},"expectedRevision":1,"task":{"goal":"new design","primitiveIds":["r1"],"remaining":["read affected scope"]}}`

func replacementFixture(t *testing.T, reply func(protocol.Request) protocol.Response) *Server {
	t.Helper()
	s := New(Options{AuditDir: t.TempDir(), PortStart: 60932})
	server := httptest.NewServer(s.routes(0))
	t.Cleanup(server.Close)
	base := strings.TrimPrefix(server.URL, "http://")
	connector := dialConnector(t, base, "w")
	t.Cleanup(func() { connector.CloseNow() })
	waitForWindow(t, base, "w")
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}
	native, _ := s.hub.target("w")
	native.applyResponseContext(*ctx, time.Now())
	s.runtime.mu.Lock()
	d := s.runtime.document("w", ctx)
	d.Task = &runtimeTask{Revision: 1, Goal: "old design"}
	s.runtime.mu.Unlock()
	callCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		for {
			var req protocol.Request
			if wsjson.Read(callCtx, connector, &req) != nil {
				return
			}
			if req.Type != protocol.TypeRequest {
				continue
			}
			response := reply(req)
			response.ID, response.Type = req.ID, protocol.TypeResponse
			if wsjson.Write(callCtx, connector, response) != nil {
				return
			}
		}
	}()
	return s
}

func replacementResponse(paused bool) protocol.Response {
	return protocol.Response{OK: true, Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d", DocumentType: "schematic"}, Result: map[string]any{
		"targetChecked": true, "paused": paused, "pending": 0, "inFlight": []string{},
	}}
}

func TestRuntimeTaskReplaceCancelsAllClientsAndKeepsPause(t *testing.T) {
	for _, initialPause := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "user-paused"}[initialPause], func(t *testing.T) {
			paused := initialPause
			var calls []protocol.Request
			s := replacementFixture(t, func(req protocol.Request) protocol.Response {
				calls = append(calls, req)
				if req.Payload["operation"] == "pause" {
					paused = true
				}
				return replacementResponse(paused)
			})
			response := runtimeTestControl(s, replaceTaskBody)
			var result map[string]any
			_ = json.Unmarshal(response.Body.Bytes(), &result)
			if response.Code != 200 || result["replaced"] != true || result["wasPaused"] != initialPause || result["paused"] != true || result["persisted"] != true || result["edaReads"] != nil {
				t.Fatalf("replacement result: %d %s", response.Code, response.Body.String())
			}
			if len(calls) != 4 {
				t.Fatalf("wrong control count: %v", calls)
			}
			for i, op := range []string{"status", "pause", "cancel", "status"} {
				if calls[i].Action != "debug.control" || calls[i].Payload["operation"] != op || calls[i].Payload["clientId"] != nil || calls[i].Payload["requestId"] != nil || runtimeMap(calls[i].Payload["_target"])["documentUuid"] != "d" {
					t.Fatalf("replacement narrowed old clients, lost target, or dispatched EDA write: %+v", calls[i])
				}
			}
			d := s.runtime.documents[s.runtime.current["w"]]
			if d.Task.Revision != 2 || d.Task.Goal != "new design" {
				t.Fatal("replacement not committed")
			}
			if again := runtimeTestControl(s, replaceTaskBody); again.Code != 409 || len(calls) != 4 {
				t.Fatal("stale replacement controlled the new task")
			}
		})
	}
}

func TestRuntimeTaskReplaceRejectsBeforeQueueChanges(t *testing.T) {
	s := New(Options{})
	d := s.runtime.document("w", &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"})
	d.Task = &runtimeTask{Revision: 1, Goal: "old"}
	for _, body := range []string{
		strings.Replace(replaceTaskBody, `"expectedRevision":1`, `"expectedRevision":0`, 1),
		strings.Replace(replaceTaskBody, `"documentUuid":"d"`, `"documentUuid":"else"`, 1),
		strings.Replace(replaceTaskBody, `"goal":"new design"`, `"goal":""`, 1),
	} {
		response := runtimeTestControl(s, body)
		if (response.Code != 400 && response.Code != 409) || len(s.runtime.receipts) != 0 || d.Task.Revision != 1 {
			t.Fatalf("invalid replacement touched queue/task: %d %s", response.Code, response.Body.String())
		}
	}
	update := strings.Replace(replaceTaskBody, "task_replace", "task_update", 1)
	if response := runtimeTestControl(s, update); response.Code != 200 || len(s.runtime.receipts) != 0 {
		t.Fatal("ordinary task progress update controlled the queue")
	}
}

func TestRuntimeTaskReplaceRequiresActualSettlementAndGuardedTarget(t *testing.T) {
	for _, failure := range []string{"legacy-target", "wrong-target", "missing-inflight", "in-flight", "pending", "partial-control", "changed-target", "late-pending"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			s := replacementFixture(t, func(req protocol.Request) protocol.Response {
				calls++
				response := replacementResponse(calls > 1)
				if failure == "legacy-target" {
					delete(response.Result, "targetChecked")
				}
				if failure == "wrong-target" || (failure == "changed-target" && calls == 3) {
					response.Context.DocumentUUID = "other-page"
				}
				if failure == "missing-inflight" {
					delete(response.Result, "inFlight")
				}
				if failure == "late-pending" && calls == 4 {
					response.Result["pending"] = 1
				}
				if calls == 3 {
					switch failure {
					case "in-flight":
						response.Result["inFlight"] = []string{"native-still-running"}
					case "pending":
						response.Result["pending"] = 1
					case "partial-control":
						response.Result["partial"] = true
					}
				}
				return response
			})
			response := runtimeTestControl(s, replaceTaskBody)
			if response.Code != 409 {
				t.Fatalf("uncertain queue replaced task: %d %s", response.Code, response.Body.String())
			}
			d := s.runtime.documents["w|p|d"]
			if d.Task.Revision != 1 || d.Task.Goal != "old design" {
				t.Fatal("uncertain queue changed task")
			}
			if (failure == "legacy-target" || failure == "wrong-target" || failure == "missing-inflight") && calls != 1 {
				t.Fatal("invalid status caused cancellation")
			}
		})
	}
}

func TestRuntimeTaskReplaceWaitsForDaemonWriteSettlement(t *testing.T) {
	calls := 0
	s := replacementFixture(t, func(req protocol.Request) protocol.Response {
		calls++
		return replacementResponse(calls > 1)
	})
	release, acquired := s.acquireExclusive("schematic-geometry-window", "w")
	if !acquired {
		t.Fatal("could not hold existing write slot")
	}
	defer release()
	response := runtimeTestControl(s, replaceTaskBody)
	if response.Code != 409 || calls != 3 || s.runtime.documents["w|p|d"].Task.Revision != 1 {
		t.Fatalf("control success overrode unsettled daemon write: %d %s", response.Code, response.Body.String())
	}
}

func TestRuntimeTaskReplaceSerializesTaskAndQueueControls(t *testing.T) {
	seen, release := make(chan struct{}), make(chan struct{})
	calls := 0
	s := replacementFixture(t, func(req protocol.Request) protocol.Response {
		calls++
		if calls == 1 {
			close(seen)
			<-release
		}
		return replacementResponse(calls > 1)
	})
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- runtimeTestControl(s, replaceTaskBody) }()
	select {
	case <-seen:
	case <-time.After(3 * time.Second):
		t.Fatal("replacement status did not dispatch")
	}
	for _, body := range []string{replaceTaskBody, strings.Replace(replaceTaskBody, "task_replace", "task_update", 1), `{"operation":"resume_queue","window":"w"}`} {
		if response := runtimeTestControl(s, body); response.Code != 409 {
			t.Fatalf("concurrent task/control entered replacement: %d", response.Code)
		}
	}
	close(release)
	if response := <-done; response.Code != 200 || calls != 4 {
		t.Fatalf("replacement blocked its own controls: %d %s", response.Code, response.Body.String())
	}
}

func TestRuntimeContinuationRevisionCheckedAtDispatch(t *testing.T) {
	s := newRuntimeStore()
	ctx := &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	d := s.document("w", ctx)
	d.Task = &runtimeTask{Revision: 2, Goal: "replacement"}
	oldRevision := uint64(1)
	req := protocol.Request{Envelope: protocol.Envelope{ID: "old-resume", WindowID: "w"}, Action: "debug.batch", Payload: map[string]any{}}
	s.prepare(&req, ctx, runtimeDispatchProof{TaskRevision: &oldRevision})
	if err := s.start(req, ctx); err == nil || len(s.receipts) != 0 {
		t.Fatal("old continuation dispatched after replacement")
	}
	s.prepare(&req, ctx) // A caller cannot forge private continuation evidence.
	if _, present := req.Payload["_taskRevision"]; present {
		t.Fatal("private revision accepted from caller payload")
	}
}

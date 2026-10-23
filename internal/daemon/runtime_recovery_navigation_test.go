package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func recoveryNavigationFixture(t *testing.T, alter func(protocol.Request, map[string]any), adjust ...func(protocol.Request, *protocol.Response)) (*Server, map[string]any, *[]protocol.Request) {
	t.Helper()
	s, input, calls := acceptanceFixtureWithReply(t, func(req protocol.Request, result map[string]any) {
		if req.Action == "schematic.pages.list" {
			page := map[string]any{"uuid": "next", "parentSchematicUuid": "sch"}
			result["pages"] = append(runtimeArray(result["pages"]), page)
			sch := runtimeMap(runtimeArray(result["schematics"])[0])
			sch["page"] = append(runtimeArray(sch["page"]), page)
		}
		if alter != nil {
			alter(req, result)
		}
	}, adjust...)
	input["operation"] = "recovery_open_document"
	input["target"] = map[string]any{"projectUuid": "p", "documentUuid": "next"}
	delete(input, "task")
	return s, input, calls
}

func navigationHTTP(t *testing.T, s *Server, input map[string]any) (int, map[string]any, string) {
	t.Helper()
	server := httptest.NewServer(s.routes(0))
	defer server.Close()
	raw, _ := json.Marshal(input)
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/runtime/control", bytes.NewReader(raw))
	req.Header.Set("X-Easyeda-Control", "1")
	req.Header.Set("X-Easyeda-Response", "public-v1")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	var result map[string]any
	_ = json.Unmarshal(body, &result)
	return response.StatusCode, result, string(body)
}

func navigationOpenCount(calls []protocol.Request) int {
	count := 0
	for _, req := range calls {
		if req.Action == "document.open" {
			count++
		}
	}
	return count
}

func TestRecoveryOpenDocumentPublicHTTPPreservesUnknown(t *testing.T) {
	for _, noTask := range []bool{false, true} {
		t.Run(map[bool]string{false: "task", true: "no-task"}[noTask], func(t *testing.T) {
			s, input, calls := recoveryNavigationFixture(t, nil)
			if noTask {
				s.runtime.documents["w|p|d"].Task = nil
				input["expectedRevision"] = 0
			}
			store := s.runtime.recovery
			before, hashBefore, _ := store.LoadAttemptEvidence("old-write")
			beforeRaw, _ := json.Marshal(before)
			scopeBefore, _ := store.LoadScope("old-window|p|d")
			scopeRaw, _ := json.Marshal(scopeBefore)
			sourceBefore, _ := json.Marshal(s.runtime.documents["w|p|d"].Task)
			status, result, body := navigationHTTP(t, s, input)
			if status != 200 || result["navigated"] != true || result["ready"] != true || result["accepted"] != false || result["requiresCurrentBaselineAcceptance"] != true || result["openRequestId"] == "" || result["readbackRequestId"] == "" || navigationOpenCount(*calls) != 1 {
				t.Fatalf("navigation: %d %s calls=%+v", status, body, *calls)
			}
			for _, req := range *calls {
				if req.Action != "document.open" && req.Action != "debug.control" && req.Action != "document.current" && req.Action != "project.current" && req.Action != "schematic.pages.list" && req.Action != "board.list" {
					t.Fatalf("unexpected write: %+v", req)
				}
				if req.Action == "debug.control" && req.Payload["operation"] != "status" {
					t.Fatal("navigation changed queue")
				}
				if req.Action == "document.open" && (req.Payload["uuid"] != "next" || runtimeMap(req.Payload["_target"])["documentUuid"] != "d") {
					t.Fatal("navigation lost exact source and destination")
				}
			}
			after, hashAfter, _ := store.LoadAttemptEvidence("old-write")
			afterRaw, _ := json.Marshal(after)
			scopeAfter, _ := store.LoadScope("old-window|p|d")
			afterScopeRaw, _ := json.Marshal(scopeAfter)
			sourceAfter, _ := json.Marshal(s.runtime.documents["w|p|d"].Task)
			if hashBefore != hashAfter || !bytes.Equal(beforeRaw, afterRaw) || !bytes.Equal(scopeRaw, afterScopeRaw) || !bytes.Equal(sourceBefore, sourceAfter) || s.runtime.documents["w|p|next"].Task != nil {
				t.Fatal("navigation altered old recovery evidence or granted a task")
			}
			for _, action := range []string{"document.open", "schematic.page.open", "schematic.save", "schematic.titleblock.modify", "debug.exec_js"} {
				beforeCalls := len(*calls)
				response := acceptanceAction(t, s, "old-write", "w", action, map[string]any{"uuid": "d", "schematicPageUuid": "d", "_target": map[string]any{"projectUuid": "p", "documentUuid": "next"}, "_recoveryNavigationProof": map[string]any{"requestId": "ordinary-" + action}, "recovery_open_document": true})
				if response.OK || response.Error == nil || response.Error.Code != "DUPLICATE_REQUEST" || len(*calls) != beforeCalls {
					t.Fatalf("old request ID replayed using %s: %+v", action, response)
				}
			}
		})
	}
}

func TestRecoveryOpenDocumentRejectsBeforeOpen(t *testing.T) {
	for _, failure := range []string{"project", "target", "same-target", "hash", "epoch", "generation", "revision", "terminal", "initial", "reason", "unaccepted", "paused", "pending", "inflight", "queue-changed", "scene", "current-change", "wrong-source", "scope-ref", "extra-unknown", "target-owner"} {
		t.Run(failure, func(t *testing.T) {
			active := false
			statuses := 0
			var server *Server
			s, input, calls := recoveryNavigationFixture(t, func(req protocol.Request, result map[string]any) {
				if !active {
					return
				}
				if req.Action == "debug.control" {
					statuses++
					switch failure {
					case "paused":
						result["paused"] = true
					case "pending":
						result["pending"] = 1
					case "inflight":
						result["inFlight"] = []any{"live"}
					case "queue-changed":
						if statuses == 2 {
							result["pending"] = 1
						}
					}
				}
				if req.Action == "board.list" {
					if failure == "scene" {
						runtimeMap(result["_scene"])["sequence"] = 1
					}
					if failure == "current-change" {
						server.runtime.mu.Lock()
						server.runtime.documents["w|p|d"].Generation++
						server.runtime.mu.Unlock()
					}
				}
			}, func(req protocol.Request, response *protocol.Response) {
				if active && failure == "wrong-source" && req.Action == "document.current" {
					response.Context.DocumentUUID = "other"
				}
			})
			server, active = s, true
			acceptance := runtimeMap(input["acceptance"])
			switch failure {
			case "project":
				runtimeMap(input["target"])["projectUuid"] = "other"
			case "target":
				runtimeMap(input["target"])["documentUuid"] = "foreign"
			case "same-target":
				runtimeMap(input["target"])["documentUuid"] = "d"
			case "hash":
				acceptance["expectedBaselineSha256"] = strings.Repeat("0", 64)
			case "epoch":
				acceptance["connectionEpoch"] = "old-epoch"
			case "generation":
				input["generation"] = 99999
			case "revision":
				input["expectedRevision"] = 99999
			case "terminal":
				runtimeMap(runtimeArray(runtimeMap(acceptance["nativeEndEvidence"])["terminal"])[0])["sha256"] = strings.Repeat("0", 64)
			case "initial":
				runtimeMap(runtimeArray(runtimeMap(acceptance["nativeEndEvidence"])["terminal"])[0])["initialSha256"] = strings.Repeat("0", 64)
			case "reason":
				acceptance["reason"] = ""
			case "unaccepted":
				acceptance["acceptCurrentState"] = false
			case "scope-ref":
				runtimeMap(runtimeArray(acceptance["unsavedScopes"])[0])["revision"] = 99999
			case "extra-unknown":
				attempt := &recoveryAttempt{RequestID: "uncovered", WindowID: "another-host", ScopeKey: "another-host|p|next", Context: protocol.Context{ProjectUUID: "p", DocumentUUID: "next"}, State: recoveryAttemptUnknown, Dangerous: true, ContentWrite: true}
				if err := s.runtime.recovery.SaveAttempt(attempt); err != nil {
					t.Fatal(err)
				}
			case "target-owner":
				scope := &recoveryScope{Key: "another-host|p|next", Document: &runtimeDocument{WindowID: "another-host", Context: protocol.Context{ProjectUUID: "p", DocumentUUID: "next"}, ConnectionEpoch: "another-epoch", ChangesSinceSave: true}}
				if err := s.runtime.recovery.SaveScope(scope); err != nil {
					t.Fatal(err)
				}
			}
			status, _, body := navigationHTTP(t, s, input)
			if status < 400 || navigationOpenCount(*calls) != 0 {
				t.Fatalf("%s dispatched navigation: %d %s calls=%+v", failure, status, body, *calls)
			}
		})
	}
}

func TestRecoveryOpenDocumentPrivateProofIsExact(t *testing.T) {
	for _, failure := range []string{"request", "window", "action", "destination", "source", "epoch", "generation", "revision", "stale-attempt"} {
		t.Run(failure, func(t *testing.T) {
			s, input, _ := recoveryNavigationFixture(t, nil)
			raw, _ := json.Marshal(input["acceptance"])
			var acceptance runtimeAcceptanceInput
			_ = json.Unmarshal(raw, &acceptance)
			d := s.runtime.documents["w|p|d"]
			proof := &runtimeNavigationProof{RequestID: "proof-open", Window: "w", Epoch: d.ConnectionEpoch, Source: d.Context, Destination: "next", Generation: d.Generation, Revision: 1, Attempts: acceptance.Attempts, UnsavedScopes: acceptance.UnsavedScopes}
			req := protocol.Request{Envelope: protocol.Envelope{ID: "proof-open", WindowID: "w"}, Action: "document.open", Payload: map[string]any{"uuid": "next", "_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}}}
			switch failure {
			case "request":
				req.ID = "different"
			case "window":
				req.WindowID = "different"
			case "action":
				req.Action = "schematic.save"
			case "destination":
				req.Payload["uuid"] = "different"
			case "source":
				runtimeMap(req.Payload["_target"])["documentUuid"] = "different"
			case "epoch":
				proof.Epoch = "different"
			case "generation":
				proof.Generation++
			case "revision":
				proof.Revision++
			case "stale-attempt":
				proof.Attempts[0].SHA256 = strings.Repeat("0", 64)
			}
			if err := s.runtime.start(req, &d.Context, proof); err == nil {
				t.Fatalf("%s widened navigation proof", failure)
			}
		})
	}
}

func TestRecoveryOpenDocumentNoRetryOnTransportTimeout(t *testing.T) {
	calls := 0
	_, err, retried := forwardWithAdaptiveRetry(context.Background(), protocol.Request{Action: "document.open"}, func(context.Context, protocol.Request) (*protocol.Response, error) {
		calls++
		return nil, context.DeadlineExceeded
	}, adaptiveHooks{noRetry: true})
	if err != context.DeadlineExceeded || retried || calls != 1 {
		t.Fatal("recovery navigation timeout was retried")
	}
}

func TestRecoveryOpenDocumentUnconfirmedIsNotSuccessOrRetried(t *testing.T) {
	for _, failure := range []string{"not-ready", "readback-changed", "native-error", "partial", "wrong-document", "wrong-project"} {
		t.Run(failure, func(t *testing.T) {
			opened := false
			s, input, calls := recoveryNavigationFixture(t, func(req protocol.Request, result map[string]any) {
				if req.Action == "document.open" {
					opened = true
					if failure == "not-ready" {
						result["ready"] = false
					}
				}
				if opened && failure == "readback-changed" && req.Action == "document.current" {
					runtimeMap(result["_scene"])["changedDuringAction"] = true
				}
			}, func(req protocol.Request, response *protocol.Response) {
				if req.Action != "document.open" {
					return
				}
				switch failure {
				case "native-error":
					response.OK = false
					response.Error = &protocol.ErrorInfo{Code: "EDA_ERROR", Message: "synthetic native failure"}
				case "partial":
					response.Result["partial"] = true
				case "wrong-document":
					response.Context.DocumentUUID = "other"
				case "wrong-project":
					response.Context.ProjectUUID = "other"
				}
			})
			status, result, body := navigationHTTP(t, s, input)
			if status != 409 || result["navigated"] != false || navigationOpenCount(*calls) != 1 {
				t.Fatalf("unconfirmed navigation accepted/retried: %d %s", status, body)
			}
		})
	}
}

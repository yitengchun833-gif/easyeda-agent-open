package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Internal queue controls share the normal dispatch/audit path, but already
// own the task slot. Never expose this bypass as a request payload flag.
type runtimeReplaceControlKey struct{}

func (s *Server) replaceRuntimeTask(w http.ResponseWriter, r *http.Request, window string, target *protocol.Context, expected *uint64, task *runtimeTask) {
	if invalid := validateRuntimeTask(target, expected, task); invalid != "" {
		http.Error(w, invalid, http.StatusBadRequest)
		return
	}
	release, acquired := s.acquireExclusive("runtime-task-window", window)
	if !acquired {
		http.Error(w, "Task or queue control is changing; inspect current state before replacing", http.StatusConflict)
		return
	}
	defer release()
	s.runtime.mu.Lock()
	_, err := s.runtime.taskTarget(window, target, *expected)
	s.runtime.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return // A stale or foreign task must never cancel a newer task.
	}

	var wasPaused any
	var queue map[string]any
	fail := func(message string) {
		writeJSON(w, http.StatusConflict, map[string]any{
			"ok": false, "replaced": false, "taskIsIntent": true, "wasPaused": wasPaused,
			"queue": queue, "executionState": "unknown", "error": message,
			"nextAction": "Read runtime state and target queue status. Read back uncertain edits before retrying task_replace; no task was updated and no queue resume was sent.",
		})
	}
	control := func(operation string) bool {
		var controlErr error
		queue, controlErr = s.replacementQueueControl(r, window, target, operation)
		if controlErr != nil {
			fail(controlErr.Error())
			return false
		}
		return true
	}
	// Probe the guarded-control capability without changing a legacy queue.
	// Older Connectors ignore _target on debug.control and must stop here.
	if !control("status") {
		return
	}
	wasPaused = queue["paused"]
	if !control("pause") {
		return
	}
	if queue["paused"] != true {
		fail("Queue pause was not confirmed; no cancellation or task update sent")
		return
	}
	// No clientId/requestId: cancel all old queued work, including other MCP
	// sessions. Cancellation acknowledges a request, not native settlement.
	if !control("cancel") {
		return
	}
	if !replacementQueueIdle(queue) {
		fail("Old native work is still in flight or pending; task unchanged and queue left paused")
		return
	}
	// Reuse the existing window write slot so no new write/document switch can
	// enter between the final idle observation and the task CAS. An old HTTP
	// call that has not settled also prevents commitment even if cancel replied.
	writeRelease, idle := s.acquireExclusive("schematic-geometry-window", window)
	if !idle {
		fail("A prior write or document transition has not settled; task unchanged and queue left paused")
		return
	}
	defer writeRelease()
	if !control("status") {
		return
	}
	if !replacementQueueIdle(queue) {
		fail("Queue changed before replacement; task unchanged and queue left paused")
		return
	}
	s.commitRuntimeTask(w, window, target, expected, task, map[string]any{
		"replaced": true, "wasPaused": wasPaused, "paused": true, "pending": 0, "inFlight": []string{},
		"requiresScopedReadback": true, "contentWrites": 0, "queueControls": 4,
		"nextAction": "Read runtime state and the affected EDA scope, submit only remaining work under the new task, and explicitly resume_queue when drawing is authorized. The new plan has not executed.",
	})
}

func replacementQueueIdle(queue map[string]any) bool {
	return queue["paused"] == true && runtimeNumber(queue["pending"]) == 0 && len(runtimeArray(queue["inFlight"])) == 0
}

func (s *Server) replacementQueueControl(r *http.Request, window string, target *protocol.Context, operation string) (map[string]any, error) {
	probe := &runtimeResponseWriter{header: http.Header{}}
	internal := r.Clone(context.WithValue(r.Context(), runtimeReplaceControlKey{}, true))
	s.runtimeDispatch(probe, internal, protocol.Request{
		Envelope: protocol.Envelope{ID: s.nextRequestID(), WindowID: window}, Action: "debug.control", TimeoutMs: 5000,
		Payload: map[string]any{"operation": operation, "_target": map[string]any{"projectUuid": target.ProjectUUID, "documentUuid": target.DocumentUUID}},
	})
	var response protocol.Response
	if probe.status >= 400 || json.Unmarshal(probe.body.Bytes(), &response) != nil || !response.OK || response.Result["partial"] == true || response.Result["ok"] == false {
		return nil, fmt.Errorf("Target queue %s was not confirmed; do not infer cancellation from a control response", operation)
	}
	result := response.Result
	if result["targetChecked"] != true || response.Context == nil || response.Context.ProjectUUID != target.ProjectUUID || response.Context.DocumentUUID != target.DocumentUUID {
		return nil, fmt.Errorf("Target-checked queue control unavailable; use a matching Connector and read the intended page first")
	}
	paused, pausedOK := result["paused"].(bool)
	pending, pendingOK := numberField(result["pending"])
	inFlight, inFlightOK := result["inFlight"].([]any)
	if !pausedOK || !pendingOK || pending < 0 || pending != float64(int64(pending)) || !inFlightOK {
		return nil, fmt.Errorf("Queue %s lacks explicit pause/pending/inFlight evidence", operation)
	}
	return map[string]any{"paused": paused, "pending": pending, "inFlight": inFlight}, nil
}

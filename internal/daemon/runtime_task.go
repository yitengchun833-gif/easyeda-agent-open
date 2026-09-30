package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Restore only this branch's task receipts from the existing audit. Never
// restore fresh facts, verification passes, or permission to replay a write.
func (s *Server) restoreRuntimeReceipts() {
	if s.audit == nil || s.audit.disabled {
		return
	}
	file, err := os.Open(s.audit.Path(time.Now()))
	if err != nil {
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return
	}
	start := info.Size() - 4*1024*1024
	if start < 0 {
		start = 0
	}
	_, _ = file.Seek(start, io.SeekStart)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 65536), 4*1024*1024)
	if start > 0 {
		scanner.Scan()
	} // skip the partial leading JSONL row
	for scanner.Scan() {
		var entry auditEntry
		if json.Unmarshal(scanner.Bytes(), &entry) != nil {
			continue
		}
		if runtimeMap(entry.Result["rules"])["version"] != "open-local-1" {
			continue
		}
		original := entry.Result
		entry.OK = false
		entry.Result = map[string]any{"completed": original["completed"], "total": original["total"], "results": original["results"]}
		s.runtime.observe(entry)
	}
}

// Apply both full ring stamps on responses and one-change event deltas. A
// missing sequence or changed connector instance invalidates every old fact.
func (d *runtimeDocument) applyScene(stamp map[string]any) bool {
	instance, seq := runtimeString(stamp["instance"]), runtimeNumber(stamp["sequence"])
	if instance == "" || (instance == d.SceneInstance && seq <= d.SceneSequence) {
		return false
	}
	if d.SceneInstance == "" && seq == 0 && len(d.Objects) == 0 {
		d.SceneInstance, d.SceneSequence, d.EventCoverage = instance, seq, "beta-unverified"
		return false // initial empty event stamp is not an edit
	}
	reset := d.SceneInstance != instance || d.SceneSequence < runtimeNumber(stamp["oldestSequence"])-1 || (stamp["delta"] == true && seq != d.SceneSequence+1)
	changed := map[string]bool{}
	for _, item := range runtimeArray(stamp["changes"]) {
		event := runtimeMap(item)
		if !reset && runtimeNumber(event["sequence"]) <= d.SceneSequence {
			continue
		}
		for _, id := range runtimeArray(event["primitiveIds"]) {
			if value := runtimeString(id); value != "" {
				changed[value] = true
			}
		}
	}
	d.Generation++
	for key, object := range d.Objects {
		object.StaleFields["pins"] = true
		if reset || len(changed) == 0 || changed[key] || changed[runtimeString(object.Data["primitiveId"])] || changed[runtimeString(object.Data["parentId"])] {
			object.StaleFields["identity"] = true
			object.StaleFields["geometry"] = true
		}
	}
	d.Coverage = map[string]bool{}
	d.SceneInstance, d.SceneSequence, d.EventCoverage = instance, seq, "beta-unverified"
	return true
}
func (s *runtimeStore) connectorEvent(window string, data map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if data["kind"] == "progress" {
		for _, receipt := range s.receipts {
			if receipt.WindowID == window && receipt.RequestID == runtimeString(data["requestId"]) && (receipt.Status == "queued" || receipt.Status == "running") {
				receipt.Progress, receipt.Status = runtimeClone(data), "running"
			}
		}
		s.emit("progress", data)
		return
	}
	if data["kind"] != "scene_changed" {
		return
	}
	stamp, changed := runtimeMap(data["stamp"]), false
	for _, d := range s.documents {
		// Native events have no reliable page owner. Keep window pages separate,
		// but invalidate their evidence until a scoped read restores it.
		if d.WindowID == window && d.applyScene(stamp) {
			changed = true
		}
	}
	if changed {
		s.emit("scene_changed", map[string]any{"windowId": window, "sequence": stamp["sequence"], "coverage": "beta-unverified"})
	}
}
func (s *runtimeStore) connectionChanged(window, kind string, ctx *protocol.Context) {
	if window == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.current[window]
	var current *runtimeDocument
	if ctx != nil {
		if ctx.ProjectUUID == "" || ctx.DocumentUUID == "" {
			delete(s.current, window)
		} else {
			current = s.document(window, ctx)
		}
	}
	if kind == "context" && previous == s.current[window] {
		return
	}
	for key, d := range s.documents {
		if d.WindowID != window {
			continue
		}
		if kind == "context" && key != previous && d != current {
			continue
		}
		d.Generation++
		d.Coverage = map[string]bool{}
		for _, object := range d.Objects {
			object.StaleFields["identity"], object.StaleFields["geometry"], object.StaleFields["pins"] = true, true, true
		}
	}
	data := map[string]any{"windowId": window}
	if ctx != nil {
		data["context"] = *ctx
	}
	s.emit(kind, data)
}

func runtimeControlAllowed(r *http.Request) bool {
	if r.Method != http.MethodPost || r.Header.Get("X-Easyeda-Control") != "1" {
		return false
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host {
			return false
		}
	}
	return true
}

type runtimeBaselineKey struct{}
type runtimeDispatchProof struct {
	Baseline         any
	Observed         any
	ParentID         string
	FinishGeneration *uint64
	TaskRevision     *uint64
}

func (s *Server) runtimeDispatch(w http.ResponseWriter, r *http.Request, req protocol.Request, baseline ...any) {
	if len(baseline) > 0 {
		if proof, ok := baseline[0].(runtimeDispatchProof); ok && proof.ParentID != "" {
			if req.Payload == nil {
				req.Payload = map[string]any{}
			}
			req.Payload["_taskParent"] = proof.ParentID
		}
	}
	data, err := json.Marshal(req)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	dispatchContext := r.Context()
	if len(baseline) != 0 && baseline[0] != nil {
		dispatchContext = context.WithValue(dispatchContext, runtimeBaselineKey{}, baseline[0])
	}
	forward := r.Clone(dispatchContext)
	forward.Body = http.NoBody
	forward.Body = io.NopCloser(bytes.NewReader(data))
	s.handleAction(w, forward)
}

// All controls use the same Connector queue; unknown writes are never replayed.
func (s *Server) handleRuntimeControl(w http.ResponseWriter, r *http.Request) {
	if !runtimeControlAllowed(r) {
		http.Error(w, "POST with X-Easyeda-Control: 1 and same origin required", 403)
		return
	}
	var input struct {
		SourceWindow        string            `json:"sourceWindow"`
		Target              *protocol.Context `json:"target"`
		ExpectedRevision    *uint64           `json:"expectedRevision"`
		Task                *runtimeTask      `json:"task"`
		Operation           string            `json:"operation"`
		Window              string            `json:"window"`
		RequestID           string            `json:"requestId"`
		Generation          uint64            `json:"generation"`
		ScreenshotRequestID string            `json:"screenshotRequestId"`
		Source              string            `json:"source"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&input); err != nil || input.Window == "" {
		http.Error(w, "Explicit window and valid control body required", 400)
		return
	}
	if input.Operation == "pause" || input.Operation == "resume_queue" || input.Operation == "cancel" {
		op := input.Operation
		if op == "resume_queue" {
			op = "resume"
		}
		payload := map[string]any{"operation": op}
		if input.RequestID != "" {
			payload["requestId"] = input.RequestID
		}
		s.runtimeDispatch(w, r, protocol.Request{Envelope: protocol.Envelope{ID: s.nextRequestID(), WindowID: input.Window}, Action: "debug.control", TimeoutMs: 5000, Payload: payload})
		return
	}
	if input.Operation == "task_update" {
		s.updateRuntimeTask(w, input.Window, input.Target, input.ExpectedRevision, input.Task)
		return
	}
	if input.Operation == "task_replace" {
		s.replaceRuntimeTask(w, r, input.Window, input.Target, input.ExpectedRevision, input.Task)
		return
	}
	if input.Operation == "restore" {
		s.restoreRuntimeTask(w, input.Window, input.SourceWindow, input.Target, input.Generation)
		return
	}
	s.runtime.mu.Lock()
	d := s.runtime.documents[s.runtime.current[input.Window]]
	var receipt *runtimeReceipt
	for _, item := range s.runtime.receipts {
		if item.RequestID == input.RequestID && item.WindowID == input.Window {
			receipt = item
		}
	}
	if d == nil || receipt == nil || receipt.Historical || receipt.DocumentUUID != d.Context.DocumentUUID || receipt.ProjectUUID != d.Context.ProjectUUID {
		s.runtime.mu.Unlock()
		http.Error(w, "Task/document observation unavailable; read the target first", 409)
		return
	}
	if d.Task != nil && receipt.TaskRevision != d.Task.Revision && (input.Operation == "resume" || input.Operation == "retry" || input.Operation == "finish") {
		s.runtime.mu.Unlock()
		http.Error(w, "Task intent changed; read local state and submit the remaining work under the current revision", 409)
		return
	}
	if input.Operation == "visual_review" {
		if input.Source != "ai" && input.Source != "engineer" {
			s.runtime.mu.Unlock()
			http.Error(w, "Visual source must be ai or engineer", 400)
			return
		}
		var capture *runtimeReceipt
		for _, item := range s.runtime.receipts {
			if item.RequestID == input.ScreenshotRequestID {
				capture = item
			}
		}
		if input.Generation != d.Generation || capture == nil || capture.Historical || capture.WindowID != input.Window || capture.Generation != d.Generation || capture.DocumentUUID != d.Context.DocumentUUID || capture.ProjectUUID != d.Context.ProjectUUID || !capture.coversImage(runtimeMap(receipt.Checks)["scope"]) {
			s.runtime.mu.Unlock()
			http.Error(w, "A current image receipt covering this task scope is required", 409)
			return
		}
		checks := runtimeMap(receipt.Checks)
		if checks == nil {
			checks = map[string]any{}
		}
		checks["visualReview"] = map[string]any{"generation": d.Generation, "screenshotRequestId": capture.RequestID, "source": input.Source, "at": time.Now().UTC()}
		receipt.Checks = checks
		d.Checks = map[string]any{"generation": receipt.Generation, "documentUuid": receipt.DocumentUUID, "checkedAt": time.Now().UTC(), "result": runtimeClone(checks)}
		s.runtime.emit("visual_review", map[string]any{"requestId": receipt.RequestID, "windowId": input.Window, "source": input.Source})
		s.runtime.mu.Unlock()
		writeJSON(w, 200, map[string]any{"ok": true, "generation": input.Generation})
		return
	}
	if input.Operation == "finish" {
		checks := runtimeMap(receipt.Checks)
		visual := runtimeMap(checks["visualReview"])
		if s.runtime.pendingWrite(input.Window, "") || receipt.Status != "action_finished" || checks["status"] != "pass" || receipt.ObservedFacts == nil || receipt.Generation != d.Generation || visual["generation"] == nil || runtimeNumber(visual["generation"]) != int64(d.Generation) {
			s.runtime.mu.Unlock()
			http.Error(w, "Required checks/visual review are missing, failed or stale; no save executed", 409)
			return
		}
		for _, required := range receipt.RequiredChecks {
			verified := false
			if required == "drc" {
				for _, evidence := range s.runtime.receipts {
					if !evidence.Historical && evidence.WindowID == input.Window && evidence.ProjectUUID == d.Context.ProjectUUID && evidence.DocumentUUID == d.Context.DocumentUUID && evidence.Generation == d.Generation && evidence.Status == "action_finished" && evidence.DRCPassed {
						verified = true
					}
				}
			}
			if !verified {
				s.runtime.mu.Unlock()
				http.Error(w, "A declared required check is missing, failed or stale; no save executed", 409)
				return
			}
		}
		receipt.Status = "saving"
		s.runtime.emit("task_saving", map[string]any{"requestId": receipt.RequestID, "windowId": input.Window})
		ctx := d.Context
		expectedGeneration := receipt.Generation
		observed := runtimeClone(receipt.ObservedFacts)
		scope := runtimeClone(checks["scope"])
		s.runtime.mu.Unlock()
		s.runtimeDispatch(w, r, protocol.Request{Envelope: protocol.Envelope{ID: s.nextRequestID(), WindowID: input.Window}, Action: saveActionForDocType(ctx.DocumentType), Payload: map[string]any{"_target": map[string]any{"projectUuid": ctx.ProjectUUID, "documentUuid": ctx.DocumentUUID}, "_edit": map[string]any{"primitiveIds": scope}}}, runtimeDispatchProof{ParentID: input.RequestID, FinishGeneration: &expectedGeneration, Observed: observed, TaskRevision: &receipt.TaskRevision})
		return
	}
	if input.Operation != "resume" && input.Operation != "retry" {
		s.runtime.mu.Unlock()
		http.Error(w, "Unknown control operation", 400)
		return
	}
	saved, found := s.runtime.requests[input.RequestID]
	progress := runtimeMap(receipt.Progress)
	results := runtimeArray(progress["results"])
	if !found || receipt.Status != "paused" || len(results) != int(runtimeNumber(progress["completed"])) {
		s.runtime.mu.Unlock()
		http.Error(w, "Only a confirmed paused batch can resume directly. Unknown/failed writes require scoped readback and a repaired plan; no replay sent", 409)
		return
	}
	if receipt.ObservedFacts == nil || s.runtime.baselines[input.RequestID] == nil {
		s.runtime.mu.Unlock()
		http.Error(w, "Paused scope observation unavailable; read back and submit remaining work. No continuation sent", 409)
		return
	}
	for _, item := range results {
		if runtimeMap(item)["ok"] != true {
			s.runtime.mu.Unlock()
			http.Error(w, "A completed step is uncertain; read it back before resuming", 409)
			return
		}
	}
	steps := runtimeArray(saved.Payload["steps"])
	completed := len(results)
	if completed >= len(steps) {
		s.runtime.mu.Unlock()
		http.Error(w, "No unexecuted steps remain", 409)
		return
	}
	if saved.WindowID != input.Window || receipt.DocumentUUID != d.Context.DocumentUUID || receipt.ProjectUUID != d.Context.ProjectUUID || receipt.Generation != d.Generation {
		s.runtime.mu.Unlock()
		http.Error(w, "Paused task target changed or its evidence is stale; read back before continuing", 409)
		return
	}
	// Claim before releasing the lock: another resume cannot replay these steps.
	receipt.Status = "resuming"
	s.runtime.emit("task_resuming", map[string]any{"requestId": receipt.RequestID, "windowId": input.Window})
	saved.Payload = runtimeMap(runtimeClone(saved.Payload))
	saved.ID = s.nextRequestID()
	saved.Payload["steps"] = steps[completed:]
	edit := runtimeMap(saved.Payload["_edit"])
	if edit == nil {
		edit = map[string]any{}
	}
	baseline := runtimeClone(s.runtime.baselines[input.RequestID])
	observed := runtimeClone(receipt.ObservedFacts)
	edit["primitiveIds"] = saved.Payload["_scope"]
	saved.Payload["_edit"] = edit
	saved.Payload["_target"] = map[string]any{"projectUuid": d.Context.ProjectUUID, "documentUuid": d.Context.DocumentUUID}
	saved.Payload["_taskParent"] = input.RequestID
	s.runtime.mu.Unlock()
	// Resume permission is explicit. The old instruction stamp remains intact;
	// superseded tasks still fail in the Connector rather than becoming new work.
	clear := protocol.Request{Envelope: protocol.Envelope{ID: s.nextRequestID(), WindowID: input.Window}, Action: "debug.control", TimeoutMs: 5000, Payload: map[string]any{"operation": "resume"}}
	probe := &runtimeResponseWriter{header: http.Header{}}
	s.runtimeDispatch(probe, r, clear)
	var acknowledgement protocol.Response
	if probe.status >= 400 || json.Unmarshal(probe.body.Bytes(), &acknowledgement) != nil || !acknowledgement.OK || acknowledgement.Result["paused"] == true {
		s.runtime.mu.Lock()
		if receipt.Status == "resuming" {
			receipt.Status = "paused"
		}
		s.runtime.mu.Unlock()
		http.Error(w, "Queue resume was not acknowledged; no continuation sent", 409)
		return
	}
	s.runtime.mu.Lock()
	current := s.runtime.documents[s.runtime.current[input.Window]]
	if current != d || current.Generation != receipt.Generation || receipt.Status != "resuming" || (current.Task != nil && current.Task.Revision != receipt.TaskRevision) {
		receipt.Status = "needs_readback"
		s.runtime.mu.Unlock()
		http.Error(w, "Task changed while resuming; no continuation sent", 409)
		return
	}
	// The original stays consumed even when transport outcome is unknown.
	// A new child receipt records the continuation and any further pause.
	receipt.Status = "continued"
	receipt.Progress = map[string]any{"continuationRequestId": saved.ID, "completed": completed, "total": len(steps)}
	s.runtime.mu.Unlock()
	s.runtimeDispatch(w, r, saved, runtimeDispatchProof{Baseline: baseline, Observed: observed, ParentID: input.RequestID, TaskRevision: &receipt.TaskRevision})
}

type runtimeResponseWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (w *runtimeResponseWriter) Header() http.Header  { return w.header }
func (w *runtimeResponseWriter) WriteHeader(code int) { w.status = code }
func (w *runtimeResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(data)
}

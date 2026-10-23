package daemon

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

type runtimeTerminalEvidence struct {
	RequestID              string `json:"requestId"`
	StatusRequestID        string `json:"statusRequestId"`
	SHA256                 string `json:"sha256"`
	InitialStatusRequestID string `json:"initialStatusRequestId"`
	InitialSHA256          string `json:"initialSha256"`
}

type runtimeAcceptanceInput struct {
	AcceptCurrentState     bool                         `json:"acceptCurrentState"`
	RequestIDs             []string                     `json:"requestIds"`
	ConnectionEpoch        string                       `json:"connectionEpoch"`
	ExpectedBaselineSHA256 string                       `json:"expectedBaselineSha256"`
	Reason                 string                       `json:"reason"`
	Attempts               []recoveryDispositionAttempt `json:"attempts"`
	UnsavedScopes          []recoveryDispositionScope   `json:"unsavedScopes"`
	NativeEndEvidence      struct {
		Type       string                    `json:"type"`
		AttestedBy string                    `json:"attestedBy"`
		OldWindow  string                    `json:"oldWindow"`
		Statement  string                    `json:"statement"`
		Terminal   []runtimeTerminalEvidence `json:"terminal"`
	} `json:"nativeEndEvidence"`
}

// Manual acceptance starts new, page-scoped intent. It never settles an old
// native result or permits raw/project operations under a fresh request ID.
func runtimeDispositionRequestAllowed(req protocol.Request, ctx protocol.Context) bool {
	target := runtimeMap(req.Payload["_target"])
	if target["projectUuid"] != ctx.ProjectUUID || target["documentUuid"] != ctx.DocumentUUID || runtimeProjectWide(req) {
		return false
	}
	if req.Action == "document.close" {
		return req.Payload["uuid"] == ctx.DocumentUUID && ctx.TabID != "" && req.Payload["tabId"] == ctx.TabID
	}
	if req.Action == "document.open" {
		return runtimeString(req.Payload["uuid"]) != "" && req.Payload["uuid"] != ctx.DocumentUUID
	}
	if req.Action == "debug.batch" {
		steps := runtimeArray(req.Payload["steps"])
		if len(steps) == 0 {
			return false
		}
		for _, value := range steps {
			step := runtimeMap(value)
			if step["action"] == "document.open" || step["action"] == "document.close" {
				return false // Saved close/reopen evidence belongs to separate top-level requests.
			}
			payload := runtimeMap(runtimeClone(step["payload"]))
			if payload == nil {
				payload = map[string]any{}
			}
			if nested := runtimeMap(payload["_target"]); nested != nil && (nested["projectUuid"] != ctx.ProjectUUID || nested["documentUuid"] != ctx.DocumentUUID) {
				return false
			}
			payload["_target"] = target
			if !runtimeDispositionRequestAllowed(protocol.Request{Action: runtimeString(step["action"]), Payload: payload}, ctx) {
				return false
			}
		}
		return true
	}
	if !knownActions[req.Action] || !strings.HasPrefix(req.Action, ctx.DocumentType+".") {
		return false
	}
	switch req.Action {
	case "schematic.page.create", "schematic.page.rename", "schematic.page.delete", "schematic.page.open", "schematic.rename":
		return false
	}
	return true
}

func (s *runtimeStore) currentDispositionLocked(req protocol.Request, ctx protocol.Context) (*recoveryDisposition, error) {
	d := s.documents[runtimeScopeKey(req.WindowID, ctx)]
	if req.Action == "document.open" {
		key := req.WindowID + "|" + ctx.ProjectUUID + "|" + runtimeString(req.Payload["uuid"])
		d = s.documents[key]
	}
	if d == nil || d.Task == nil || d.Restored || !runtimeDispositionRequestAllowed(req, ctx) {
		return nil, nil
	}
	if req.Action == "document.close" || req.Action == "document.open" {
		if d.ChangesSinceSave || d.SavedAt == nil {
			return nil, nil
		}
		if req.Action == "document.open" {
			closed := false
			for _, receipt := range s.receipts {
				if receipt.Action == "document.close" && receipt.Status == "action_finished" && !receipt.Historical && receipt.ConnectionEpoch == s.connectionEpoch[req.WindowID] && receipt.WindowID == req.WindowID && receipt.TaskRevision == d.Task.Revision && receipt.ClosedDocumentUUID == d.Context.DocumentUUID && receipt.StartedContext != nil && receipt.StartedContext.ProjectUUID == d.Context.ProjectUUID && receipt.StartedContext.DocumentUUID == d.Context.DocumentUUID && !receipt.StartedAt.Before(*d.SavedAt) {
					closed = true
				}
			}
			if !closed {
				return nil, nil
			}
		}
	}
	disposition, err := s.recovery.LoadDisposition(runtimeScopeKey(d.WindowID, d.Context), d.Task.ID, d.Task.Revision)
	if err != nil || disposition == nil {
		return nil, err
	}
	if disposition.WindowID != req.WindowID || disposition.Context.ProjectUUID != d.Context.ProjectUUID || disposition.Context.DocumentUUID != d.Context.DocumentUUID || disposition.ConnectionEpoch == "" || disposition.ConnectionEpoch != s.connectionEpoch[req.WindowID] || disposition.ConnectionEpoch != d.ConnectionEpoch || d.Generation < disposition.Generation || disposition.CurrentSceneInstance == "" || disposition.CurrentSceneInstance != d.SceneInstance {
		return nil, nil
	}
	return disposition, nil
}

func dispositionCoversAttempt(disposition *recoveryDisposition, attempt recoveryAttempt) bool {
	if disposition == nil || attempt.State != recoveryAttemptUnknown {
		return false
	}
	for _, ref := range disposition.Attempts {
		if ref.RequestID == attempt.RequestID && ref.Revision == attempt.Revision && ref.ScopeKey == attempt.ScopeKey {
			return true
		}
	}
	return false
}

func dispositionCoversScope(disposition *recoveryDisposition, scope recoveryIndexEntry) bool {
	if disposition != nil {
		for _, ref := range disposition.UnsavedScopes {
			if ref.Key == scope.Key && ref.Revision == scope.Revision && ref.ConnectionEpoch == scope.ConnectionEpoch {
				return true
			}
		}
	}
	return false
}

type runtimeAcceptanceEvidence struct {
	Sources  []recoveryDispositionEvidence `json:"sources"`
	Handlers []any                         `json:"handlers"`
}

func runtimeNativeStatusEntry(status auditEntry, requestID string) map[string]any {
	if !status.OK || status.WindowID == "" || status.Action != "debug.control" || status.Payload["operation"] != "status" || status.Payload["requestId"] != requestID {
		return nil
	}
	var found map[string]any
	for _, value := range runtimeArray(status.Result["requests"]) {
		entry := runtimeMap(value)
		if entry["id"] == requestID {
			if found != nil {
				return nil
			}
			found = entry
		}
	}
	return found
}

func (s *Server) acceptanceEvidence(input *runtimeAcceptanceInput, window string) (*runtimeAcceptanceEvidence, error) {
	proof := input.NativeEndEvidence
	if proof.Type != "manual_session_termination_attestation" || proof.AttestedBy != "user" || proof.OldWindow == "" || proof.OldWindow == window || strings.TrimSpace(proof.Statement) == "" || len(proof.Statement) > 4000 || len(proof.Terminal) != len(input.Attempts) {
		return nil, fmt.Errorf("Explicit user manual_session_termination_attestation and one same-store handler terminal proof per unknown request are required; queue idle/disconnect/elapsed time are not native termination proof")
	}
	if _, live := s.hub.target(proof.OldWindow); live {
		return nil, fmt.Errorf("Attested old host window is still connected; no acceptance committed")
	}
	refs := []recoveryDispositionEvidence{}
	handlers := []any{}
	sourceIDs := map[string]bool{}
	seen := map[string]bool{}
	for _, terminal := range proof.Terminal {
		if seen[terminal.RequestID] {
			return nil, fmt.Errorf("Duplicate terminal evidence")
		}
		seen[terminal.RequestID] = true
		var attemptRef *recoveryDispositionAttempt
		for i := range input.Attempts {
			if input.Attempts[i].RequestID == terminal.RequestID {
				attemptRef = &input.Attempts[i]
			}
		}
		if attemptRef == nil {
			return nil, fmt.Errorf("Terminal proof does not reference an accepted unknown request")
		}
		attempt, digest, err := s.runtime.recovery.LoadAttemptEvidence(terminal.RequestID)
		if err != nil || attempt == nil || attempt.State != recoveryAttemptUnknown || attempt.Revision != attemptRef.Revision || digest != attemptRef.SHA256 || attempt.ScopeKey != attemptRef.ScopeKey || attempt.WindowID != attemptRef.WindowID || attempt.WindowID != proof.OldWindow || (attempt.ConnectionEpoch != "" && attempt.ConnectionEpoch == input.ConnectionEpoch) {
			return nil, fmt.Errorf("Unknown request evidence changed or belongs to a different host")
		}
		raw, err := s.runtime.recovery.LoadLegacyAudit(terminal.StatusRequestID, terminal.SHA256)
		var status auditEntry
		if err != nil || json.Unmarshal(raw, &status) != nil {
			return nil, fmt.Errorf("Same-store targeted native handler status evidence is unavailable or mismatched")
		}
		initialRaw, err := s.runtime.recovery.LoadLegacyAudit(terminal.InitialStatusRequestID, terminal.InitialSHA256)
		var initial auditEntry
		if err != nil || json.Unmarshal(initialRaw, &initial) != nil || initial.RequestID == status.RequestID || status.Timestamp.Before(initial.Timestamp) {
			return nil, fmt.Errorf("Independent same-store initial handler status evidence is required")
		}
		initialEntry := runtimeNativeStatusEntry(initial, terminal.RequestID)
		entry := runtimeNativeStatusEntry(status, terminal.RequestID)
		execution := runtimeMap(attempt.Request.Payload["_execution"])
		clientID := runtimeString(execution["clientId"])
		revision, revisionOK := numberField(execution["revision"])
		state := runtimeString(entry["state"])
		if entry == nil || initialEntry == nil || initialEntry["started"] != true || initialEntry["settled"] != false || runtimeNumber(initialEntry["arrivedAt"]) != runtimeNumber(entry["arrivedAt"]) || initialEntry["clientId"] != clientID || runtimeNumber(initialEntry["revision"]) != int64(revision) || entry["started"] != true || entry["settled"] != true || (state != "completed" && state != "failed" && state != "settled_after_cancel") || runtimeNumber(entry["arrivedAt"]) <= 0 || runtimeNumber(entry["finishedAt"]) < runtimeNumber(entry["arrivedAt"]) || clientID == "" || runtimeString(entry["clientId"]) != clientID || !revisionOK || runtimeNumber(entry["revision"]) != int64(revision) {
			return nil, fmt.Errorf("Status does not prove the exact old handler reached a terminal state")
		}
		handlers = append(handlers, map[string]any{"handlerWindow": attempt.WindowID, "initialCollectorWindow": initial.WindowID, "terminalCollectorWindow": status.WindowID, "initial": initialEntry, "terminal": entry})
		for _, ref := range []recoveryDispositionEvidence{{Kind: "legacy-audit", RequestID: terminal.InitialStatusRequestID, SHA256: terminal.InitialSHA256}, {Kind: "legacy-audit", RequestID: terminal.StatusRequestID, SHA256: terminal.SHA256}} {
			if !sourceIDs[ref.RequestID] {
				refs = append(refs, ref)
				sourceIDs[ref.RequestID] = true
			}
		}
	}
	return &runtimeAcceptanceEvidence{Sources: refs, Handlers: handlers}, nil
}

func (s *Server) baselineRead(r *http.Request, window string, target *protocol.Context, action string) (map[string]any, error) {
	r = r.Clone(r.Context())
	r.Header = r.Header.Clone()
	// Internal baseline checks need scene evidence before public transport projection.
	r.Header.Del("X-Easyeda-Response")
	probe := &runtimeResponseWriter{header: http.Header{}}
	s.runtimeDispatch(probe, r, protocol.Request{Envelope: protocol.Envelope{ID: s.nextRequestID(), WindowID: window}, Action: action, TimeoutMs: 10000, Payload: map[string]any{"_target": map[string]any{"projectUuid": target.ProjectUUID, "documentUuid": target.DocumentUUID}}})
	var response protocol.Response
	if probe.status >= 400 || json.Unmarshal(probe.body.Bytes(), &response) != nil || !response.OK || response.Result["partial"] == true || response.Result["ok"] == false || response.Context == nil || response.Context.ProjectUUID != target.ProjectUUID || response.Context.DocumentUUID != target.DocumentUUID || runtimeMap(response.Result["_scene"])["changedDuringAction"] == true {
		return nil, fmt.Errorf("Fresh target-checked %s read failed; no current-state acceptance committed", action)
	}
	return map[string]any{"requestId": response.ID, "context": response.Context, "result": response.Result}, nil
}

func runtimeBaselineStructure(basis map[string]any, target *protocol.Context) bool {
	project := runtimeMap(runtimeMap(basis["project.current"])["result"])
	pages := runtimeMap(runtimeMap(basis["schematic.pages.list"])["result"])
	boards := runtimeMap(runtimeMap(basis["board.list"])["result"])
	if project["uuid"] != target.ProjectUUID {
		return false
	}
	schematics, schematicsOK := pages["schematics"].([]any)
	pageList, pagesOK := pages["pages"].([]any)
	boardList, boardsOK := boards["boards"].([]any)
	if !schematicsOK || !pagesOK || !boardsOK || runtimeNumber(boards["count"]) != int64(len(boardList)) {
		return false
	}
	pageParents := map[string]string{}
	for _, value := range schematics {
		sch := runtimeMap(value)
		if runtimeString(sch["uuid"]) == "" || sch["parentProjectUuid"] != target.ProjectUUID {
			return false
		}
		children, ok := sch["page"].([]any)
		if !ok {
			return false
		}
		for _, child := range children {
			page := runtimeMap(child)
			id := runtimeString(page["uuid"])
			if id == "" || page["parentSchematicUuid"] != sch["uuid"] || pageParents[id] != "" {
				return false
			}
			pageParents[id] = runtimeString(sch["uuid"])
		}
	}
	seen := map[string]bool{}
	for _, value := range pageList {
		page := runtimeMap(value)
		id := runtimeString(page["uuid"])
		if id == "" || seen[id] || pageParents[id] == "" || pageParents[id] != page["parentSchematicUuid"] {
			return false
		}
		seen[id] = true
	}
	if len(seen) != len(pageParents) {
		return false
	}
	found := seen[target.DocumentUUID]
	for _, value := range boardList {
		board := runtimeMap(value)
		if runtimeString(board["name"]) == "" || board["parentProjectUuid"] != target.ProjectUUID {
			return false
		}
		if target.DocumentType == "pcb" && board["pcbUuid"] == target.DocumentUUID {
			found = true
		}
	}
	return found
}

func runtimeBaselineCanonical(basis map[string]any) map[string]any {
	project := runtimeMap(runtimeMap(basis["project.current"])["result"])
	pages := runtimeMap(runtimeMap(basis["schematic.pages.list"])["result"])
	boards := runtimeMap(runtimeMap(basis["board.list"])["result"])
	projectIdentity := map[string]any{}
	for _, key := range []string{"uuid", "name", "friendlyName", "teamUuid"} {
		projectIdentity[key] = project[key]
	}
	projectRows := func(values []any, fields []string, sortField string) []any {
		rows := []any{}
		for _, value := range values {
			row := map[string]any{}
			for _, key := range fields {
				row[key] = runtimeMap(value)[key]
			}
			rows = append(rows, row)
		}
		sort.Slice(rows, func(i, j int) bool {
			return runtimeString(runtimeMap(rows[i])[sortField]) < runtimeString(runtimeMap(rows[j])[sortField])
		})
		return rows
	}
	return map[string]any{"project": projectIdentity,
		"schematics": projectRows(runtimeArray(pages["schematics"]), []string{"uuid", "name", "parentProjectUuid"}, "uuid"),
		"pages":      projectRows(runtimeArray(pages["pages"]), []string{"uuid", "name", "parentSchematicUuid"}, "uuid"),
		"boards":     projectRows(runtimeArray(boards["boards"]), []string{"name", "schematicUuid", "schematicName", "pcbUuid", "pcbName", "parentProjectUuid"}, "name")}
}

func (s *Server) readAcceptanceBaseline(r *http.Request, window string, target *protocol.Context) (map[string]any, map[string]any, string, error) {
	basis := map[string]any{}
	instance, sequence := "", int64(0)
	for _, action := range []string{"project.current", "schematic.pages.list", "board.list"} {
		observation, err := s.baselineRead(r, window, target, action)
		if err != nil {
			return nil, nil, "", err
		}
		scene := runtimeMap(runtimeMap(observation["result"])["_scene"])
		if runtimeString(scene["instance"]) == "" || (instance != "" && (scene["instance"] != instance || runtimeNumber(scene["sequence"]) != sequence)) {
			return nil, nil, "", fmt.Errorf("Native scene changed across the project structure read")
		}
		instance, sequence = runtimeString(scene["instance"]), runtimeNumber(scene["sequence"])
		basis[action] = observation
	}
	if !runtimeBaselineStructure(basis, target) {
		return nil, nil, "", fmt.Errorf("Native project/page/board structure is incomplete or target membership differs")
	}
	canonical := runtimeBaselineCanonical(basis)
	raw, err := json.Marshal(canonical)
	if err != nil {
		return nil, nil, "", err
	}
	return runtimeMap(runtimeClone(basis)), canonical, recoveryHash(raw), nil
}

func (s *Server) acceptCurrentRuntimeBaseline(w http.ResponseWriter, r *http.Request, window string, target *protocol.Context, expected *uint64, generation uint64, task *runtimeTask, input *runtimeAcceptanceInput) {
	if input == nil || s.runtime.recovery == nil || s.runtime.recoveryError != nil {
		http.Error(w, "Healthy durable recovery store and explicit acceptance input are required", http.StatusConflict)
		return
	}
	if !input.AcceptCurrentState {
		if target == nil || target.ProjectUUID == "" || target.DocumentUUID == "" || len(input.RequestIDs) == 0 || len(input.RequestIDs) > 16 {
			http.Error(w, "Preview requires explicit target and 1..16 exact requestIds", http.StatusBadRequest)
			return
		}
		release, acquired := s.acquireExclusive("runtime-task-window", window)
		if !acquired {
			http.Error(w, "Task or queue control is changing", http.StatusConflict)
			return
		}
		defer release()
		writeRelease, acquired := s.acquireExclusive("schematic-geometry-window", window)
		if !acquired {
			http.Error(w, "A write or document transition has not settled", http.StatusConflict)
			return
		}
		defer writeRelease()
		queue, err := s.replacementQueueControl(r, window, target, "status")
		if err != nil || queue["paused"] != false || runtimeNumber(queue["pending"]) != 0 || len(runtimeArray(queue["inFlight"])) != 0 {
			http.Error(w, "Queue must be idle and explicitly resumed before preview reads", http.StatusConflict)
			return
		}
		s.runtime.mu.Lock()
		d := s.runtime.documents[s.runtime.current[window]]
		if d == nil || d.Context.ProjectUUID != target.ProjectUUID || d.Context.DocumentUUID != target.DocumentUUID {
			s.runtime.mu.Unlock()
			http.Error(w, "Read the current target identity first", http.StatusConflict)
			return
		}
		ctx := d.Context
		epoch := s.runtime.connectionEpoch[window]
		s.runtime.mu.Unlock()
		_, canonical, baselineHash, err := s.readAcceptanceBaseline(r, window, &ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		refs := []recoveryDispositionAttempt{}
		for _, id := range input.RequestIDs {
			attempt, digest, err := s.runtime.recovery.LoadAttemptEvidence(id)
			if err != nil || attempt == nil {
				http.Error(w, "Requested recovery evidence unavailable", http.StatusConflict)
				return
			}
			refs = append(refs, recoveryDispositionAttempt{RequestID: id, Revision: attempt.Revision, SHA256: digest, ScopeKey: attempt.ScopeKey, WindowID: attempt.WindowID})
		}
		s.runtime.mu.Lock()
		d = s.runtime.documents[s.runtime.current[window]]
		if d == nil || d.Context.ProjectUUID != target.ProjectUUID || d.Context.DocumentUUID != target.DocumentUUID || epoch == "" || epoch != s.runtime.connectionEpoch[window] {
			s.runtime.mu.Unlock()
			http.Error(w, "Preview connection or target changed", http.StatusConflict)
			return
		}
		var current any
		if d != nil {
			current = map[string]any{"target": d.Context, "task": d.Task, "generation": d.Generation, "connectionEpoch": s.runtime.connectionEpoch[window]}
		}
		s.runtime.mu.Unlock()
		index, err := s.runtime.recovery.ListIndex()
		if err != nil {
			http.Error(w, "Recovery scope index unavailable", http.StatusConflict)
			return
		}
		unsaved := []recoveryDispositionScope{}
		for _, scope := range index {
			if target != nil && scope.Context.ProjectUUID == target.ProjectUUID && scope.Context.DocumentUUID == target.DocumentUUID && scope.ChangesSinceSave && scope.WindowID != window {
				unsaved = append(unsaved, recoveryDispositionScope{Key: scope.Key, Revision: scope.Revision, ConnectionEpoch: scope.ConnectionEpoch})
			}
		}
		writeJSON(w, 200, map[string]any{"ok": true, "preview": true, "current": current, "attempts": refs, "unsavedScopes": unsaved, "baseline": canonical, "baselineSha256": baselineHash, "edaReads": 3, "automaticReplay": false, "accepted": false})
		return
	}
	if invalid := validateRuntimeTask(target, expected, task); invalid != "" || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 4000 || len(input.Attempts) == 0 || len(input.Attempts) > 16 || len(input.UnsavedScopes) > 16 || input.ConnectionEpoch == "" || len(input.ExpectedBaselineSHA256) != 64 {
		http.Error(w, "Acceptance requires bounded task/reason, exact old attempts, current epoch/generation and revision", http.StatusBadRequest)
		return
	}
	release, acquired := s.acquireExclusive("runtime-task-window", window)
	if !acquired {
		http.Error(w, "Task or queue control is changing", http.StatusConflict)
		return
	}
	defer release()
	writeRelease, acquired := s.acquireExclusive("schematic-geometry-window", window)
	if !acquired {
		http.Error(w, "A write or document transition has not settled", http.StatusConflict)
		return
	}
	defer writeRelease()
	fail := func(err error) { http.Error(w, err.Error(), http.StatusConflict) }
	connection, live := s.hub.target(window)
	if !live || connection.epoch != input.ConnectionEpoch {
		fail(fmt.Errorf("Current connection epoch changed"))
		return
	}
	s.runtime.mu.Lock()
	d, err := s.runtime.taskTarget(window, target, *expected)
	valid := err == nil && d.Generation == generation && d.ConnectionEpoch == input.ConnectionEpoch
	s.runtime.mu.Unlock()
	if !valid {
		fail(fmt.Errorf("Current target/task/generation changed; read it before accepting"))
		return
	}
	evidence, err := s.acceptanceEvidence(input, window)
	if err != nil {
		fail(err)
		return
	}
	queue, err := s.replacementQueueControl(r, window, target, "status")
	if err != nil || runtimeNumber(queue["pending"]) != 0 || len(runtimeArray(queue["inFlight"])) != 0 || queue["paused"] != false {
		fail(fmt.Errorf("Current queue must be idle and explicitly resumed before native baseline reads; no resume or acceptance sent"))
		return
	}
	basis, canonical, baselineHash, err := s.readAcceptanceBaseline(r, window, &d.Context)
	if err != nil {
		fail(err)
		return
	}
	if baselineHash != input.ExpectedBaselineSHA256 {
		fail(fmt.Errorf("Native baseline differs from the reviewed structure hash; preview and review it again"))
		return
	}
	for _, operation := range []string{"pause", "status"} {
		queue, err = s.replacementQueueControl(r, window, target, operation)
		if err != nil || !replacementQueueIdle(queue) {
			fail(fmt.Errorf("Current queue safety changed; no acceptance committed and no queue resume sent"))
			return
		}
	}
	s.runtime.mu.Lock()
	defer s.runtime.mu.Unlock()
	current, err := s.runtime.taskTarget(window, target, *expected)
	active, connected := s.hub.target(window)
	if err != nil || current != d || !connected || active != connection || current.Generation != generation || s.runtime.connectionEpoch[window] != input.ConnectionEpoch || current.SceneInstance == "" || s.runtime.pendingWrite(window, "") {
		fail(fmt.Errorf("Current task/connection/observation changed during acceptance; queue remains paused"))
		return
	}
	previous := d.Task
	task.ID, task.Revision, task.UpdatedAt = rand.Text(), *expected+1, time.Now().UTC()
	d.Task = task
	scope := s.runtime.scopeLocked(d)
	disposition := &recoveryDisposition{ScopeKey: scope.Key, ScopeRevision: scope.Revision, WindowID: window, Context: d.Context, ConnectionEpoch: input.ConnectionEpoch, Generation: generation, CurrentSceneInstance: d.SceneInstance, TaskID: task.ID, TaskRevision: task.Revision, Basis: basis, Reason: input.Reason, Attempts: input.Attempts, UnsavedScopes: input.UnsavedScopes, SourceEvidence: evidence.Sources, Evidence: map[string]any{"attestation": input.NativeEndEvidence, "handlers": evidence.Handlers, "reviewedBaseline": canonical, "reviewedBaselineSha256": baselineHash}}
	if err := s.runtime.recovery.SaveDisposition(scope, disposition); err != nil {
		d.Task = previous
		s.runtime.failRecovery(err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "accepted": false, "persisted": false, "automaticReplay": false, "error": err.Error()})
		return
	}
	s.runtime.scopeVersions[scope.Key] = scope.Revision
	s.runtime.emit("task_baseline_accepted", map[string]any{"windowId": window, "taskId": task.ID, "revision": task.Revision})
	if r.Header.Get("X-Easyeda-Response") == "public-v1" {
		public := *disposition
		public.Basis = publicActionValue(disposition.Basis)
		public.Evidence = publicActionValue(disposition.Evidence)
		disposition = &public
	}
	writeJSON(w, 200, map[string]any{"ok": true, "accepted": true, "persisted": true, "task": task, "disposition": disposition, "paused": true, "contentWrites": 0, "edaReads": 3, "automaticReplay": false, "oldResultsRemainUnknown": true, "terminationEvidenceType": "human-attestation-with-same-store-handler-terminal-proof", "nextAction": "Review the fresh baseline and explicitly resume_queue before a new typed page-scoped write. Old request IDs and uncertain resumes remain forbidden."})
}

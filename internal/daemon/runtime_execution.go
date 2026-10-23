package daemon

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

var (
	errRuntimePersistence  = errors.New("recovery evidence could not be committed")
	errRuntimeScopeBusy    = errors.New("a conflicting project write is still executing")
	errRuntimeScopeUnknown = errors.New("related write or unsaved owner requires scoped reconciliation")
)

func runtimeScopeKey(window string, ctx protocol.Context) string {
	return window + "|" + ctx.ProjectUUID + "|" + ctx.DocumentUUID
}

// Called with runtime.mu held. The scope is a recoverable cache entry, not a queue.
func (s *runtimeStore) saveScopeLocked(d *runtimeDocument) error {
	if s.recovery == nil || d == nil {
		return nil
	}
	scope := s.scopeLocked(d)
	if err := s.recovery.SaveScope(scope); err != nil {
		return err
	}
	s.scopeVersions[scope.Key] = scope.Revision
	return nil
}

func (s *runtimeStore) scopeLocked(d *runtimeDocument) *recoveryScope {
	key := runtimeScopeKey(d.WindowID, d.Context)
	scope := &recoveryScope{Key: key, Revision: s.scopeVersions[key], Document: d, Requests: map[string]protocol.Request{}, Baselines: map[string]any{}}
	selected := map[string]bool{}
	for _, r := range s.receipts {
		if r.WindowID == d.WindowID && r.ProjectUUID == d.Context.ProjectUUID && r.DocumentUUID == d.Context.DocumentUUID {
			selected[r.RequestID] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, r := range s.receipts {
			if !selected[r.RequestID] {
				continue
			}
			for _, id := range []string{r.ParentRequestID, runtimeString(runtimeMap(r.Progress)["continuationRequestId"]), runtimeString(runtimeMap(runtimeMap(r.Checks)["visualReview"])["screenshotRequestId"])} {
				if id != "" && !selected[id] {
					selected[id], changed = true, true
				}
			}
		}
	}
	for _, r := range s.receipts {
		if selected[r.RequestID] {
			scope.Receipts = append(scope.Receipts, r)
			if request, ok := s.requests[r.RequestID]; ok {
				scope.Requests[r.RequestID] = request
			}
			if baseline, ok := s.baselines[r.RequestID]; ok {
				scope.Baselines[r.RequestID] = baseline
			}
		}
	}
	return scope
}

func (s *runtimeStore) lazyLoadScopeLocked(key string) (*runtimeDocument, error) {
	if d := s.documents[key]; d != nil || s.recovery == nil {
		return d, nil
	}
	scope, err := s.recovery.LoadScope(key)
	if err != nil || scope == nil || scope.Document == nil {
		return nil, err
	}
	if len(s.documents) >= 8 {
		candidate := s.reclaimableDocument(s.protectedReceipts())
		if candidate == "" {
			return nil, errRuntimeCapacity
		}
		s.reclaimDocument(candidate)
	}
	d := scope.Document
	s.scopeVersions[key] = scope.Revision
	sameEpoch := d.ConnectionEpoch != "" && d.ConnectionEpoch == s.connectionEpoch[d.WindowID]
	d.Restored = !sameEpoch
	if d.Objects == nil {
		d.Objects = map[string]*runtimeObject{}
	}
	d.Coverage = map[string]bool{}
	if !sameEpoch {
		d.Generation++
		d.Checks, d.EventCoverage = nil, "unknown"
	}
	for _, object := range d.Objects {
		object.StaleFields = map[string]bool{"identity": true, "geometry": true, "pins": true}
	}
	s.documents[key] = d
	for _, receipt := range scope.Receipts {
		found := false
		for _, current := range s.receipts {
			if current.RequestID == receipt.RequestID {
				found = true
				break
			}
		}
		if found {
			continue
		}
		if !sameEpoch {
			receipt.Historical = true
			receipt.Checks, receipt.ObservedFacts = nil, nil
			receipt.ImageAvailable, receipt.DRCPassed = false, false
			if receipt.Status == "queued" || receipt.Status == "running" || receipt.Status == "resuming" || receipt.Status == "saving" {
				receipt.Status = "needs_readback"
			}
		}
		s.receipts = append(s.receipts, receipt)
	}
	if sameEpoch {
		for id, request := range scope.Requests {
			if len(s.requests) < 16 {
				s.requests[id] = request
				s.baselines[id] = scope.Baselines[id]
			}
		}
	}
	return d, nil
}

func runtimeDangerous(req protocol.Request) bool {
	if runtimeDryRun(req.Action, req.Payload) || capacityReadOnly(req) {
		return false
	}
	if req.Action == "debug.control" {
		return false // queue control cannot establish that an old write ended
	}
	if req.Action == "debug.batch" {
		for _, value := range runtimeArray(req.Payload["steps"]) {
			step := runtimeMap(value)
			if runtimeDangerous(protocol.Request{Action: runtimeString(step["action"]), Payload: runtimeMap(step["payload"])}) {
				return true
			}
		}
		return len(runtimeArray(req.Payload["steps"])) == 0
	}
	return mutatesAction[req.Action] || !knownActions[req.Action] || req.Action == "document.open" || req.Action == "document.close" || req.Action == "schematic.page.open" || req.Action == "system.page_reload"
}

func runtimeDryRun(action string, payload map[string]any) bool {
	if payload["dryRun"] != true {
		return false
	}
	// Only these adapters return before every native write. Caller flags on
	// raw scripts or other typed actions cannot downgrade an execution intent.
	switch action {
	case "debug.batch", "schematic.page.clear", "schematic.wire.from_pin", "pcb.page.clear", "pcb.drc.rules.set", "pcb.config.set", "pcb.net.color.set", "pcb.beautify":
		return true
	}
	return false
}

func runtimeProjectWide(req protocol.Request) bool {
	if req.Action == "debug.exec_js" || strings.HasPrefix(req.Action, "project.") {
		return true
	}
	if req.Action == "debug.batch" {
		for _, step := range runtimeArray(req.Payload["steps"]) {
			item := runtimeMap(step)
			if runtimeProjectWide(protocol.Request{Action: runtimeString(item["action"]), Payload: runtimeMap(item["payload"])}) {
				return true
			}
		}
	}
	return false
}

func (s *runtimeStore) checkExecutionScopeLocked(req protocol.Request, ctx *protocol.Context, navigation ...*runtimeNavigationProof) error {
	if !runtimeDangerous(req) {
		return nil
	}
	context := protocol.Context{}
	if ctx != nil {
		context = *ctx
	}
	if runtimeFreshRequest(req) && (len(navigation) == 0 || navigation[0] == nil) {
		for _, attempt := range s.attempts {
			if (attempt.Dangerous || attempt.ContentWrite) && attempt.State != recoveryAttemptSettled && attempt.State != recoveryAttemptNotDispatched &&
				runtimeConflictingAttempt(req, context, *attempt) {
				return fmt.Errorf("%w: request %s", errRuntimeScopeBusy, attempt.RequestID)
			}
		}
		return nil // New work does not inherit historical task or unsaved-owner locks.
	}
	var proof *runtimeNavigationProof
	if len(navigation) == 1 && navigation[0].matches(req, context) && navigation[0].Epoch == s.connectionEpoch[req.WindowID] {
		proof = navigation[0]
		d := s.documents[s.current[req.WindowID]]
		revision := uint64(0)
		if d != nil && d.Task != nil {
			revision = d.Task.Revision
		}
		if d == nil || d.Context.ProjectUUID != context.ProjectUUID || d.Context.DocumentUUID != context.DocumentUUID || d.Generation != proof.Generation || revision != proof.Revision {
			return fmt.Errorf("recovery navigation source changed before dispatch")
		}
	}
	unresolved, err := s.recovery.ListUnresolved()
	if err != nil {
		return fmt.Errorf("%w: %v", errRuntimePersistence, err)
	}
	all := map[string]recoveryAttempt{}
	for _, attempt := range unresolved {
		all[attempt.RequestID] = attempt
	}
	for id, attempt := range s.attempts {
		if attempt.State != recoveryAttemptSettled && attempt.State != recoveryAttemptNotDispatched {
			all[id] = *attempt
		}
	}
	disposition, err := s.currentDispositionLocked(req, context)
	if err != nil {
		return fmt.Errorf("%w: %v", errRuntimePersistence, err)
	}
	if proof != nil {
		disposition = nil // Navigation admits only this request's reviewed evidence.
	}
	relatedDocument := func(document string) bool {
		return document == context.DocumentUUID || (proof != nil && document == proof.Destination) || (disposition != nil && req.Action == "document.open" && document == disposition.Context.DocumentUUID)
	}
	for _, attempt := range all {
		if !attempt.Dangerous && !attempt.ContentWrite {
			continue
		}
		live := s.attempts[attempt.RequestID]
		if live != nil && (live.State == recoveryAttemptPending || live.State == recoveryAttemptDispatched) {
			if attempt.WindowID == req.WindowID || attempt.Context.ProjectUUID == context.ProjectUUID {
				return fmt.Errorf("%w: request %s", errRuntimeScopeBusy, attempt.RequestID)
			}
		}
		if dispositionCoversAttempt(disposition, attempt) {
			continue
		}
		if proof != nil && proof.coversAttempt(s, attempt) {
			continue
		}
		if attempt.Context.ProjectUUID == "" && attempt.WindowID == req.WindowID {
			return fmt.Errorf("%w: request %s has no confirmed project identity", errRuntimeScopeUnknown, attempt.RequestID)
		}
		if attempt.Context.ProjectUUID != context.ProjectUUID || (context.ProjectUUID == "" && attempt.WindowID != req.WindowID) {
			continue
		}
		if live != nil && (live.State == recoveryAttemptPending || live.State == recoveryAttemptDispatched) {
			return fmt.Errorf("%w: request %s", errRuntimeScopeBusy, attempt.RequestID)
		}
		if attempt.ProjectWide || runtimeProjectWide(req) || attempt.Context.DocumentUUID == "" || relatedDocument(attempt.Context.DocumentUUID) {
			return fmt.Errorf("%w: request %s", errRuntimeScopeUnknown, attempt.RequestID)
		}
	}
	index, err := s.recovery.ListIndex()
	if err != nil {
		return fmt.Errorf("%w: %v", errRuntimePersistence, err)
	}
	for _, history := range index {
		if history.Context.ProjectUUID != context.ProjectUUID || !relatedDocument(history.Context.DocumentUUID) || !history.ChangesSinceSave {
			continue
		}
		if history.WindowID != req.WindowID || history.ConnectionEpoch == "" || history.ConnectionEpoch != s.connectionEpoch[req.WindowID] {
			if dispositionCoversScope(disposition, history) {
				continue
			}
			if proof != nil && dispositionCoversScope(&recoveryDisposition{UnsavedScopes: proof.UnsavedScopes}, history) {
				continue
			}
			return fmt.Errorf("%w: unsaved document belongs to window %s", errRuntimeScopeUnknown, history.WindowID)
		}
	}
	return nil
}

func (s *runtimeStore) persistIntentLocked(req protocol.Request, ctx *protocol.Context, receipt *runtimeReceipt, d *runtimeDocument) error {
	attempt := &recoveryAttempt{RequestID: req.ID, Request: req, Baseline: runtimeClone(req.Payload["_resumeBaseline"]), Receipt: receipt, State: recoveryAttemptPending, WindowID: req.WindowID, ConnectionEpoch: receipt.ConnectionEpoch, ContentWrite: runtimeContentWrite(req.Action, req.Payload), Dangerous: runtimeDangerous(req), ProjectWide: runtimeProjectWide(req), UpdatedAt: time.Now().UTC()}
	if ctx != nil {
		attempt.Context = *ctx
	}
	if d != nil {
		attempt.ScopeKey = runtimeScopeKey(d.WindowID, d.Context)
		attempt.TaskRevision = receipt.TaskRevision
		if d.Task != nil {
			attempt.TaskID = d.Task.ID
		}
	}
	if err := s.recovery.SaveAttempt(attempt); err != nil {
		return err
	}
	s.attempts[req.ID] = attempt
	if err := s.saveScopeLocked(d); err != nil {
		attempt.State, attempt.ExecutionKnown, attempt.Executed = recoveryAttemptNotDispatched, true, false
		_ = s.recovery.SaveAttempt(attempt)
		return err
	}
	return nil
}

func (s *runtimeStore) markDispatched(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recovery == nil {
		return nil
	}
	attempt := s.attempts[id]
	if attempt == nil {
		return nil // narrow audit-only recovery read
	}
	var parent *recoveryAttempt
	var paused *runtimeReceipt
	reject := func(err error) error {
		if parent != nil && paused != nil {
			parent.Receipt = paused
			_ = s.recovery.SaveAttempt(parent) // child is known unsent; failed rollback stays consumed
		}
		attempt.State, attempt.ExecutionKnown, attempt.Executed = recoveryAttemptNotDispatched, true, false
		if attempt.Receipt != nil {
			attempt.Receipt.started, attempt.Receipt.Status = false, "not_dispatched"
			for _, retained := range s.receipts {
				if retained.RequestID == attempt.Receipt.ParentRequestID && retained.Status == "continued" {
					retained.Status = "resuming"
				}
			}
		}
		_ = s.recovery.SaveAttempt(attempt)
		return err
	}
	if parentID := runtimeString(attempt.Request.Payload["_taskParent"]); parentID != "" && attempt.Request.Action == "debug.batch" {
		var err error
		parent, err = s.recovery.LoadAttempt(parentID)
		if err != nil || parent == nil || parent.Receipt == nil || parent.State != recoveryAttemptSettled || parent.Receipt.Status != "paused" {
			return reject(fmt.Errorf("continuation parent is unavailable or already consumed: %v", err))
		}
		original := *parent.Receipt
		paused = &original
		consumed := original
		consumed.Status = "continued"
		consumed.Progress = map[string]any{"continuationRequestId": id, "completed": runtimeMap(original.Progress)["completed"], "total": runtimeMap(original.Progress)["total"]}
		parent.Receipt = &consumed
		if err := s.recovery.SaveAttempt(parent); err != nil {
			return reject(fmt.Errorf("continuation consumption could not be committed: %w", err))
		}
	}
	attempt.State, attempt.UpdatedAt = recoveryAttemptDispatched, time.Now().UTC()
	if err := s.recovery.SaveAttempt(attempt); err != nil {
		return reject(err)
	}
	return nil
}

// Execution and durable evidence have separate outcomes. Persistence failure
// leaves an unresolved guard; it is never permission to dispatch again.
func (s *runtimeStore) settleExecution(req protocol.Request, entry auditEntry, known bool) error {
	s.observe(entry)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recovery == nil {
		return nil
	}
	attempt := s.attempts[req.ID]
	if attempt == nil {
		return nil
	}
	for _, receipt := range s.receipts {
		if receipt.RequestID == req.ID {
			attempt.Receipt = receipt
			break
		}
	}
	attempt.State, attempt.ExecutionKnown, attempt.Executed, attempt.UpdatedAt = recoveryAttemptSettled, known, known, time.Now().UTC()
	attempt.Evidence = entry
	attempt.ContinuationBaseline = runtimeClone(s.baselines[req.ID])
	if !known || (attempt.Dangerous && attempt.Receipt != nil && attempt.Receipt.Status == "needs_readback") {
		attempt.State = recoveryAttemptUnknown
	}
	d := s.documents[attempt.ScopeKey]
	if err := s.saveScopeLocked(d); err != nil {
		attempt.State = recoveryAttemptUnknown
		return err
	}
	if err := s.recovery.SaveAttempt(attempt); err != nil {
		attempt.State = recoveryAttemptUnknown
		return err
	}
	delete(s.attempts, req.ID) // durable unknowns belong to the lightweight ledger, not active memory
	return nil
}

// The persistent request log outlives the observation ring. Reload only the
// requested paused proof, and only while its original live authority matches.
func (s *runtimeStore) loadPausedAttemptLocked(d *runtimeDocument, id string) (*runtimeReceipt, error) {
	if s.recovery == nil || d == nil {
		return nil, nil
	}
	attempt, err := s.recovery.LoadAttempt(id)
	if err != nil || attempt == nil {
		return nil, err
	}
	r := attempt.Receipt
	epoch := s.connectionEpoch[d.WindowID]
	revision, taskID := uint64(0), ""
	if d.Task != nil {
		revision, taskID = d.Task.Revision, d.Task.ID
	}
	if r == nil || attempt.State != recoveryAttemptSettled || !attempt.ExecutionKnown || !attempt.Executed || attempt.EvidenceUnknown || epoch == "" || attempt.ConnectionEpoch != epoch || d.ConnectionEpoch != epoch || attempt.ScopeKey != runtimeScopeKey(d.WindowID, d.Context) || attempt.WindowID != d.WindowID || attempt.Context.ProjectUUID != d.Context.ProjectUUID || attempt.Context.DocumentUUID != d.Context.DocumentUUID || attempt.TaskRevision != revision || attempt.TaskID != taskID || r.RequestID != id || r.WindowID != d.WindowID || r.ProjectUUID != d.Context.ProjectUUID || r.DocumentUUID != d.Context.DocumentUUID || r.ConnectionEpoch != epoch || r.Status != "paused" || r.Generation != d.Generation || r.TaskRevision != revision || r.ObservedFacts == nil || attempt.Request.ID != id || attempt.Request.Action != "debug.batch" {
		return nil, fmt.Errorf("persisted continuation does not match the current connection, task and observation")
	}
	baseline := attempt.ContinuationBaseline
	if baseline == nil {
		// Older records retain the response verbatim, independently of intent.
		baseline = runtimeMap(runtimeMap(runtimeClone(attempt.Evidence))["result"])["_baseline"]
	}
	if baseline == nil {
		return nil, fmt.Errorf("persisted continuation basis is unavailable")
	}
	if len(s.requests) >= 16 {
		protected := s.protectedReceipts()
		for _, candidate := range s.receipts {
			_, hasRequest := s.requests[candidate.RequestID]
			if !protected[candidate.RequestID] && (len(s.requests) < 16 || hasRequest) {
				if err := s.reclaimReceipt(candidate.RequestID); err != nil {
					return nil, err
				}
				break
			}
		}
	}
	if len(s.requests) >= 16 {
		return nil, errRuntimeCapacity
	}
	r.started, r.Historical = true, false
	s.receipts = append(s.receipts, r)
	s.requests[id], s.baselines[id] = attempt.Request, runtimeClone(baseline)
	return r, nil
}

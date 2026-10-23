package daemon

import (
	"errors"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

var errRuntimeCapacity = errors.New("runtime capacity is occupied by active evidence; no action dispatched")
var errRuntimeDuplicateRequest = errors.New("request ID is already retained; no action dispatched")

// capacityReadOnlyActions is deliberately derived from the typed protocol
// catalog. Unknown/raw actions must retain a receipt slot: their effect is not
// safe to infer from a missing Mutates flag.
var capacityReadOnlyActions = func() map[string]bool {
	actions := map[string]bool{}
	for _, action := range protocol.AllActions() {
		if action.Mutates || action.Name == "document.open" || action.Name == "document.close" || action.Name == "schematic.page.open" ||
			action.Name == "system.notify" || action.Name == "system.page_reload" || strings.HasPrefix(action.Name, "view.") {
			continue
		}
		actions[action.Name] = true
	}
	return actions
}()

// capacityReadOnly reports the narrow recovery path that may be forwarded when
// every retained receipt is protected. It never includes document/queue state
// changes, raw JavaScript, saves, or an unknown action.
func capacityReadOnly(req protocol.Request) bool {
	if req.Payload["_taskParent"] != nil || req.Payload["_taskRevision"] != nil || req.Payload["_finishGeneration"] != nil {
		return false
	}
	switch req.Action {
	case "debug.exec_js":
		return false
	case "debug.control":
		return runtimeString(req.Payload["operation"]) == "status"
	case "debug.batch":
		steps := runtimeArray(req.Payload["steps"])
		if len(steps) == 0 {
			return false
		}
		for _, value := range steps {
			step := runtimeMap(value)
			payload := runtimeMap(step["payload"])
			if len(step) == 0 || step["_taskParent"] != nil || step["_taskRevision"] != nil || step["_finishGeneration"] != nil || !capacityReadOnly(protocol.Request{Action: runtimeString(step["action"]), Payload: payload}) {
				return false
			}
		}
		return true
	default:
		return capacityReadOnlyActions[req.Action]
	}
}

// Called under runtime.mu. Completion alone does not release review/save proof.
func (s *runtimeStore) protectedReceipts() map[string]bool {
	protected := map[string]bool{}
	if s.recovery != nil {
		latestRead := map[string]bool{}
		for i := len(s.receipts) - 1; i >= 0; i-- {
			r := s.receipts[i]
			key := r.WindowID + "|" + r.ProjectUUID + "|" + r.DocumentUUID
			if d := s.documents[key]; d != nil && !latestRead[key] && !r.Historical && !r.ContentWrite && r.Status == "action_finished" && r.Generation == d.Generation {
				protected[r.RequestID], latestRead[key] = true, true
			}
		}
	}
	for _, r := range s.receipts {
		if r.Historical || !r.started {
			continue
		}
		if s.recovery != nil && r.Status != "queued" && r.Status != "running" && r.Status != "resuming" && r.Status != "saving" {
			d := s.documents[r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID]
			// Keep current finish proof; other settled history is reloadable from SQLite.
			if r.Status != "action_finished" || d == nil || r.Generation != d.Generation || r.Checks == nil || r.ObservedFacts == nil {
				continue
			}
		}
		switch r.Status {
		case "queued", "running", "resuming", "saving", "paused", "needs_readback":
			protected[r.RequestID] = true
		case "needs_review":
			// Settled review evidence from an older generation cannot finish or resume.
			// Keep its original unknown/fail result in audit JSONL, not forever in the live pool.
			d := s.documents[r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID]
			if d == nil || r.Generation >= d.Generation {
				protected[r.RequestID] = true
			}
		case "action_finished":
			d := s.documents[r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID]
			if d == nil || r.Generation != d.Generation {
				continue
			}
			if d.Task != nil && r.TaskRevision != d.Task.Revision {
				continue
			}
			if r.ContentWrite || (r.Checks != nil && r.ObservedFacts != nil) {
				protected[r.RequestID] = true
			}
		}
	}
	// A save for another scope cannot finish this task; captures follow live checks.
	checkedDocuments := map[string]bool{}
	for _, r := range s.receipts {
		key := r.WindowID + "|" + r.ProjectUUID + "|" + r.DocumentUUID
		if d := s.documents[key]; protected[r.RequestID] && d != nil && r.Generation == d.Generation && !strings.HasSuffix(r.Action, ".save") && (r.Checks != nil || len(r.RequiredChecks) > 0) {
			checkedDocuments[key] = true
		}
	}
	for _, r := range s.receipts {
		key := r.WindowID + "|" + r.ProjectUUID + "|" + r.DocumentUUID
		if d := s.documents[key]; checkedDocuments[key] && d != nil && !r.Historical && r.started && r.Status == "action_finished" && r.Generation == d.Generation && (r.ImageAvailable || r.DRCPassed) {
			protected[r.RequestID] = true
		}
	}
	// Existing parent/continuation/image IDs are the dependency links, not a new journal.
	for changed := true; changed; {
		changed = false
		for _, r := range s.receipts {
			if !protected[r.RequestID] {
				continue
			}
			for _, id := range []string{r.ParentRequestID, runtimeString(runtimeMap(r.Progress)["continuationRequestId"]), runtimeString(runtimeMap(runtimeMap(r.Checks)["visualReview"])["screenshotRequestId"])} {
				if id != "" && !protected[id] {
					protected[id], changed = true, true
				}
			}
		}
	}
	return protected
}

func (s *runtimeStore) reclaimableDocument(protected map[string]bool) string {
	if s.recovery != nil {
		for key, d := range s.documents {
			active := false
			for _, receipt := range s.receipts {
				if protected[receipt.RequestID] && receipt.WindowID == d.WindowID && receipt.ProjectUUID == d.Context.ProjectUUID && receipt.DocumentUUID == d.Context.DocumentUUID {
					active = true
					break
				}
			}
			if !active {
				if err := s.saveScopeLocked(d); err != nil {
					s.failRecovery(err)
					return ""
				}
				return key
			}
		}
		return ""
	}
	current := map[string]bool{}
	for _, key := range s.current {
		// Historical checkpoint routing is not an online window pin.
		if d := s.documents[key]; d != nil && !d.Restored {
			current[key] = true
		}
	}
	for key, d := range s.documents {
		if current[key] || d.ChangesSinceSave || (d.Task != nil && len(d.Task.Remaining) > 0) {
			continue
		}
		used := false
		for _, r := range s.receipts {
			if protected[r.RequestID] && key == r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID {
				used = true
				break
			}
		}
		if !used {
			return key
		}
	}
	return ""
}

func (s *runtimeStore) reclaimDocument(key string) {
	delete(s.documents, key)
	for window, current := range s.current {
		if current == key {
			delete(s.current, window)
		}
	}
	if s.recovery != nil {
		for i := len(s.receipts) - 1; i >= 0; i-- {
			r := s.receipts[i]
			if key == r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID {
				delete(s.requests, r.RequestID)
				delete(s.baselines, r.RequestID)
				s.receipts = append(s.receipts[:i], s.receipts[i+1:]...)
			}
		}
		return
	}
	for _, r := range s.receipts {
		if key == r.WindowID+"|"+r.ProjectUUID+"|"+r.DocumentUUID {
			// A later observation of this page must not reuse an evicted generation.
			r.Historical, r.ImageAvailable, r.DRCPassed = true, false, false
			r.Checks, r.ObservedFacts = nil, nil
			delete(s.requests, r.RequestID)
			delete(s.baselines, r.RequestID)
		}
	}
}

func (s *runtimeStore) reclaimReceipt(id string) error {
	if s.recovery != nil {
		for _, receipt := range s.receipts {
			if receipt.RequestID == id {
				key := receipt.WindowID + "|" + receipt.ProjectUUID + "|" + receipt.DocumentUUID
				if err := s.saveScopeLocked(s.documents[key]); err != nil {
					s.failRecovery(err)
					return err
				}
				break
			}
		}
	}
	for i, r := range s.receipts {
		if r.RequestID == id {
			copy(s.receipts[i:], s.receipts[i+1:])
			s.receipts[len(s.receipts)-1] = nil
			s.receipts = s.receipts[:len(s.receipts)-1]
			break
		}
	}
	delete(s.requests, id)
	delete(s.baselines, id)
	return nil
}

// Reclaim durable cache entries before admission; receipt counts never reject work.
func (s *runtimeStore) admit(req protocol.Request, ctx *protocol.Context) error {
	protected := s.protectedReceipts()
	documentID, batchID := "", ""
	for _, parent := range s.receipts {
		if parent.RequestID != runtimeString(req.Payload["_taskParent"]) || parent.WindowID != req.WindowID || parent.Historical {
			continue
		}
		if parent.Status == "resuming" && req.Action == "debug.batch" {
			// The child's prepared payload already carries the remaining steps and baseline.
			if _, retained := s.requests[parent.RequestID]; retained {
				batchID = parent.RequestID
			}
		}
	}
	if ctx != nil && ctx.ProjectUUID != "" && ctx.DocumentUUID != "" && s.documents[req.WindowID+"|"+ctx.ProjectUUID+"|"+ctx.DocumentUUID] == nil && len(s.documents) >= 8 {
		documentID = s.reclaimableDocument(protected)
		if documentID == "" {
			return errRuntimeCapacity
		}
	}
	receiptIDs := []string{}
	if s.recovery != nil {
		for _, r := range s.receipts {
			if !protected[r.RequestID] {
				receiptIDs = append(receiptIDs, r.RequestID)
			}
		}
	}
	if req.Action == "debug.batch" && len(s.requests) >= 16 && batchID == "" {
		for _, r := range s.receipts {
			if _, found := s.requests[r.RequestID]; found && !protected[r.RequestID] {
				batchID = r.RequestID
				break
			}
		}
		if batchID == "" {
			return errRuntimeCapacity
		}
	}
	if documentID != "" {
		s.reclaimDocument(documentID)
	}
	for _, receiptID := range receiptIDs {
		if err := s.reclaimReceipt(receiptID); err != nil {
			return err
		}
	}
	if batchID != "" {
		if s.recovery != nil {
			for _, receipt := range s.receipts {
				if receipt.RequestID == batchID {
					if err := s.saveScopeLocked(s.documents[receipt.WindowID+"|"+receipt.ProjectUUID+"|"+receipt.DocumentUUID]); err != nil {
						return err
					}
					break
				}
			}
		}
		delete(s.requests, batchID)
		delete(s.baselines, batchID)
	}
	return nil
}

// Early rejection may be audited, but cannot displace an already dispatched proof.
func (s *runtimeStore) retainUntrackedReceipt(receipt *runtimeReceipt) bool {
	s.receipts = append(s.receipts, receipt)
	return true
}

// Confirmed child readback replaces its consumed parent's live resume proof.
func (s *runtimeStore) releaseContinuedParent(child *runtimeReceipt) {
	if child.ParentRequestID == "" || child.Action != "debug.batch" || child.ObservedFacts == nil {
		return
	}
	progress := runtimeMap(child.Progress)
	results := runtimeArray(progress["results"])
	if len(results) != int(runtimeNumber(progress["completed"])) {
		return
	}
	if child.Status == "paused" {
		if s.baselines[child.RequestID] == nil {
			return
		}
	} else if child.Status != "action_finished" || runtimeNumber(progress["completed"]) != runtimeNumber(progress["total"]) {
		return
	}
	for _, result := range results {
		if runtimeMap(result)["ok"] != true {
			return
		}
	}
	for _, parent := range s.receipts {
		if parent.RequestID == child.ParentRequestID && parent.WindowID == child.WindowID && parent.Status == "continued" {
			child.ParentRequestID = ""
			return
		}
	}
}

// An unsent child must not leave its parent permanently claimed as saving/resuming.
func (s *runtimeStore) releaseUnsentClaim(req protocol.Request, parentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.receipts {
		if r.RequestID == req.ID && r.WindowID == req.WindowID && r.started {
			return
		}
	}
	for _, r := range s.receipts {
		if r.RequestID != parentID || r.WindowID != req.WindowID {
			continue
		}
		switch r.Status {
		case "saving":
			r.Status = "action_finished"
		case "resuming":
			r.Status = "paused"
		default:
			return
		}
		s.emit("task_not_dispatched", map[string]any{"requestId": parentID, "windowId": req.WindowID, "status": r.Status})
	}
}

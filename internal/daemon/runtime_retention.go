package daemon

import (
	"errors"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

var errRuntimeCapacity = errors.New("runtime capacity is occupied by active evidence; no action dispatched")

// Called under runtime.mu. Completion alone does not release review/save proof.
func (s *runtimeStore) protectedReceipts() map[string]bool {
	protected := map[string]bool{}
	for _, r := range s.receipts {
		if r.Historical || !r.started {
			continue
		}
		switch r.Status {
		case "queued", "running", "resuming", "saving", "paused", "needs_readback", "needs_review":
			protected[r.RequestID] = true
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

func (s *runtimeStore) reclaimReceipt(id string) {
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
}

// Reserve all three bounded stores atomically before invalidation or dispatch.
func (s *runtimeStore) admit(req protocol.Request, ctx *protocol.Context) error {
	protected := s.protectedReceipts()
	documentID, batchID := "", ""
	// Each window with active proof owns one handoff slot inside the existing 256.
	windows, occupied := map[string]bool{req.WindowID: true}, map[string]bool{}
	for _, r := range s.receipts {
		if protected[r.RequestID] && !r.Historical && r.started {
			windows[r.WindowID] = true
			if r.ParentRequestID != "" {
				occupied[r.WindowID] = true
			}
		}
	}
	receiptLimit := 256
	for window := range windows {
		if !occupied[window] {
			receiptLimit--
		}
	}
	for _, parent := range s.receipts {
		if parent.RequestID != runtimeString(req.Payload["_taskParent"]) || parent.WindowID != req.WindowID || parent.Historical {
			continue
		}
		if (parent.Status == "saving" && req.Payload["_finishGeneration"] != nil) || (parent.Status == "resuming" && (req.Action == "debug.batch" || (req.Action == "debug.control" && req.Payload["operation"] == "resume"))) {
			if !occupied[req.WindowID] {
				receiptLimit++
			}
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
	needed := len(s.receipts) + 1 - receiptLimit
	if needed > 0 {
		for _, r := range s.receipts {
			if !protected[r.RequestID] {
				receiptIDs = append(receiptIDs, r.RequestID)
				if len(receiptIDs) == needed {
					break
				}
			}
		}
		if len(receiptIDs) < needed {
			return errRuntimeCapacity
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
		s.reclaimReceipt(receiptID)
	}
	if batchID != "" {
		delete(s.requests, batchID)
		delete(s.baselines, batchID)
	}
	return nil
}

// Early rejection may be audited, but cannot displace an already dispatched proof.
func (s *runtimeStore) retainUntrackedReceipt(receipt *runtimeReceipt) bool {
	if len(s.receipts) >= 256 {
		protected := s.protectedReceipts()
		candidate := ""
		for _, r := range s.receipts {
			if !protected[r.RequestID] {
				candidate = r.RequestID
				break
			}
		}
		if candidate == "" {
			s.emit("runtime_capacity", map[string]any{"requestId": receipt.RequestID, "windowId": receipt.WindowID, "auditOnly": true})
			return false
		}
		s.reclaimReceipt(candidate)
	}
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

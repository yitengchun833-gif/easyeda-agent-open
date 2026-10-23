package daemon

import (
	"net/http"
	"sort"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Only the index is scanned. Historical objects are loaded after one source
// has been selected by document/session identity, never on daemon startup.
func (s *runtimeStore) historyCandidates(current *runtimeDocument) []recoveryIndexEntry {
	currentKey := runtimeScopeKey(current.WindowID, current.Context)
	byKey := map[string]recoveryIndexEntry{}
	if s.recovery != nil {
		index, err := s.recovery.ListIndex()
		if err != nil {
			s.failRecovery(err)
			return nil
		}
		for _, item := range index {
			byKey[item.Key] = item
		}
	}
	for key, old := range s.documents {
		if !old.Restored {
			delete(byKey, key)
			continue
		}
		byKey[key] = recoveryIndexEntry{Key: key, WindowID: old.WindowID, Context: old.Context, Task: old.Task, Generation: old.Generation, SceneInstance: old.SceneInstance, ObjectCount: len(old.Objects)}
	}
	byTask := map[string]recoveryIndexEntry{}
	for key, item := range byKey {
		if key == currentKey || (item.Task == nil && item.ObjectCount == 0) || item.Context.ProjectUUID != current.Context.ProjectUUID || item.Context.DocumentUUID != current.Context.DocumentUUID {
			continue
		}
		identity := key
		if item.Task != nil && item.Task.ID != "" {
			identity = item.Task.ID
		}
		old, exists := byTask[identity]
		newer := !exists || item.Generation > old.Generation
		if exists && item.Task != nil && old.Task != nil && item.Task.Revision != old.Task.Revision {
			newer = item.Task.Revision > old.Task.Revision
		}
		if newer {
			byTask[identity] = item
		}
	}
	items := make([]recoveryIndexEntry, 0, len(byTask))
	for _, item := range byTask {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items
}

func (s *runtimeStore) historicalSource(key string) *runtimeDocument {
	if old := s.documents[key]; old != nil {
		return old
	}
	if s.recovery == nil {
		return nil
	}
	scope, err := s.recovery.LoadScope(key)
	if err != nil {
		s.failRecovery(err)
		return nil
	}
	if scope == nil || scope.Document == nil {
		return nil
	}
	s.scopeVersions[key] = scope.Revision
	scope.Document.Restored = true
	for _, receipt := range scope.Receipts {
		found := false
		for _, retained := range s.receipts {
			if retained.RequestID == receipt.RequestID {
				found = true
				break
			}
		}
		if found {
			continue
		}
		receipt.Historical = true
		receipt.Checks, receipt.ObservedFacts = nil, nil
		receipt.ImageAvailable, receipt.DRCPassed = false, false
		if receipt.Status == "queued" || receipt.Status == "running" || receipt.Status == "resuming" || receipt.Status == "saving" {
			receipt.Status = "needs_readback"
		}
		s.receipts = append(s.receipts, receipt)
	}
	return scope.Document
}

// Called only after a successful native observation, never from connection
// order alone. The scene instance already supplied by the Connector survives
// a daemon restart. It selects history, but does not certify cached freshness.
func (s *runtimeStore) restoreObservedDocument(current *runtimeDocument, instance string) (*runtimeDocument, bool) {
	if current.Task != nil || len(current.Objects) != 0 {
		return current, false
	}
	candidates := s.historyCandidates(current)
	exact := []recoveryIndexEntry{}
	for _, item := range candidates {
		if item.SceneInstance == instance {
			exact = append(exact, item)
		}
	}
	var selected string
	if len(exact) == 1 {
		selected = exact[0].Key
	} else if len(exact) == 0 && len(candidates) == 1 {
		selected = candidates[0].Key
	}
	if selected != "" {
		if old := s.historicalSource(selected); old != nil {
			restored := s.adoptHistory(current, old, instance)
			return restored, restored != current
		}
	}
	if len(candidates) > 0 {
		windows := []string{}
		sources := []any{}
		for _, old := range candidates {
			windows = append(windows, old.WindowID)
			sources = append(sources, map[string]any{"sourceWindow": old.WindowID, "task": runtimeClone(old.Task), "generation": old.Generation, "evidenceUnknown": old.EvidenceUnknown})
		}
		current.Recovery = map[string]any{"status": "selection_required", "sourceWindows": windows, "sources": sources, "reason": "Multiple meaningful histories; select a matching task using restore. No history was merged or replayed."}
	}
	return current, false
}

func (s *runtimeStore) adoptHistory(current, old *runtimeDocument, instance string) *runtimeDocument {
	oldKey := old.WindowID + "|" + old.Context.ProjectUUID + "|" + old.Context.DocumentUUID
	if s.recovery != nil {
		if s.documents[oldKey] != nil {
			if err := s.saveScopeLocked(old); err != nil {
				s.failRecovery(err)
				return current
			}
		}
		// Preserve the original owner and evidence in storage. Adoption is an
		// intent/cache copy; it grants no permission to replay an unknown write.
		copy := *old
		copy.Objects = map[string]*runtimeObject{}
		for id, object := range old.Objects {
			value := *object
			copy.Objects[id] = &value
		}
		old = &copy
	}
	delete(s.documents, oldKey)
	if s.current[old.WindowID] == oldKey {
		delete(s.current, old.WindowID)
	}
	old.WindowID, old.Context = current.WindowID, current.Context
	old.ConnectionEpoch = current.ConnectionEpoch
	old.Generation = max(old.Generation, current.Generation) + 1
	old.SceneInstance, old.SceneSequence = instance, current.SceneSequence
	old.Restored, old.Recovery = false, nil
	old.Checks, old.Coverage, old.EventCoverage = nil, map[string]bool{}, "unknown"
	for _, obj := range old.Objects {
		obj.StaleFields = map[string]bool{"identity": true, "geometry": true, "pins": true}
	}
	key := old.WindowID + "|" + old.Context.ProjectUUID + "|" + old.Context.DocumentUUID
	s.documents[key], s.current[old.WindowID] = old, key
	return old
}

// Explicit recovery selects historical context, not an action to execute.
// It cannot overwrite live work, cross document identities or restore proofs.
func (s *Server) restoreRuntimeTask(w http.ResponseWriter, window, source string, target *protocol.Context, generation uint64) {
	release, acquired := s.acquireExclusive("runtime-task-window", window)
	if !acquired {
		http.Error(w, "Task or queue control is changing; inspect current state before restoring", http.StatusConflict)
		return
	}
	defer release()
	s.runtime.mu.Lock()
	current := s.runtime.documents[s.runtime.current[window]]
	var old *runtimeDocument
	if target != nil {
		old = s.runtime.historicalSource(source + "|" + target.ProjectUUID + "|" + target.DocumentUUID)
	}
	if current == nil || current.Restored || old == nil || !old.Restored || current == old || target == nil || current.Context.ProjectUUID != target.ProjectUUID || current.Context.DocumentUUID != target.DocumentUUID || current.Generation != generation || current.SceneInstance == "" || current.Task != nil || len(current.Objects) != 0 || s.runtime.pendingWrite(window, "") {
		s.runtime.mu.Unlock()
		http.Error(w, "Restore requires a current native document observation, matching target/generation, empty destination and a historical source", 409)
		return
	}
	restored := s.runtime.adoptHistory(current, old, current.SceneInstance)
	if restored == current && s.runtime.recoveryError != nil {
		err := s.runtime.recoveryError
		s.runtime.mu.Unlock()
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "persisted": false, "error": map[string]any{"code": "RECOVERY_STORE_FAILED", "message": err.Error()}, "automaticReplay": false})
		return
	}
	s.runtime.emit("history_restored", map[string]any{"windowId": window, "sourceWindow": source})
	result := map[string]any{"ok": true, "task": runtimeClone(restored.Task), "generation": restored.Generation, "edaReads": 0, "requiresScopedReadback": true, "automaticReplay": false}
	s.runtime.mu.Unlock()
	err := s.runtime.flushCheckpoint()
	result["persisted"] = err == nil && s.runtime.checkpointPath != ""
	if err != nil {
		result["persistenceError"] = err.Error()
	}
	writeJSON(w, 200, result)
}

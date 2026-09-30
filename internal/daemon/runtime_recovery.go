package daemon

import (
	"net/http"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Called only after a successful native observation, never from connection
// order alone. The scene instance already supplied by the Connector survives
// a daemon restart. It selects history, but does not certify cached freshness.
func (s *runtimeStore) restoreObservedDocument(current *runtimeDocument, instance string) (*runtimeDocument, bool) {
	if current.Task != nil || len(current.Objects) != 0 {
		return current, false
	}
	candidates, exact := []*runtimeDocument{}, []*runtimeDocument{}
	for _, old := range s.documents {
		if !old.Restored || old == current || (old.Task == nil && len(old.Objects) == 0) || old.Context.ProjectUUID != current.Context.ProjectUUID || old.Context.DocumentUUID != current.Context.DocumentUUID {
			continue
		}
		candidates = append(candidates, old)
		if old.SceneInstance == instance {
			exact = append(exact, old)
		}
	}
	var selected *runtimeDocument
	if len(exact) == 1 {
		selected = exact[0]
	} else if len(exact) == 0 && len(candidates) == 1 {
		selected = candidates[0]
	}
	if selected != nil {
		return s.adoptHistory(current, selected, instance), true
	}
	if len(candidates) > 0 {
		windows := []string{}
		for _, old := range candidates {
			windows = append(windows, old.WindowID)
		}
		current.Recovery = map[string]any{"status": "selection_required", "sourceWindows": windows, "reason": "Multiple meaningful histories; select a matching task using restore. No history was merged or replayed."}
	}
	return current, false
}

func (s *runtimeStore) adoptHistory(current, old *runtimeDocument, instance string) *runtimeDocument {
	oldKey := old.WindowID + "|" + old.Context.ProjectUUID + "|" + old.Context.DocumentUUID
	delete(s.documents, oldKey)
	if s.current[old.WindowID] == oldKey {
		delete(s.current, old.WindowID)
	}
	old.WindowID, old.Context = current.WindowID, current.Context
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
	s.runtime.mu.Lock()
	current := s.runtime.documents[s.runtime.current[window]]
	var old *runtimeDocument
	if target != nil {
		old = s.runtime.documents[source+"|"+target.ProjectUUID+"|"+target.DocumentUUID]
	}
	if current == nil || current.Restored || old == nil || !old.Restored || current == old || target == nil || current.Context.ProjectUUID != target.ProjectUUID || current.Context.DocumentUUID != target.DocumentUUID || current.Generation != generation || current.SceneInstance == "" || current.Task != nil || len(current.Objects) != 0 || s.runtime.pendingWrite(window, "") {
		s.runtime.mu.Unlock()
		http.Error(w, "Restore requires a current native document observation, matching target/generation, empty destination and a historical source", 409)
		return
	}
	restored := s.runtime.adoptHistory(current, old, current.SceneInstance)
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

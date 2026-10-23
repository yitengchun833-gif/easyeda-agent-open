package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Task is caller-authored intent, never evidence of execution or permission to
// replay actions. The existing receipts and native observations remain truth.
type runtimeTask struct {
	ID           string    `json:"id,omitempty"`
	Revision     uint64    `json:"revision"`
	Goal         string    `json:"goal"`
	PrimitiveIDs []string  `json:"primitiveIds"`
	Remaining    []string  `json:"remaining"`
	Note         string    `json:"note,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

type runtimeCheckpoint struct {
	Version   int                         `json:"version"`
	At        time.Time                   `json:"at"`
	Documents map[string]*runtimeDocument `json:"documents"`
	Current   map[string]string           `json:"current"`
	Receipts  []*runtimeReceipt           `json:"receipts"`
}

// Legacy intent did not have a task UUID. Its source identity and revision
// supply a stable label without inventing execution or replay permission.
func legacyRuntimeTaskID(scopeKey string, revision uint64) string {
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%d", scopeKey, revision)))
	return fmt.Sprintf("legacy-%x", digest[:16])
}

func (s *runtimeStore) persistenceStatus() map[string]any {
	backend := "unavailable"
	if s.recovery != nil {
		backend = "sqlite"
	}
	return map[string]any{"enabled": s.checkpointPath != "" && s.checkpointPath != ":memory:", "backend": backend, "savedAt": s.checkpointAt, "error": s.checkpointError,
		"historicalScopes": len(s.scopeVersions), "taskIsIntent": true, "automaticReplay": false, "legacyCheckpointReadOnly": s.recovery != nil}
}

func (s *runtimeStore) flushCheckpoint() error {
	if s.recovery != nil {
		return s.flushRecoveryScopes()
	}
	if s.recoveryError != nil {
		return s.recoveryError
	}
	if s.checkpointPath != "" {
		return s.failRecovery(fmt.Errorf("recovery storage is unavailable"))
	}
	return nil
}

// Coalesce disposable observation updates. Execution intents and results use
// synchronous recovery commits on the dispatch path, outside this timer.
func (s *runtimeStore) checkpointLoop(ctx context.Context) {
	if s.checkpointPath == "" {
		return
	}
	for {
		select {
		case <-ctx.Done():
			_ = s.flushCheckpoint()
			return
		case <-s.dirty:
			timer := time.NewTimer(200 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				_ = s.flushCheckpoint()
				return
			case <-timer.C:
			}
			_ = s.flushCheckpoint()
		}
	}
}

func (s *Server) updateRuntimeTask(w http.ResponseWriter, window string, target *protocol.Context, expected *uint64, task *runtimeTask) {
	release, acquired := s.acquireExclusive("runtime-task-window", window)
	if !acquired {
		http.Error(w, "Task or queue control is changing; inspect current state before updating", 409)
		return
	}
	defer release()
	s.commitRuntimeTask(w, window, target, expected, task, nil)
}

func validateRuntimeTask(target *protocol.Context, expected *uint64, task *runtimeTask) string {
	if target == nil || target.ProjectUUID == "" || target.DocumentUUID == "" || expected == nil || task == nil || strings.TrimSpace(task.Goal) == "" || len(task.Goal) > 2000 || len(task.Note) > 4000 || len(task.PrimitiveIDs) > 500 || len(task.Remaining) > 32 {
		return "Task change needs explicit target, expectedRevision and bounded task goal/scope/remaining"
	}
	for _, id := range task.PrimitiveIDs {
		if id == "" || len(id) > 256 {
			return "Invalid task primitive ID"
		}
	}
	for _, step := range task.Remaining {
		if strings.TrimSpace(step) == "" || len(step) > 1000 {
			return "Invalid remaining step"
		}
	}
	return ""
}

// Caller holds the existing per-window task slot; runtime.mu owns the CAS.
func (s *runtimeStore) taskTarget(window string, target *protocol.Context, expected uint64) (*runtimeDocument, error) {
	d := s.documents[s.current[window]]
	if d == nil || d.Restored || d.Context.ProjectUUID != target.ProjectUUID || d.Context.DocumentUUID != target.DocumentUUID {
		return nil, fmt.Errorf("Read the current target identity first")
	}
	revision := uint64(0)
	if d.Task != nil {
		revision = d.Task.Revision
	}
	if expected != revision {
		return nil, fmt.Errorf("Task revision changed; read current task before updating")
	}
	return d, nil
}

func (s *Server) commitRuntimeTask(w http.ResponseWriter, window string, target *protocol.Context, expected *uint64, task *runtimeTask, extra map[string]any) {
	if invalid := validateRuntimeTask(target, expected, task); invalid != "" {
		http.Error(w, invalid, 400)
		return
	}
	s.runtime.mu.Lock()
	d, targetErr := s.runtime.taskTarget(window, target, *expected)
	if targetErr != nil {
		s.runtime.mu.Unlock()
		http.Error(w, targetErr.Error(), 409)
		return
	}
	previousTask := d.Task
	if previousTask != nil && previousTask.ID != "" && extra["replaced"] != true {
		task.ID = previousTask.ID
	} else {
		task.ID = rand.Text()
	}
	task.Revision, task.UpdatedAt = *expected+1, time.Now().UTC()
	d.Task = task
	if s.runtime.recovery != nil {
		if err := s.runtime.saveScopeLocked(d); err != nil {
			d.Task = previousTask
			s.runtime.failRecovery(err)
			s.runtime.mu.Unlock()
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"ok": false, "persisted": false, "taskIsIntent": true,
				"error": map[string]any{"code": "RECOVERY_STORE_FAILED", "message": err.Error()}, "edaReads": 0, "automaticReplay": false})
			return
		}
	}
	s.runtime.emit("task_updated", map[string]any{"windowId": window, "revision": task.Revision})
	result := runtimeClone(task)
	s.runtime.mu.Unlock()
	// The task is local intent. SQLite has already committed this update; legacy
	// readers still flush their projection through the compatibility path.
	err := s.runtime.flushCheckpoint()
	response := map[string]any{"ok": true, "task": result, "edaReads": 0, "taskIsIntent": true, "persisted": err == nil && s.runtime.checkpointPath != ""}
	for key, value := range extra {
		response[key] = value
	}
	if extra != nil {
		delete(response, "edaReads") // Replacement controls perform native target identity reads.
	}
	if err != nil {
		response["persistenceError"] = err.Error()
	}
	writeJSON(w, 200, response)
}

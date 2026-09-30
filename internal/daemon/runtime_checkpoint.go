package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Task is caller-authored intent, never evidence of execution or permission to
// replay actions. The existing receipts and native observations remain truth.
type runtimeTask struct {
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

// The one checkpoint is a persisted projection of the existing runtime. Audit
// JSONL remains the history; queues, private baselines and replay proofs are not
// serialized. This deliberately does not implement another workflow engine.
func (s *runtimeStore) restoreCheckpoint(path string) bool {
	s.checkpointPath, s.dirty = path, make(chan struct{}, 1)
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		s.checkpointError = err.Error()
		return false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 64*1024*1024+1))
	var saved runtimeCheckpoint
	if err != nil || len(data) > 64*1024*1024 || json.Unmarshal(data, &saved) != nil || saved.Version != 1 || len(saved.Documents) > 8 || len(saved.Receipts) > 256 {
		s.checkpointError = "Checkpoint unreadable or unsupported; audit receipts only"
		return false
	}
	for key, d := range saved.Documents {
		if d == nil || d.Context.ProjectUUID == "" || d.Context.DocumentUUID == "" || key != d.WindowID+"|"+d.Context.ProjectUUID+"|"+d.Context.DocumentUUID {
			s.checkpointError = "Checkpoint document identity invalid; audit receipts only"
			return false
		}
		if d.Task == nil && len(d.Objects) == 0 {
			delete(saved.Documents, key)
			continue
		}
		if d.Objects == nil {
			d.Objects = map[string]*runtimeObject{}
		}
		for id, object := range d.Objects {
			if object == nil || object.Data == nil {
				delete(d.Objects, id)
				continue
			}
			object.StaleFields = map[string]bool{"identity": true, "geometry": true, "pins": true}
		}
		d.Coverage = map[string]bool{}
		// Preserve the connector session identity for routing only, not freshness.
		d.Checks, d.Recovery, d.EventCoverage = nil, nil, "unknown"
		d.Restored = true
		d.Generation++
	}
	for _, receipt := range saved.Receipts {
		if receipt == nil {
			s.checkpointError = "Invalid checkpoint receipt"
			return false
		}
		receipt.Historical = true
		receipt.Checks, receipt.ObservedFacts = nil, nil
		receipt.ImageAvailable, receipt.DRCPassed = false, false
		switch receipt.Status {
		case "queued", "running", "resuming", "saving", "paused":
			receipt.Status = "needs_readback"
		}
	}
	if saved.Documents != nil {
		s.documents = saved.Documents
	}
	for window, key := range saved.Current {
		if s.documents[key] != nil && s.documents[key].WindowID == window {
			s.current[window] = key
		}
	}
	s.receipts, s.checkpointAt = saved.Receipts, &saved.At
	return true
}

func (s *runtimeStore) persistenceStatus() map[string]any {
	return map[string]any{"enabled": s.checkpointPath != "", "savedAt": s.checkpointAt, "error": s.checkpointError,
		"taskIsIntent": true, "automaticReplay": false}
}

func (s *runtimeStore) flushCheckpoint() error {
	if s.checkpointPath == "" {
		return nil
	}
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	now := time.Now().UTC()
	s.mu.Lock()
	data, err := json.Marshal(runtimeCheckpoint{1, now, s.documents, s.current, s.receipts})
	s.mu.Unlock()
	if err == nil && len(data) > 64*1024*1024 {
		err = fmt.Errorf("checkpoint exceeds 64 MiB; audit history retained")
	}
	if err == nil {
		err = os.MkdirAll(filepath.Dir(s.checkpointPath), 0o700)
	}
	if err == nil {
		var f *os.File
		f, err = os.CreateTemp(filepath.Dir(s.checkpointPath), ".runtime-*.tmp")
		if err == nil {
			name := f.Name()
			defer os.Remove(name)
			_, err = f.Write(data)
			if err == nil {
				err = f.Sync()
			}
			closeErr := f.Close()
			if err == nil {
				err = closeErr
			}
			if err == nil {
				err = os.Rename(name, s.checkpointPath)
			}
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		s.checkpointError = err.Error()
	} else {
		s.checkpointError = ""
		s.checkpointAt = &now
	}
	return err
}

// Coalesce writes without adding disk I/O to each EDA call. A forced kill can
// lose the last 200 ms of this cache; audit is retained and all recovery is stale.
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
	if target == nil || target.ProjectUUID == "" || target.DocumentUUID == "" || expected == nil || task == nil || strings.TrimSpace(task.Goal) == "" || len(task.Goal) > 2000 || len(task.Note) > 4000 || len(task.PrimitiveIDs) > 500 || len(task.Remaining) > 32 {
		http.Error(w, "task_update needs explicit target, expectedRevision and bounded task goal/scope/remaining", 400)
		return
	}
	for _, id := range task.PrimitiveIDs {
		if id == "" || len(id) > 256 {
			http.Error(w, "Invalid task primitive ID", 400)
			return
		}
	}
	for _, step := range task.Remaining {
		if strings.TrimSpace(step) == "" || len(step) > 1000 {
			http.Error(w, "Invalid remaining step", 400)
			return
		}
	}
	s.runtime.mu.Lock()
	d := s.runtime.documents[s.runtime.current[window]]
	if d == nil || d.Restored || d.Context.ProjectUUID != target.ProjectUUID || d.Context.DocumentUUID != target.DocumentUUID {
		s.runtime.mu.Unlock()
		http.Error(w, "Read the current target identity first", 409)
		return
	}
	revision := uint64(0)
	if d.Task != nil {
		revision = d.Task.Revision
	}
	if *expected != revision {
		s.runtime.mu.Unlock()
		http.Error(w, "Task revision changed; read current task before updating", 409)
		return
	}
	task.Revision, task.UpdatedAt = revision+1, time.Now().UTC()
	d.Task = task
	s.runtime.emit("task_updated", map[string]any{"windowId": window, "revision": task.Revision})
	result := runtimeClone(task)
	s.runtime.mu.Unlock()
	// Only explicit goal changes wait for durability; ordinary drawing does not.
	err := s.runtime.flushCheckpoint()
	response := map[string]any{"ok": true, "task": result, "edaReads": 0, "taskIsIntent": true, "persisted": err == nil && s.runtime.checkpointPath != ""}
	if err != nil {
		response["persistenceError"] = err.Error()
	}
	writeJSON(w, 200, response)
}

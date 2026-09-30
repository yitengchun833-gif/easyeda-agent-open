package daemon

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

//go:embed dashboard.html
var dashboardHTML []byte

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/dashboard" && r.URL.Path != "/dashboard/" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	_, _ = w.Write(dashboardHTML)
}

// Build a bounded view of already observed receipts. Never dispatch an EDA
// request, copy full object data, or load artifacts to render the console.
func (s *runtimeStore) dashboardSnapshotLocked() map[string]any {
	documents := []any{}
	for window, key := range s.current {
		d := s.documents[key]
		if d == nil {
			continue
		}
		counts := map[string]int{}
		stale := 0
		for _, object := range d.Objects {
			counts[runtimeString(object.Data["kind"])]++
			for _, invalid := range object.StaleFields {
				if invalid {
					stale++
					break
				}
			}
		}
		documents = append(documents, map[string]any{
			"task": d.Task, "restored": d.Restored, "recovery": d.Recovery,
			"windowId": window, "context": d.Context, "generation": d.Generation,
			"counts": counts, "staleObjects": stale, "coverage": d.Coverage,
			"eventCoverage": d.EventCoverage, "savedAt": d.SavedAt,
			"freshness": "external_unconfirmed", "requiresScopedReadback": true,
			"mutationAttemptedSinceConfirmedSave": d.ChangesSinceSave,
			"checks":                              dashboardChecks(d.Checks),
		})
	}
	receipts := []any{}
	var duration int64
	finished, failed := 0, 0
	for i, receipt := range s.receipts {
		if receipt.Status != "queued" && receipt.Status != "running" {
			finished++
			duration += receipt.DurationMs
		}
		if receipt.ErrorCode != "" || receipt.Status == "needs_readback" {
			failed++
		}
		if i < len(s.receipts)-64 {
			continue
		}
		progress := runtimeMap(receipt.Progress)
		receipts = append(receipts, map[string]any{
			"taskRevision": receipt.TaskRevision, "historical": receipt.Historical,
			"requestId": receipt.RequestID, "windowId": receipt.WindowID,
			"documentUuid": receipt.DocumentUUID, "projectUuid": receipt.ProjectUUID, "generation": receipt.Generation,
			"action": receipt.Action, "status": receipt.Status, "startedAt": receipt.StartedAt,
			"durationMs": receipt.DurationMs, "errorCode": receipt.ErrorCode,
			"error":    dashboardText(receipt.Error, 600),
			"progress": map[string]any{"completed": progress["completed"], "total": progress["total"], "phase": progress["phase"]},
		})
	}
	// Clone small maps under the mutex; the socket write happens after unlocking.
	return runtimeMap(runtimeClone(map[string]any{
		"instance": s.instance, "sequence": s.sequence, "documents": documents, "receipts": receipts,
		"metrics": map[string]any{"retainedRequests": len(s.receipts), "finished": finished, "failed": failed, "durationMs": duration},
		"source":  "daemon-receipts", "edaReads": 0, "modelCalls": 0,
		"persistence": s.persistenceStatus(),
	}))
}

func dashboardText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit]) + "…"
	}
	return value
}

func dashboardChecks(value any) map[string]any {
	checks := runtimeMap(value)
	result := runtimeMap(checks["result"])
	items := []any{}
	for i, value := range runtimeArray(result["items"]) {
		if i >= 32 {
			break
		}
		item := runtimeMap(value)
		evidence, _ := json.Marshal(item["evidence"])
		targets := runtimeArray(item["targets"])
		if len(targets) > 12 {
			targets = targets[:12]
		}
		items = append(items, map[string]any{
			"id": item["id"], "status": item["status"], "severity": item["severity"],
			"coverage": item["coverage"], "targets": targets, "evidence": dashboardText(string(evidence), 400),
		})
	}
	var visual any
	if review := runtimeMap(result["visualReview"]); review != nil {
		visual = map[string]any{"generation": review["generation"], "source": review["source"], "at": review["at"]}
	}
	return map[string]any{"generation": checks["generation"], "checkedAt": checks["checkedAt"], "status": result["status"], "items": items, "visualReview": visual}
}

// A cursor is scoped to one daemon instance. Missing history sends the latest
// cached projection instead of pretending that a replay is complete.
func (s *runtimeStore) dashboardFrame(cursor string) (map[string]any, string, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	instance, raw, valid := strings.Cut(cursor, ":")
	after, err := strconv.ParseUint(raw, 10, 64)
	reset := !valid || err != nil || instance != s.instance || after > s.sequence
	if !reset && len(s.events) > 0 && after+1 < s.events[0].Sequence {
		reset = true
	}
	events := []any{}
	if !reset {
		for _, event := range s.events {
			if event.Sequence <= after {
				continue
			}
			// Events are hints; omit arbitrary action payloads/results here.
			events = append(events, map[string]any{"sequence": event.Sequence, "type": event.Type, "at": event.At, "requestId": event.Data["requestId"], "windowId": event.Data["windowId"]})
		}
	}
	return map[string]any{"reset": reset, "events": events, "state": s.dashboardSnapshotLocked()}, fmt.Sprintf("%s:%d", s.instance, s.sequence), s.notify
}

func (s *Server) handleRuntimeEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET required", http.StatusMethodNotAllowed)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host != r.Host {
			http.Error(w, "same origin required", http.StatusForbidden)
			return
		}
	}
	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	write := func(data string) error {
		// A slow/disconnected browser never holds the runtime mutex or a writer.
		_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
		if _, err := fmt.Fprint(w, data); err != nil {
			return err
		}
		return controller.Flush()
	}
	cursor := r.Header.Get("Last-Event-ID")
	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		frame, next, changed := s.runtime.dashboardFrame(cursor)
		frame["windows"] = s.hub.listAnnotated(s.opts.Version)
		frame["version"] = s.opts.Version
		data, err := json.Marshal(frame)
		if err != nil || write(fmt.Sprintf("id: %s\nevent: state\ndata: %s\n\n", next, data)) != nil {
			return
		}
		cursor = next
	wait:
		for {
			select {
			case <-r.Context().Done():
				return
			case <-changed:
				// Coalesce native edit bursts into at most ten console updates/sec.
				timer := time.NewTimer(100 * time.Millisecond)
				select {
				case <-r.Context().Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				break wait
			case <-keepalive.C:
				if write(": keepalive\n\n") != nil {
					return
				}
			}
		}
	}
}

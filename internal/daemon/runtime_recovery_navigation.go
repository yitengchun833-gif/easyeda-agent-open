package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Only recoveryOpenDocument constructs this request-bound proof; payload flags
// cannot grant a navigation exception or suppress the normal durable intent.
type runtimeNavigationKey struct{}
type runtimeNavigationProof struct {
	RequestID, Window, Epoch, Destination string
	Source                                protocol.Context
	Generation, Revision                  uint64
	Attempts                              []recoveryDispositionAttempt
	UnsavedScopes                         []recoveryDispositionScope
}

func (p *runtimeNavigationProof) matches(req protocol.Request, ctx protocol.Context) bool {
	if p == nil {
		return false
	}
	target := runtimeMap(req.Payload["_target"])
	return req.ID == p.RequestID && req.WindowID == p.Window && req.Action == "document.open" && req.Payload["uuid"] == p.Destination && ctx.ProjectUUID == p.Source.ProjectUUID && ctx.DocumentUUID == p.Source.DocumentUUID && target["projectUuid"] == p.Source.ProjectUUID && target["documentUuid"] == p.Source.DocumentUUID
}

func (p *runtimeNavigationProof) coversAttempt(s *runtimeStore, attempt recoveryAttempt) bool {
	if attempt.State != recoveryAttemptUnknown || attempt.Context.ProjectUUID != p.Source.ProjectUUID {
		return false
	}
	for _, ref := range p.Attempts {
		if ref.RequestID == attempt.RequestID && ref.Revision == attempt.Revision && ref.ScopeKey == attempt.ScopeKey && ref.WindowID == attempt.WindowID {
			_, digest, err := s.recovery.LoadAttemptEvidence(attempt.RequestID)
			return err == nil && digest == ref.SHA256
		}
	}
	return false
}

func (s *Server) recoveryOpenDocument(w http.ResponseWriter, r *http.Request, window string, target *protocol.Context, expected *uint64, generation uint64, input *runtimeAcceptanceInput) {
	if target == nil || target.ProjectUUID == "" || target.DocumentUUID == "" || expected == nil || input == nil || !input.AcceptCurrentState || input.ConnectionEpoch == "" || len(input.ExpectedBaselineSHA256) != 64 || strings.TrimSpace(input.Reason) == "" || len(input.Reason) > 4000 || len(input.Attempts) == 0 || len(input.Attempts) > 16 || len(input.UnsavedScopes) > 16 {
		http.Error(w, "Recovery navigation requires exact target, source revision/generation, reviewed baseline and bounded termination evidence", http.StatusBadRequest)
		return
	}
	fail := func(message string) { http.Error(w, message, http.StatusConflict) }
	release, acquired := s.acquireExclusive("runtime-task-window", window)
	if !acquired {
		fail("Task or queue control is changing")
		return
	}
	defer release()
	releaseGeometry, acquired := s.acquireExclusive("schematic-geometry-window", window)
	if !acquired {
		fail("A write or document transition has not settled")
		return
	}
	defer releaseGeometry()
	connection, live := s.hub.target(window)
	if !live || connection.epoch != input.ConnectionEpoch || s.runtime.recovery == nil {
		fail("Current connection or recovery store unavailable")
		return
	}
	s.runtime.mu.Lock()
	d := s.runtime.documents[s.runtime.current[window]]
	valid := d != nil && d.Context.ProjectUUID == target.ProjectUUID && d.Context.DocumentType == "schematic" && d.Context.DocumentUUID != target.DocumentUUID && d.Generation == generation && d.ConnectionEpoch == input.ConnectionEpoch && s.runtime.connectionEpoch[window] == input.ConnectionEpoch && s.runtime.recoveryError == nil
	var source protocol.Context
	if d != nil {
		source = d.Context
		revision := uint64(0)
		if d.Task != nil {
			revision = d.Task.Revision
		}
		valid = valid && revision == *expected
	}
	s.runtime.mu.Unlock()
	if !valid {
		fail("Current source project/task/generation differs; preview the current page first")
		return
	}
	if _, err := s.acceptanceEvidence(input, window); err != nil {
		fail(err.Error())
		return
	}
	for _, ref := range input.Attempts {
		attempt, _, err := s.runtime.recovery.LoadAttemptEvidence(ref.RequestID)
		if err != nil || attempt == nil || attempt.Context.ProjectUUID != source.ProjectUUID {
			fail("Recovery evidence belongs to another project")
			return
		}
	}
	index, err := s.runtime.recovery.ListIndex()
	if err != nil {
		fail("Recovery scope index unavailable")
		return
	}
	seenScopes := map[string]bool{}
	for _, ref := range input.UnsavedScopes {
		found := false
		for _, scope := range index {
			if scope.Key == ref.Key && scope.Revision == ref.Revision && scope.ConnectionEpoch == ref.ConnectionEpoch && scope.ChangesSinceSave && scope.Context.ProjectUUID == source.ProjectUUID && scope.WindowID == input.NativeEndEvidence.OldWindow {
				found = true
			}
		}
		if !found || seenScopes[ref.Key] {
			fail("Unsaved scope evidence changed or is unrelated to the ended host")
			return
		}
		seenScopes[ref.Key] = true
	}
	queueIdle := func() bool {
		queue, err := s.replacementQueueControl(r, window, &source, "status")
		return err == nil && queue["paused"] == false && runtimeNumber(queue["pending"]) == 0 && len(runtimeArray(queue["inFlight"])) == 0
	}
	if !queueIdle() {
		fail("Current queue must be idle and unpaused; no navigation sent")
		return
	}
	if _, err := s.baselineRead(r, window, &source, "document.current"); err != nil {
		fail(err.Error())
		return
	}
	_, canonical, hash, err := s.readAcceptanceBaseline(r, window, &source)
	if err != nil || hash != input.ExpectedBaselineSHA256 {
		fail("Native structure changed from the reviewed baseline; no navigation sent")
		return
	}
	found := false
	for _, value := range runtimeArray(canonical["pages"]) {
		if runtimeMap(value)["uuid"] == target.DocumentUUID {
			found = true
		}
	}
	if !found || !queueIdle() {
		fail("Target is absent from verified project schematic pages or queue changed; no navigation sent")
		return
	}
	s.runtime.mu.Lock()
	current := s.runtime.documents[s.runtime.current[window]]
	active, connected := s.hub.target(window)
	revision := uint64(0)
	if current != nil && current.Task != nil {
		revision = current.Task.Revision
	}
	valid = current == d && connected && active == connection && d.Generation == generation && revision == *expected && s.runtime.connectionEpoch[window] == input.ConnectionEpoch && d.SceneInstance != "" && !s.runtime.pendingWrite(window, "")
	s.runtime.mu.Unlock()
	if !valid {
		fail("Source identity/task/connection changed during readback; no navigation sent")
		return
	}
	openID := s.nextRequestID()
	proof := &runtimeNavigationProof{RequestID: openID, Window: window, Epoch: input.ConnectionEpoch, Source: source, Destination: target.DocumentUUID, Generation: generation, Revision: *expected, Attempts: input.Attempts, UnsavedScopes: input.UnsavedScopes}
	internal := r.Clone(context.WithValue(r.Context(), runtimeNavigationKey{}, proof))
	probe := &runtimeResponseWriter{header: http.Header{}}
	internal.Header = internal.Header.Clone()
	internal.Header.Del("X-Easyeda-Response")
	s.runtimeDispatch(probe, internal, protocol.Request{Envelope: protocol.Envelope{ID: openID, WindowID: window}, Action: "document.open", TimeoutMs: 10000, Payload: map[string]any{"uuid": target.DocumentUUID, "_target": map[string]any{"projectUuid": source.ProjectUUID, "documentUuid": source.DocumentUUID}}})
	var response protocol.Response
	if probe.status >= 400 || json.Unmarshal(probe.body.Bytes(), &response) != nil || !response.OK || response.Result["partial"] == true || response.Result["ok"] == false || response.Result["ready"] != true || response.Context == nil || response.Context.ProjectUUID != source.ProjectUUID || response.Context.DocumentUUID != target.DocumentUUID {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "navigated": false, "openRequestId": openID, "executionState": "unknown", "automaticReplay": false, "error": "Native navigation was not confirmed; inspect this exact receipt and current identity, do not retry"})
		return
	}
	observation, err := s.baselineRead(r, window, target, "document.current")
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "navigated": false, "openRequestId": openID, "automaticReplay": false, "error": fmt.Sprintf("Native target readback failed: %v; do not retry navigation", err)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "navigated": true, "from": source, "target": response.Context, "openRequestId": openID, "readbackRequestId": observation["requestId"], "ready": true, "accepted": false, "requiresCurrentBaselineAcceptance": true, "automaticReplay": false, "contentWrites": 0, "oldResultsRemainUnknown": true})
}

package daemon

import (
	"fmt"
	"net/http"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

// Only server-created continuation/finish proofs bind a new dispatch to old work.
func runtimeFreshRequest(req protocol.Request) bool {
	return req.Payload["_taskParent"] == nil && req.Payload["_taskRevision"] == nil && req.Payload["_finishGeneration"] == nil && req.Payload["_expectedObserved"] == nil && req.Payload["_resumeBaseline"] == nil
}

func runtimeConflictingAttempt(req protocol.Request, ctx protocol.Context, attempt recoveryAttempt) bool {
	return attempt.WindowID == req.WindowID || (ctx.ProjectUUID != "" && attempt.Context.ProjectUUID == ctx.ProjectUUID) ||
		(req.Action == "project.open" && runtimeString(req.Payload["projectUuid"]) != "" && attempt.Context.ProjectUUID == runtimeString(req.Payload["projectUuid"]))
}

// A timed-out native call can still be running on another connected window.
// Check its existing queue, without settling or rewriting the original outcome.
func (s *Server) checkFreshNativeExecution(r *http.Request, req protocol.Request, ctx protocol.Context) error {
	if !runtimeFreshRequest(req) || !runtimeDangerous(req) || s.runtime.recovery == nil {
		return nil
	}
	unresolved, err := s.runtime.recovery.ListUnresolved()
	if err != nil {
		return fmt.Errorf("%w: %v", errRuntimePersistence, err)
	}
	checked := map[string]bool{}
	for _, attempt := range unresolved {
		if attempt.RequestID == req.ID || (!attempt.Dangerous && !attempt.ContentWrite) || !runtimeConflictingAttempt(req, ctx, attempt) {
			continue
		}
		connection, connected := s.hub.target(attempt.WindowID)
		if !connected || attempt.ConnectionEpoch == "" || connection.epoch != attempt.ConnectionEpoch {
			if attempt.State == recoveryAttemptPending || attempt.State == recoveryAttemptDispatched {
				return fmt.Errorf("%w: request %s lost contact before its native call ended", errRuntimeScopeBusy, attempt.RequestID)
			}
			continue // Historical terminal unknowns are evidence, not execution slots.
		}
		if checked[attempt.WindowID] {
			continue
		}
		checked[attempt.WindowID] = true
		current := connection.snapshot().Context
		queue, err := s.replacementQueueControl(r, attempt.WindowID, &current, "status")
		if err != nil {
			return fmt.Errorf("%w: native queue for %s could not be read: %v", errRuntimeScopeBusy, attempt.WindowID, err)
		}
		if len(runtimeArray(queue["inFlight"])) != 0 || runtimeNumber(queue["pending"]) != 0 {
			return fmt.Errorf("%w: native queue for %s has unfinished calls", errRuntimeScopeBusy, attempt.WindowID)
		}
		active, ok := s.hub.target(attempt.WindowID)
		if !ok || active != connection {
			return fmt.Errorf("%w: native connection changed during queue read", errRuntimeScopeBusy)
		}
	}
	return nil
}

func (s *runtimeStore) rejectFreshIntent(id string, response protocol.Response) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	attempt := s.attempts[id]
	if attempt == nil || attempt.State != recoveryAttemptPending {
		return fmt.Errorf("unsent intent is not pending")
	}
	attempt.State, attempt.ExecutionKnown, attempt.Executed = recoveryAttemptNotDispatched, true, false
	attempt.Evidence = fromResponse(attempt.UpdatedAt, &attempt.Request, &response)
	if attempt.Receipt != nil {
		attempt.Receipt.Status, attempt.Receipt.started = "not_dispatched", false
		attempt.Receipt.ErrorCode, attempt.Receipt.Error = response.Error.Code, response.Error.Message
	}
	if err := s.recovery.SaveAttempt(attempt); err != nil {
		s.recoveryError = err
		return err
	}
	if err := s.saveScopeLocked(s.documents[attempt.ScopeKey]); err != nil {
		s.recoveryError = err
		return err
	}
	delete(s.attempts, id)
	return nil
}

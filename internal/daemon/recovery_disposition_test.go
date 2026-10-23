package daemon

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func dispositionStoreFixture(t *testing.T) (*recoveryStore, string, *recoveryScope, *recoveryDisposition) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	s, err := openRecoveryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := protocol.Context{ProjectUUID: "project", DocumentUUID: "document"}
	oldScope := &recoveryScope{Key: runtimeScopeKey("old", ctx), Document: &runtimeDocument{WindowID: "old", Context: ctx, ConnectionEpoch: "old-epoch", ChangesSinceSave: true, Task: &runtimeTask{ID: "old-task", Revision: 2}}}
	if err = s.SaveScope(oldScope); err != nil {
		t.Fatal(err)
	}
	a := &recoveryAttempt{RequestID: "unknown-write", Request: protocol.Request{Envelope: protocol.Envelope{ID: "unknown-write", WindowID: "old"}, Action: "debug.exec_js", Payload: map[string]any{"script": "original script"}}, ScopeKey: oldScope.Key, WindowID: "old", Context: ctx, ConnectionEpoch: "old-epoch", TaskID: "old-task", TaskRevision: 2, State: recoveryAttemptUnknown, ContentWrite: true, EvidenceUnknown: true}
	if err = s.SaveAttempt(a); err != nil {
		t.Fatal(err)
	}
	_, hash, err := s.LoadAttemptEvidence(a.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	scope := &recoveryScope{Key: runtimeScopeKey("current", ctx), Document: &runtimeDocument{WindowID: "current", Context: ctx, ConnectionEpoch: "new-epoch", Generation: 7, SceneInstance: "current-scene", Task: &runtimeTask{ID: "old-task", Revision: 2}}}
	if err = s.SaveScope(scope); err != nil {
		t.Fatal(err)
	}
	scope.Document.Task = &runtimeTask{ID: "new-task", Revision: 3, Goal: "user-approved next work"}
	terminal := []byte(`{"requestId":"terminal-read","action":"debug.control","payload":{"operation":"status","requestId":"unknown-write"},"ok":true,"result":{"requests":[{"id":"unknown-write","settled":true,"state":"settled_after_cancel"}]}}`)
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err = recoveryEvidence(tx, "legacy-audit", "terminal-read", terminal, "original.jsonl"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	d := &recoveryDisposition{ScopeKey: scope.Key, ScopeRevision: scope.Revision, WindowID: "current", Context: ctx, ConnectionEpoch: "new-epoch", Generation: 7, CurrentSceneInstance: "current-scene", TaskID: "new-task", TaskRevision: 3, Basis: map[string]any{"components": []any{}, "nativeGeneration": 7}, Reason: "user accepts observed baseline after ending the old native session", Attempts: []recoveryDispositionAttempt{{RequestID: a.RequestID, Revision: a.Revision, SHA256: hash, ScopeKey: a.ScopeKey, WindowID: a.WindowID}}, UnsavedScopes: []recoveryDispositionScope{{Key: oldScope.Key, Revision: oldScope.Revision, ConnectionEpoch: "old-epoch"}}, SourceEvidence: []recoveryDispositionEvidence{{Kind: "legacy-audit", RequestID: "terminal-read", SHA256: recoveryHash(terminal)}}, Evidence: map[string]any{"nativeResult": "read from retained status", "termination": "explicit user attestation"}}
	return s, path, scope, d
}

func TestRecoveryDispositionAtomicAcceptancePreservesUnknownAndReopens(t *testing.T) {
	s, path, scope, d := dispositionStoreFixture(t)
	beforeAttempt, beforeHash, err := s.LoadAttemptEvidence("unknown-write")
	if err != nil {
		t.Fatal(err)
	}
	var original []byte
	if err := s.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id='unknown-write'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveDisposition(scope, d); err != nil {
		t.Fatal(err)
	}
	if scope.Revision != 2 || d.Version != 1 || d.AcceptedAt.IsZero() || d.BasisSHA256 == "" {
		t.Fatalf("acceptance not committed: %#v, revision %d", d, scope.Revision)
	}
	var current []byte
	if err := s.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id='unknown-write'`).Scan(&current); err != nil || !bytes.Equal(original, current) {
		t.Fatalf("original attempt rewritten: %v", err)
	}
	afterAttempt, afterHash, err := s.LoadAttemptEvidence("unknown-write")
	if err != nil || beforeAttempt.Revision != afterAttempt.Revision || beforeHash != afterHash {
		t.Fatalf("original attempt digest or revision changed: %v", err)
	}
	if err := s.SaveDisposition(scope, d); err != nil {
		t.Fatalf("identical acceptance was not idempotent: %v", err)
	}
	changed := *d
	changed.Reason = "different acceptance must not overwrite the first"
	if err := s.SaveDisposition(scope, &changed); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("conflicting acceptance: %v", err)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM recovery_evidence WHERE kind='manual-acceptance'`).Scan(&count); err != nil || count != 1 || scope.Revision != 2 {
		t.Fatalf("duplicate changed evidence or scope: %d %d %v", count, scope.Revision, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openRecoveryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	loaded, err := reopened.LoadDisposition(scope.Key, d.TaskID, d.TaskRevision)
	if err != nil || loaded == nil || loaded.Reason != d.Reason || loaded.BasisSHA256 != d.BasisSHA256 {
		t.Fatalf("lost committed acceptance: %#v %v", loaded, err)
	}
	for _, query := range []struct {
		scope string
		task  string
		rev   uint64
	}{{"wrong-scope", d.TaskID, d.TaskRevision}, {scope.Key, "old-task", d.TaskRevision}, {scope.Key, d.TaskID, d.TaskRevision + 1}} {
		if got, e := reopened.LoadDisposition(query.scope, query.task, query.rev); e != nil || got != nil {
			t.Fatalf("acceptance escaped exact task identity: %#v %v", got, e)
		}
	}
	unresolved, err := reopened.ListUnresolved()
	if err != nil || len(unresolved) != 1 || unresolved[0].State != recoveryAttemptUnknown {
		t.Fatalf("old unknown was settled: %#v %v", unresolved, err)
	}
	old, err := reopened.LoadAttempt("unknown-write")
	if err != nil {
		t.Fatal(err)
	}
	old.Revision = 0
	if err = reopened.SaveAttempt(old); !errors.Is(err, ErrRecoveryDuplicate) {
		t.Fatalf("old request lost permanent dedup: %v", err)
	}
	retained, err := reopened.LoadScope(d.UnsavedScopes[0].Key)
	if err != nil || !retained.Document.ChangesSinceSave {
		t.Fatalf("old unsaved ownership was cleared: %#v %v", retained, err)
	}
}

func TestRecoveryDispositionAttemptScopeAndState(t *testing.T) {
	for _, test := range []struct {
		name    string
		project string
		state   recoveryAttemptState
		valid   bool
	}{
		{"historical-unscoped-unknown", "", recoveryAttemptUnknown, true},
		{"different-project", "another-project", recoveryAttemptUnknown, false},
		{"live-pending", "project", recoveryAttemptPending, false},
		{"live-dispatched", "project", recoveryAttemptDispatched, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, scope, d := dispositionStoreFixture(t)
			a, err := s.LoadAttempt("unknown-write")
			if err != nil {
				t.Fatal(err)
			}
			a.Context.ProjectUUID, a.State = test.project, test.state
			a.ScopeKey = runtimeScopeKey(a.WindowID, a.Context)
			raw, metadata, err := recoveryAttemptBytes(a)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`UPDATE recovery_attempts SET payload=?,hash=?,state=?,metadata=?,metadata_hash=? WHERE request_id=?`, raw, recoveryHash(raw), a.State, metadata, recoveryHash(metadata), a.RequestID); err != nil {
				t.Fatal(err)
			}
			d.Attempts[0].ScopeKey, d.Attempts[0].SHA256 = a.ScopeKey, recoveryHash(raw)
			err = s.SaveDisposition(scope, d)
			if (err == nil) != test.valid {
				t.Fatalf("acceptance validity should be %v: %v", test.valid, err)
			}
		})
	}
}

func TestRecoveryDispositionFailureRollsBackScopeAndAcceptance(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, *recoveryStore, *recoveryScope, *recoveryDisposition)
	}{
		{"stale-scope", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			stale, err := s.LoadScope(scope.Key)
			if err != nil || s.SaveScope(stale) != nil {
				t.Fatal("could not advance scope", err)
			}
		}},
		{"stale-attempt", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.Attempts[0].Revision++
		}},
		{"wrong-attempt-hash", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.Attempts[0].SHA256 = recoveryHash([]byte("wrong"))
		}},
		{"duplicate-attempt-id", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.Attempts = append(d.Attempts, d.Attempts[0])
		}},
		{"duplicate-source-id", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.SourceEvidence = append(d.SourceEvidence, d.SourceEvidence[0])
		}},
		{"missing-source", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.SourceEvidence[0].SHA256 = recoveryHash([]byte("missing"))
		}},
		{"wrong-epoch", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.ConnectionEpoch = "wrong"
		}},
		{"wrong-basis-hash", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			d.BasisSHA256 = recoveryHash([]byte("wrong"))
		}},
		{"missing-user-reason", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) { d.Reason = " " }},
		{"insert-fails", func(t *testing.T, s *recoveryStore, scope *recoveryScope, d *recoveryDisposition) {
			if _, err := s.db.Exec(`CREATE TRIGGER reject_acceptance BEFORE INSERT ON recovery_evidence WHEN NEW.kind='manual-acceptance' BEGIN SELECT RAISE(ABORT,'injected acceptance failure'); END`); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, scope, d := dispositionStoreFixture(t)
			test.mutate(t, s, scope, d)
			before, err := s.LoadScope(scope.Key)
			if err != nil {
				t.Fatal(err)
			}
			if err = s.SaveDisposition(scope, d); err == nil {
				t.Fatal("invalid acceptance committed")
			}
			after, err := s.LoadScope(scope.Key)
			if err != nil || after.Revision != before.Revision || after.Document.Task.ID != before.Document.Task.ID || scope.Revision != 1 || !d.AcceptedAt.IsZero() {
				t.Fatalf("failed transaction leaked scope or receipt: %#v %v", after, err)
			}
			var count int
			if err = s.db.QueryRow(`SELECT COUNT(*) FROM recovery_evidence WHERE kind='manual-acceptance'`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed transaction leaked acceptance: %d %v", count, err)
			}
		})
	}
}

func TestRecoveryDispositionRevalidatesRetainedEvidence(t *testing.T) {
	for _, test := range []struct {
		name string
		sql  string
	}{
		{"changed-attempt", `UPDATE recovery_attempts SET payload='broken' WHERE request_id='unknown-write'`},
		{"missing-attempt", `DELETE FROM recovery_attempts WHERE request_id='unknown-write'`},
		{"changed-status-evidence", `UPDATE recovery_evidence SET raw='broken' WHERE kind='legacy-audit'`},
		{"missing-status-evidence", `DELETE FROM recovery_evidence WHERE kind='legacy-audit'`},
		{"changed-unsaved-owner", `UPDATE recovery_scopes SET revision=revision+1 WHERE key LIKE 'old|%'`},
		{"changed-acceptance", `UPDATE recovery_evidence SET raw='broken' WHERE kind='manual-acceptance'`},
		{"duplicate-acceptance-identity", `INSERT INTO recovery_evidence(kind,record_id,hash,raw,source) SELECT kind,record_id,'different-hash',raw,source FROM recovery_evidence WHERE kind='manual-acceptance'`},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _, scope, d := dispositionStoreFixture(t)
			if err := s.SaveDisposition(scope, d); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			if _, err := s.LoadDisposition(scope.Key, d.TaskID, d.TaskRevision); err == nil {
				t.Fatal("changed or missing acceptance evidence was trusted")
			}
		})
	}
}

func TestRecoveryDispositionLegacyAuditIsHashBoundReadOnly(t *testing.T) {
	s, _, _, d := dispositionStoreFixture(t)
	ref := d.SourceEvidence[0]
	raw, err := s.LoadLegacyAudit(ref.RequestID, ref.SHA256)
	if err != nil || recoveryHash(raw) != ref.SHA256 || !json.Valid(raw) {
		t.Fatalf("raw audit evidence unavailable: %s %v", raw, err)
	}
	if _, err = s.LoadLegacyAudit("another-request", ref.SHA256); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("audit evidence escaped its original identity: %v", err)
	}
	if _, err = s.LoadLegacyAudit(ref.RequestID, "bad"); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("malformed evidence digest accepted: %v", err)
	}
}

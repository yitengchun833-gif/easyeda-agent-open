package daemon

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

type recoveryDispositionAttempt struct {
	RequestID string `json:"requestId"`
	Revision  uint64 `json:"revision"`
	SHA256    string `json:"sha256"`
	ScopeKey  string `json:"scopeKey"`
	WindowID  string `json:"windowId"`
}

type recoveryDispositionScope struct {
	Key             string `json:"key"`
	Revision        uint64 `json:"revision"`
	ConnectionEpoch string `json:"connectionEpoch"`
}

type recoveryDispositionEvidence struct {
	Kind      string `json:"kind"`
	RequestID string `json:"requestId"`
	SHA256    string `json:"sha256"`
}

// Acceptance records a user's new intent; it never settles an old attempt.
type recoveryDisposition struct {
	Version              int                           `json:"version"`
	ScopeKey             string                        `json:"scopeKey"`
	ScopeRevision        uint64                        `json:"scopeRevision"`
	WindowID             string                        `json:"windowId"`
	Context              protocol.Context              `json:"context"`
	ConnectionEpoch      string                        `json:"connectionEpoch"`
	Generation           uint64                        `json:"generation"`
	CurrentSceneInstance string                        `json:"currentSceneInstance"`
	TaskID               string                        `json:"taskId"`
	TaskRevision         uint64                        `json:"taskRevision"`
	Basis                any                           `json:"basis"`
	BasisSHA256          string                        `json:"basisSha256"`
	Reason               string                        `json:"reason"`
	Attempts             []recoveryDispositionAttempt  `json:"attempts"`
	UnsavedScopes        []recoveryDispositionScope    `json:"unsavedScopes,omitempty"`
	SourceEvidence       []recoveryDispositionEvidence `json:"sourceEvidence"`
	Evidence             any                           `json:"evidence"`
	AcceptedAt           time.Time                     `json:"acceptedAt"`
}

type recoveryDispositionReader interface {
	QueryRow(string, ...any) *sql.Row
}

func recoveryDispositionID(scopeKey, taskID string) string {
	raw, _ := json.Marshal([]string{scopeKey, taskID})
	return recoveryHash(raw)
}

func recoveryReadAttempt(reader recoveryDispositionReader, id string) (*recoveryAttempt, string, error) {
	var raw, metadata []byte
	var hash, state, metadataHash string
	var revision uint64
	err := reader.QueryRow(`SELECT payload,hash,revision,state,metadata,metadata_hash FROM recovery_attempts WHERE request_id=?`, id).Scan(&raw, &hash, &revision, &state, &metadata, &metadataHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var record recoveryAttemptRecord
	var meta recoveryAttempt
	if recoveryHash(raw) != hash || recoveryHash(metadata) != metadataHash || json.Unmarshal(raw, &record) != nil || json.Unmarshal(metadata, &meta) != nil || record.Attempt == nil || record.Attempt.RequestID != id || record.Attempt.Revision != revision || string(record.Attempt.State) != state || meta.RequestID != id || meta.Revision != revision || string(meta.State) != state {
		return nil, "", ErrRecoveryCorrupt
	}
	_, expectedMetadata, err := recoveryAttemptBytes(record.Attempt)
	if err != nil || !bytes.Equal(metadata, expectedMetadata) {
		return nil, "", ErrRecoveryCorrupt
	}
	if record.Attempt.Receipt != nil {
		record.Attempt.Receipt.ObservedFacts = record.Facts
	}
	return record.Attempt, hash, nil
}

func (s *recoveryStore) LoadAttemptEvidence(id string) (*recoveryAttempt, string, error) {
	return recoveryReadAttempt(s.db, id)
}

func recoveryReadLegacyAudit(reader recoveryDispositionReader, id, hash string) (json.RawMessage, error) {
	var raw []byte
	if id == "" || len(hash) != 64 {
		return nil, ErrRecoveryCorrupt
	}
	err := reader.QueryRow(`SELECT raw FROM recovery_evidence WHERE kind='legacy-audit' AND record_id=? AND hash=?`, id, hash).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrRecoveryCorrupt
	}
	if err != nil {
		return nil, err
	}
	var entry auditEntry
	if recoveryHash(raw) != hash || json.Unmarshal(raw, &entry) != nil || entry.RequestID != id {
		return nil, ErrRecoveryCorrupt
	}
	return json.RawMessage(raw), nil
}

func (s *recoveryStore) LoadLegacyAudit(id, hash string) (json.RawMessage, error) {
	return recoveryReadLegacyAudit(s.db, id, hash)
}

func recoveryReadDispositionScope(reader recoveryDispositionReader, key string) (*recoveryScope, error) {
	var raw, index []byte
	var hash, indexHash string
	var revision uint64
	err := reader.QueryRow(`SELECT payload,hash,revision,index_payload,index_hash FROM recovery_scopes WHERE key=?`, key).Scan(&raw, &hash, &revision, &index, &indexHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var record recoveryScopeRecord
	var entry recoveryIndexEntry
	if recoveryHash(raw) != hash || recoveryHash(index) != indexHash || json.Unmarshal(raw, &record) != nil || json.Unmarshal(index, &entry) != nil || record.Scope == nil || record.Scope.Document == nil || record.Scope.Key != key || record.Scope.Revision != revision || entry.Key != key || entry.Revision != revision {
		return nil, ErrRecoveryCorrupt
	}
	expectedIndex, err := json.Marshal(recoveryIndex(record.Scope))
	if err != nil || !bytes.Equal(expectedIndex, index) {
		return nil, ErrRecoveryCorrupt
	}
	return record.Scope, nil
}

func recoveryValidateDisposition(reader recoveryDispositionReader, d *recoveryDisposition) error {
	if d.Version != 1 || d.ScopeKey != runtimeScopeKey(d.WindowID, d.Context) || d.WindowID == "" || d.Context.ProjectUUID == "" || d.Context.DocumentUUID == "" || d.ConnectionEpoch == "" || d.CurrentSceneInstance == "" || d.TaskID == "" || d.TaskRevision == 0 || strings.TrimSpace(d.Reason) == "" || d.AcceptedAt.IsZero() || len(d.Attempts) == 0 && len(d.UnsavedScopes) == 0 || len(d.SourceEvidence) == 0 || d.Evidence == nil {
		return ErrRecoveryCorrupt
	}
	basis, err := json.Marshal(d.Basis)
	if err != nil || d.Basis == nil || bytes.Equal(basis, []byte("null")) || recoveryHash(basis) != d.BasisSHA256 {
		return ErrRecoveryCorrupt
	}
	seen := map[string]bool{}
	for _, ref := range d.Attempts {
		if ref.RequestID == "" || ref.Revision == 0 || len(ref.SHA256) != 64 || seen[ref.RequestID] {
			return ErrRecoveryCorrupt
		}
		seen[ref.RequestID] = true
		attempt, hash, err := recoveryReadAttempt(reader, ref.RequestID)
		if err != nil {
			return err
		}
		if attempt == nil {
			return ErrRecoveryCorrupt
		}
		if attempt.Revision != ref.Revision || hash != ref.SHA256 {
			return ErrRecoveryConflict
		}
		if attempt.ScopeKey != ref.ScopeKey || ref.WindowID == "" || attempt.WindowID != ref.WindowID || attempt.Context.ProjectUUID != "" && attempt.Context.ProjectUUID != d.Context.ProjectUUID || attempt.TaskID == d.TaskID || attempt.State != recoveryAttemptUnknown {
			return ErrRecoveryCorrupt
		}
	}
	seen = map[string]bool{}
	for _, ref := range d.UnsavedScopes {
		if ref.Key == "" || ref.Key == d.ScopeKey || ref.Revision == 0 || seen[ref.Key] {
			return ErrRecoveryCorrupt
		}
		seen[ref.Key] = true
		scope, err := recoveryReadDispositionScope(reader, ref.Key)
		if err != nil {
			return err
		}
		if scope == nil {
			return ErrRecoveryCorrupt
		}
		if scope.Revision != ref.Revision || scope.Document.ConnectionEpoch != ref.ConnectionEpoch {
			return ErrRecoveryConflict
		}
		if scope.Document.Context.ProjectUUID != d.Context.ProjectUUID || scope.Document.Context.DocumentUUID != d.Context.DocumentUUID || !scope.Document.ChangesSinceSave {
			return ErrRecoveryCorrupt
		}
	}
	seen = map[string]bool{}
	for _, ref := range d.SourceEvidence {
		if ref.Kind != "legacy-audit" || seen[ref.RequestID] {
			return ErrRecoveryCorrupt
		}
		seen[ref.RequestID] = true
		if _, err := recoveryReadLegacyAudit(reader, ref.RequestID, ref.SHA256); err != nil {
			return err
		}
	}
	return nil
}

func recoveryReadDisposition(reader recoveryDispositionReader, scopeKey, taskID string) (*recoveryDisposition, error) {
	row := reader.QueryRow(`SELECT COUNT(*),MIN(raw),MIN(hash),MIN(source) FROM recovery_evidence WHERE kind='manual-acceptance' AND record_id=?`, recoveryDispositionID(scopeKey, taskID))
	var count int
	var raw []byte
	var hash, source sql.NullString
	if err := row.Scan(&count, &raw, &hash, &source); err != nil {
		return nil, err
	}
	if count == 0 {
		return nil, nil
	}
	var d recoveryDisposition
	if count != 1 || source.String != "user-acceptance/v1" || recoveryHash(raw) != hash.String || json.Unmarshal(raw, &d) != nil || d.ScopeKey != scopeKey || d.TaskID != taskID {
		return nil, ErrRecoveryCorrupt
	}
	if err := recoveryValidateDisposition(reader, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *recoveryStore) LoadDisposition(scopeKey, taskID string, taskRevision uint64) (*recoveryDisposition, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	d, err := recoveryReadDisposition(tx, scopeKey, taskID)
	if err != nil || d == nil {
		return d, err
	}
	if d.TaskRevision != taskRevision {
		return nil, nil
	}
	return d, nil
}

func (s *recoveryStore) SaveDisposition(scope *recoveryScope, acceptance *recoveryDisposition) error {
	if scope == nil || scope.Document == nil || scope.Document.Task == nil || acceptance == nil {
		return fmt.Errorf("missing recovery acceptance scope or task")
	}
	d := *acceptance
	if d.Version == 0 {
		d.Version = 1
	}
	basis, err := json.Marshal(d.Basis)
	if err != nil {
		return err
	}
	if d.BasisSHA256 == "" {
		d.BasisSHA256 = recoveryHash(basis)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	previous, err := recoveryReadDisposition(tx, d.ScopeKey, d.TaskID)
	if err != nil {
		return err
	}
	if d.AcceptedAt.IsZero() {
		d.AcceptedAt = time.Now().UTC()
		if previous != nil {
			d.AcceptedAt = previous.AcceptedAt
		}
	}
	if err = recoveryValidateDisposition(tx, &d); err != nil {
		return err
	}
	doc := scope.Document
	if d.ScopeKey != scope.Key || d.WindowID != doc.WindowID || d.Context != doc.Context || d.ConnectionEpoch != doc.ConnectionEpoch || d.Generation != doc.Generation || d.CurrentSceneInstance != doc.SceneInstance || d.TaskID != doc.Task.ID || d.TaskRevision != doc.Task.Revision || doc.Restored {
		return ErrRecoveryConflict
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	if previous != nil {
		old, err := json.Marshal(previous)
		if err != nil || !bytes.Equal(old, raw) {
			return ErrRecoveryConflict
		}
		*acceptance = d
		return nil
	}
	if d.ScopeRevision != scope.Revision {
		return ErrRecoveryConflict
	}
	current, err := recoveryReadDispositionScope(tx, scope.Key)
	if err != nil {
		return err
	}
	if current == nil && scope.Revision != 0 || current != nil && (current.Revision != scope.Revision || current.Document.Task != nil && current.Document.Task.ID == d.TaskID) {
		return ErrRecoveryConflict
	}
	copy := *scope
	if err = recoverySaveScope(tx, &copy); err != nil {
		return err
	}
	if err = recoveryEvidence(tx, "manual-acceptance", recoveryDispositionID(d.ScopeKey, d.TaskID), raw, "user-acceptance/v1"); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	scope.Revision = copy.Revision
	*acceptance = d
	return nil
}

package daemon

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
	_ "modernc.org/sqlite"
)

type recoveryStore struct{ db *sql.DB }

type recoveryScopeRecord struct {
	Scope *recoveryScope `json:"scope"`
	Facts map[string]any `json:"facts,omitempty"`
}

type recoveryAttemptRecord struct {
	Attempt *recoveryAttempt `json:"attempt"`
	Facts   any              `json:"facts,omitempty"`
}

func recoveryHash(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }

func openRecoveryStore(path string) (*recoveryStore, error) {
	if path == "" {
		return nil, fmt.Errorf("recovery database path is empty")
	}
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
	}
	dsn := path
	if path != ":memory:" {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		uriPath := filepath.ToSlash(absolute)
		if filepath.VolumeName(absolute) != "" && uriPath[0] != '/' {
			uriPath = "/" + uriPath
		}
		dsn = (&url.URL{Scheme: "file", Path: uriPath}).String()
	}
	// Reopened pooled connections must retain the same durability settings.
	db, err := sql.Open("sqlite", dsn+"?_busy_timeout=3000&_journal_mode=DELETE&_synchronous=EXTRA")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &recoveryStore{db: db}
	fail := func(err error) (*recoveryStore, error) { _ = db.Close(); return nil, err }
	var version int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return fail(err)
	}
	if version != 0 && version != 1 {
		return fail(ErrRecoveryCorrupt)
	}
	if version == 1 {
		var tables int
		if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('recovery_meta','recovery_scopes','recovery_attempts','recovery_evidence')`).Scan(&tables); err != nil || tables != 4 {
			return fail(ErrRecoveryCorrupt)
		}
	}
	tx, err := db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS recovery_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recovery_scopes (key TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, hash TEXT NOT NULL, index_payload BLOB NOT NULL, index_hash TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS recovery_attempts (request_id TEXT PRIMARY KEY, revision INTEGER NOT NULL, payload BLOB NOT NULL, hash TEXT NOT NULL, state TEXT NOT NULL, metadata BLOB NOT NULL, metadata_hash TEXT NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS recovery_attempt_states ON recovery_attempts(state)`,
		`CREATE TABLE IF NOT EXISTS recovery_evidence (kind TEXT NOT NULL, record_id TEXT NOT NULL, hash TEXT NOT NULL, raw BLOB NOT NULL, source TEXT NOT NULL, PRIMARY KEY(kind,record_id,hash))`,
		`PRAGMA user_version=1`,
	} {
		if _, err = tx.Exec(statement); err != nil {
			return fail(err)
		}
	}
	var identity string
	err = tx.QueryRow(`SELECT value FROM recovery_meta WHERE key='identity'`).Scan(&identity)
	if errors.Is(err, sql.ErrNoRows) {
		if version != 0 {
			return fail(ErrRecoveryCorrupt)
		}
		var random [16]byte
		if _, err = rand.Read(random[:]); err != nil {
			return fail(err)
		}
		identity = hex.EncodeToString(random[:])
		_, err = tx.Exec(`INSERT INTO recovery_meta(key,value) VALUES('identity',?)`, identity)
	}
	if err != nil {
		return fail(err)
	}
	if len(identity) != 32 {
		return fail(ErrRecoveryCorrupt)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	if path != ":memory:" {
		if err = os.Chmod(path, 0o600); err != nil {
			return fail(err)
		}
	}
	return s, nil
}

func (s *recoveryStore) Close() error { return s.db.Close() }
func (s *recoveryStore) Identity() (string, error) {
	var identity string
	err := s.db.QueryRow(`SELECT value FROM recovery_meta WHERE key='identity'`).Scan(&identity)
	return identity, err
}

func recoveryIndex(scope *recoveryScope) recoveryIndexEntry {
	d := scope.Document
	entry := recoveryIndexEntry{Key: scope.Key, Revision: scope.Revision, WindowID: d.WindowID, Context: d.Context, Task: d.Task, Generation: d.Generation, ChangesSinceSave: d.ChangesSinceSave, SceneInstance: d.SceneInstance, ConnectionEpoch: d.ConnectionEpoch, ObjectCount: len(d.Objects), EvidenceUnknown: scope.EvidenceUnknown}
	if entry.Task != nil && entry.Task.ID == "" {
		task := *entry.Task
		task.ID = legacyRuntimeTaskID(scope.Key, task.Revision)
		entry.Task = &task
	}
	return entry
}

func recoveryScopeBytes(scope *recoveryScope) ([]byte, []byte, error) {
	facts := map[string]any{}
	for _, receipt := range scope.Receipts {
		if receipt != nil && receipt.ObservedFacts != nil {
			facts[receipt.RequestID] = receipt.ObservedFacts
		}
	}
	raw, err := json.Marshal(recoveryScopeRecord{scope, facts})
	if err != nil {
		return nil, nil, err
	}
	index, err := json.Marshal(recoveryIndex(scope))
	return raw, index, err
}

func recoveryEvidence(tx *sql.Tx, kind, id string, raw []byte, source string) error {
	_, err := tx.Exec(`INSERT OR IGNORE INTO recovery_evidence(kind,record_id,hash,raw,source) VALUES(?,?,?,?,?)`, kind, id, recoveryHash(raw), raw, source)
	return err
}

func (s *recoveryStore) SaveScope(scope *recoveryScope) error {
	if scope == nil {
		return fmt.Errorf("missing recovery scope")
	}
	copy := *scope
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = recoverySaveScope(tx, &copy); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	scope.Revision = copy.Revision
	return nil
}

func recoverySaveScope(tx *sql.Tx, scope *recoveryScope) error {
	if scope == nil || scope.Document == nil || scope.Key == "" || scope.Document.WindowID == "" || scope.Document.Context.ProjectUUID == "" || scope.Document.Context.DocumentUUID == "" || scope.Key != scope.Document.WindowID+"|"+scope.Document.Context.ProjectUUID+"|"+scope.Document.Context.DocumentUUID {
		return fmt.Errorf("invalid recovery scope identity")
	}
	copy := *scope
	copy.Revision++
	raw, index, err := recoveryScopeBytes(&copy)
	if err != nil {
		return err
	}
	if scope.Revision == 0 {
		result, e := tx.Exec(`INSERT OR IGNORE INTO recovery_scopes(key,revision,payload,hash,index_payload,index_hash) VALUES(?,?,?,?,?,?)`, scope.Key, copy.Revision, raw, recoveryHash(raw), index, recoveryHash(index))
		if e != nil {
			return e
		}
		rows, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if rows != 1 {
			return ErrRecoveryConflict
		}
	} else {
		result, e := tx.Exec(`UPDATE recovery_scopes SET revision=?,payload=?,hash=?,index_payload=?,index_hash=? WHERE key=? AND revision=?`, copy.Revision, raw, recoveryHash(raw), index, recoveryHash(index), scope.Key, scope.Revision)
		if e != nil {
			return e
		}
		rows, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if rows != 1 {
			return ErrRecoveryConflict
		}
	}
	if err = recoveryEvidence(tx, "scope", scope.Key, raw, "runtime"); err != nil {
		return err
	}
	// Evicting a cache entry must never release an already used request ID.
	for _, receipt := range scope.Receipts {
		if receipt == nil || receipt.RequestID == "" {
			return fmt.Errorf("invalid recovery receipt")
		}
		attempt := recoveryAttempt{RequestID: receipt.RequestID, ScopeKey: scope.Key, Revision: 1, Receipt: receipt, State: recoveryAttemptUnknown, WindowID: receipt.WindowID, Context: scope.Document.Context, ContentWrite: receipt.ContentWrite, EvidenceUnknown: true, UpdatedAt: time.Now().UTC()}
		attempt.Request.ID, attempt.Request.Action, attempt.Request.WindowID = receipt.RequestID, receipt.Action, receipt.WindowID
		if request, ok := scope.Requests[receipt.RequestID]; ok {
			attempt.Request = request
			attempt.Baseline = scope.Baselines[receipt.RequestID]
		}
		recoveryClassifyAttempt(&attempt)
		if !attempt.Dangerous || receipt.Status == "action_finished" || receipt.Status == "needs_review" || receipt.Status == "saved" || receipt.Status == "paused" {
			attempt.State = recoveryAttemptSettled
		}
		if receipt.Status == "not_dispatched" {
			attempt.State, attempt.ExecutionKnown, attempt.Executed = recoveryAttemptNotDispatched, true, false
		}
		if err = recoveryInsertAttempt(tx, &attempt, true); err != nil {
			return err
		}
	}
	scope.Revision = copy.Revision
	return nil
}

func (s *recoveryStore) LoadScope(key string) (*recoveryScope, error) {
	var raw []byte
	var hash string
	var revision uint64
	if err := s.db.QueryRow(`SELECT payload,hash,revision FROM recovery_scopes WHERE key=?`, key).Scan(&raw, &hash, &revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	var record recoveryScopeRecord
	if recoveryHash(raw) != hash || json.Unmarshal(raw, &record) != nil || record.Scope == nil || record.Scope.Document == nil || record.Scope.Key != key || record.Scope.Revision != revision {
		return nil, ErrRecoveryCorrupt
	}
	for _, receipt := range record.Scope.Receipts {
		if receipt != nil {
			receipt.ObservedFacts = record.Facts[receipt.RequestID]
		}
	}
	d := record.Scope.Document
	if d.Task != nil && d.Task.ID == "" {
		d.Task.ID = legacyRuntimeTaskID(key, d.Task.Revision)
	}
	return record.Scope, nil
}

func (s *recoveryStore) ListIndex() ([]recoveryIndexEntry, error) {
	rows, err := s.db.Query(`SELECT key,revision,index_payload,index_hash FROM recovery_scopes ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := []recoveryIndexEntry{}
	for rows.Next() {
		var key, hash string
		var revision uint64
		var raw []byte
		var entry recoveryIndexEntry
		if err = rows.Scan(&key, &revision, &raw, &hash); err != nil {
			return nil, err
		}
		if recoveryHash(raw) != hash || json.Unmarshal(raw, &entry) != nil || entry.Key != key || entry.Revision != revision {
			return nil, ErrRecoveryCorrupt
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func (s *recoveryStore) HasRequest(id string) (bool, error) {
	var exists bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM recovery_attempts WHERE request_id=?)`, id).Scan(&exists)
	return exists, err
}

func recoveryAttemptBytes(attempt *recoveryAttempt) ([]byte, []byte, error) {
	var facts any
	if attempt.Receipt != nil {
		facts = attempt.Receipt.ObservedFacts
	}
	raw, err := json.Marshal(recoveryAttemptRecord{attempt, facts})
	if err != nil {
		return nil, nil, err
	}
	meta := *attempt
	meta.Request = protocolRequestMetadata(attempt.Request)
	meta.Baseline = nil
	meta.ContinuationBaseline = nil
	meta.Evidence = nil
	if meta.Receipt != nil {
		r := *meta.Receipt
		r.Checks = nil
		r.Progress = nil
		r.ObservedFacts = nil
		meta.Receipt = &r
	}
	metadata, err := json.Marshal(meta)
	return raw, metadata, err
}

func protocolRequestMetadata(request protocol.Request) protocol.Request {
	request.Payload = nil
	request.OutputDir = ""
	request.ClientID = ""
	request.ForceReason = ""
	return request
}

func recoveryClassifyAttempt(attempt *recoveryAttempt) {
	attempt.Dangerous = runtimeDangerous(attempt.Request) || attempt.ContentWrite
	attempt.ProjectWide = runtimeProjectWide(attempt.Request)
	if attempt.EvidenceUnknown && attempt.Request.Action == "debug.batch" && len(runtimeArray(attempt.Request.Payload["steps"])) == 0 {
		attempt.Dangerous, attempt.ProjectWide = true, true
	}
}

func recoveryTransitionAllowed(before, after recoveryAttemptState) bool {
	if before == after {
		return true
	}
	switch before {
	case recoveryAttemptPending:
		return after == recoveryAttemptDispatched || after == recoveryAttemptNotDispatched || after == recoveryAttemptUnknown
	case recoveryAttemptDispatched:
		return after == recoveryAttemptSettled || after == recoveryAttemptUnknown
	}
	return false
}

func recoveryInsertAttempt(tx *sql.Tx, attempt *recoveryAttempt, ignore bool) error {
	raw, meta, err := recoveryAttemptBytes(attempt)
	if err != nil {
		return err
	}
	clause := ""
	if ignore {
		clause = " OR IGNORE"
	}
	result, err := tx.Exec(`INSERT`+clause+` INTO recovery_attempts(request_id,revision,payload,hash,state,metadata,metadata_hash) VALUES(?,?,?,?,?,?,?)`, attempt.RequestID, attempt.Revision, raw, recoveryHash(raw), attempt.State, meta, recoveryHash(meta))
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 && !ignore {
		return ErrRecoveryDuplicate
	}
	if n != 0 {
		return recoveryEvidence(tx, "attempt", attempt.RequestID, raw, "runtime")
	}
	return nil
}

func (s *recoveryStore) SaveAttempt(attempt *recoveryAttempt) error {
	if attempt == nil {
		return fmt.Errorf("missing recovery attempt")
	}
	copy := *attempt
	if copy.RequestID == "" {
		copy.RequestID = copy.Request.ID
	}
	if copy.RequestID == "" || copy.Request.ID != "" && copy.Request.ID != copy.RequestID {
		return fmt.Errorf("invalid recovery request identity")
	}
	switch copy.State {
	case recoveryAttemptPending, recoveryAttemptDispatched, recoveryAttemptSettled, recoveryAttemptUnknown, recoveryAttemptNotDispatched:
	default:
		return fmt.Errorf("invalid recovery attempt state")
	}
	copy.Revision++
	copy.UpdatedAt = time.Now().UTC()
	recoveryClassifyAttempt(&copy)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if attempt.Revision == 0 {
		var exists bool
		if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM recovery_attempts WHERE request_id=?)`, copy.RequestID).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return ErrRecoveryDuplicate
		}
		if err = recoveryInsertAttempt(tx, &copy, false); err != nil {
			return err
		}
	} else {
		var oldRaw []byte
		var oldHash string
		var revision uint64
		if err = tx.QueryRow(`SELECT payload,hash,revision FROM recovery_attempts WHERE request_id=?`, copy.RequestID).Scan(&oldRaw, &oldHash, &revision); err != nil {
			return err
		}
		if revision != attempt.Revision {
			return ErrRecoveryConflict
		}
		var old recoveryAttemptRecord
		if recoveryHash(oldRaw) != oldHash || json.Unmarshal(oldRaw, &old) != nil || old.Attempt == nil {
			return ErrRecoveryCorrupt
		}
		if !recoveryTransitionAllowed(old.Attempt.State, copy.State) {
			return fmt.Errorf("invalid recovery transition %s to %s", old.Attempt.State, copy.State)
		}
		if old.Attempt.ScopeKey != "" && copy.ScopeKey != "" && old.Attempt.ScopeKey != copy.ScopeKey || old.Attempt.Context.ProjectUUID != "" && copy.Context.ProjectUUID != "" && old.Attempt.Context.ProjectUUID != copy.Context.ProjectUUID || old.Attempt.Context.DocumentUUID != "" && copy.Context.DocumentUUID != "" && old.Attempt.Context.DocumentUUID != copy.Context.DocumentUUID || old.Attempt.WindowID != "" && copy.WindowID != "" && old.Attempt.WindowID != copy.WindowID || old.Attempt.ConnectionEpoch != "" && copy.ConnectionEpoch != "" && old.Attempt.ConnectionEpoch != copy.ConnectionEpoch {
			return fmt.Errorf("original recovery scope is immutable")
		}
		if old.Attempt.Request.ID != "" {
			if copy.Request.ID != "" {
				prior, _ := json.Marshal(old.Attempt.Request)
				next, _ := json.Marshal(copy.Request)
				if !bytes.Equal(prior, next) {
					return fmt.Errorf("original recovery request is immutable")
				}
			}
			copy.Request = old.Attempt.Request
		}
		if old.Attempt.Baseline != nil {
			copy.Baseline = old.Attempt.Baseline
		}
		if copy.ContinuationBaseline == nil {
			copy.ContinuationBaseline = old.Attempt.ContinuationBaseline
		}
		if copy.Evidence == nil {
			copy.Evidence = old.Attempt.Evidence
		}
		if copy.Receipt == nil {
			copy.Receipt = old.Attempt.Receipt
		}
		if copy.Receipt != nil && copy.Receipt.ObservedFacts == nil {
			r := *copy.Receipt
			r.ObservedFacts = old.Facts
			copy.Receipt = &r
		}
		if copy.ScopeKey == "" {
			copy.ScopeKey = old.Attempt.ScopeKey
		}
		if copy.WindowID == "" {
			copy.WindowID = old.Attempt.WindowID
		}
		if copy.Context.ProjectUUID == "" {
			copy.Context = old.Attempt.Context
		}
		if copy.ConnectionEpoch == "" {
			copy.ConnectionEpoch = old.Attempt.ConnectionEpoch
		}
		if copy.TaskID == "" {
			copy.TaskID = old.Attempt.TaskID
		}
		if copy.TaskRevision == 0 {
			copy.TaskRevision = old.Attempt.TaskRevision
		}
		recoveryClassifyAttempt(&copy)
		raw, meta, e := recoveryAttemptBytes(&copy)
		if e != nil {
			return e
		}
		result, e := tx.Exec(`UPDATE recovery_attempts SET revision=?,payload=?,hash=?,state=?,metadata=?,metadata_hash=? WHERE request_id=? AND revision=?`, copy.Revision, raw, recoveryHash(raw), copy.State, meta, recoveryHash(meta), copy.RequestID, attempt.Revision)
		if e != nil {
			return e
		}
		n, e := result.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return ErrRecoveryConflict
		}
		if err = recoveryEvidence(tx, "attempt", copy.RequestID, raw, "runtime"); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	*attempt = copy
	return nil
}

// Loading original evidence does not establish current editor freshness.
func (s *recoveryStore) LoadAttempt(id string) (*recoveryAttempt, error) {
	attempt, _, err := s.LoadAttemptEvidence(id)
	return attempt, err
}

func (s *recoveryStore) ListUnresolved() ([]recoveryAttempt, error) {
	rows, err := s.db.Query(`SELECT request_id,revision,metadata,metadata_hash FROM recovery_attempts WHERE state IN ('pending','dispatched','unknown') ORDER BY request_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	attempts := []recoveryAttempt{}
	for rows.Next() {
		var id, hash string
		var revision uint64
		var raw []byte
		var attempt recoveryAttempt
		if err = rows.Scan(&id, &revision, &raw, &hash); err != nil {
			return nil, err
		}
		if recoveryHash(raw) != hash || json.Unmarshal(raw, &attempt) != nil || attempt.RequestID != id || attempt.Revision != revision {
			return nil, ErrRecoveryCorrupt
		}
		attempts = append(attempts, attempt)
	}
	return attempts, rows.Err()
}

var _ recoveryStorage = (*recoveryStore)(nil)

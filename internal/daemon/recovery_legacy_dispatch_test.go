package daemon

import (
	"archive/zip"
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const legacyDispatchCreateID = "c09afd78-d7b1-4ec4-b434-209dd0836bf1"
const legacyDispatchBatchID = "cd50ec8b-b9a9-4d7b-84f7-7b5758cb3882"
const legacyDispatchSaveID = "req_IQQOXYB7BDGTOOIBEWRISONY3X"

// The authorized backup stays outside source control; only its audit inputs are
// copied into a test-owned directory. No real daemon or EDA connection is used.
func legacyDispatchBackup(t *testing.T) (string, string) {
	t.Helper()
	path := os.Getenv("EASYEDA_LEGACY_MIGRATION_BACKUP_ZIP")
	if path == "" {
		t.Skip("explicit authorized legacy migration backup ZIP was not supplied")
	}
	if acceptanceFileHash(t, path) != "d8520d38b02e26a40d13b71a453ef8e027a064a946c8d8e065ec660cbdd2b2ae" {
		t.Fatal("authorized backup hash mismatch")
	}
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal("open authorized migration backup")
	}
	defer archive.Close()
	dir := t.TempDir()
	checkpoint, audits := 0, 0
	for _, entry := range archive.File {
		name := strings.TrimPrefix(entry.Name, "runtime/audit/")
		if name == entry.Name || strings.Contains(name, "/") {
			continue
		}
		output := name
		if name == "runtime-60932.json" {
			output = "runtime-0.json"
			checkpoint++
		} else if strings.HasSuffix(name, ".jsonl") {
			audits++
		} else {
			continue
		}
		reader, err := entry.Open()
		if err != nil {
			t.Fatal("open authorized migration input")
		}
		writer, err := os.OpenFile(filepath.Join(dir, output), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			reader.Close()
			t.Fatal("create temporary migration input")
		}
		_, copyErr := io.Copy(writer, io.LimitReader(reader, 128<<20+1))
		readErr, writeErr := reader.Close(), writer.Close()
		if copyErr != nil || readErr != nil || writeErr != nil || entry.UncompressedSize64 > 128<<20 {
			t.Fatal("copy authorized migration input")
		}
	}
	if checkpoint != 1 || audits != 14 {
		t.Fatalf("authorized input counts: checkpoints=%d audits=%d", checkpoint, audits)
	}
	return dir, filepath.Join(dir, "runtime-0.json")
}

func TestLegacyDispatchAuthorizedBackupKnownSuccess(t *testing.T) {
	dir, checkpoint := legacyDispatchBackup(t)
	personalCreate := legacyDispatchRows(t, dir)[legacyDispatchCreateID]
	store, err := openRecoveryStore(filepath.Join(dir, "recovery-0.sqlite"))
	if err != nil {
		t.Fatal("open temporary recovery database")
	}
	t.Cleanup(func() { _ = store.Close() })
	report, err := store.ImportLegacy(checkpoint)
	if err != nil || report.Scopes != 8 || report.Receipts != 256 || report.AuditRecords != 3129 {
		t.Fatalf("authorized migration counts: scopes=%d receipts=%d audit=%d err=%v", report.Scopes, report.Receipts, report.AuditRecords, err)
	}
	attempt, err := store.LoadAttempt(legacyDispatchCreateID)
	if err != nil || attempt == nil {
		t.Fatal("known historical project.create was lost")
	}
	if attempt.State != recoveryAttemptSettled || !attempt.ExecutionKnown || !attempt.Executed {
		t.Fatalf("known historical success became a blocker: state=%s executionKnown=%v executed=%v", attempt.State, attempt.ExecutionKnown, attempt.Executed)
	}
	if !attempt.EvidenceUnknown || attempt.ConnectionEpoch != "" {
		t.Fatal("historical success acquired fresh-scene authority")
	}
	before := legacyDispatchFingerprint(t, store, "recovery_attempts")
	evidence := legacyDispatchFingerprint(t, store, "recovery_evidence")
	if err = store.Close(); err != nil {
		t.Fatal("close first migration process")
	}
	s, base := acceptanceServer(t, dir)
	if s.runtime.recoveryError != nil || s.runtime.recovery == nil {
		t.Fatal("restart could not open temporary migrated recovery store")
	}
	activeStore := s.runtime.recovery.(*recoveryStore)
	if legacyDispatchFingerprint(t, activeStore, "recovery_attempts") != before || legacyDispatchFingerprint(t, activeStore, "recovery_evidence") != evidence {
		t.Fatal("restart/repeated startup import changed historical classification or evidence")
	}
	_, calls := acceptanceConnector(t, s, base, "legacy-fresh", "synthetic-unrelated-project", "synthetic-unrelated-document", nil)
	if calls.Load() != 0 {
		t.Fatal("historical action replayed when a connector joined")
	}
	duplicate := acceptanceAction(t, s, legacyDispatchCreateID, "legacy-fresh", "project.create", map[string]any{"friendlyName": "synthetic"})
	if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || calls.Load() != 0 {
		t.Fatal("historical successful ID acquired replay permission")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "legacy-acceptance-new-write", "legacy-fresh", "schematic.component.modify"))
	acceptanceWantOK(t, acceptanceAction(t, s, "legacy-acceptance-new-save", "legacy-fresh", "schematic.save"))
	if calls.Load() != 2 {
		t.Fatal("unrelated fresh work did not dispatch exactly once per action")
	}
	project, _ := personalCreate.Result["uuid"].(string)
	if project == "" {
		t.Fatal("authorized historical create lost its project identity")
	}
	_, sameProjectCalls := acceptanceConnector(t, s, base, "legacy-personal-new-task", project, "synthetic-new-document", nil)
	personalResponse := acceptanceContinuationAction(t, s, "legacy-acceptance-personal-new-write", "legacy-personal-new-task", "schematic.component.modify", nil)
	if personalResponse.OK || personalResponse.Error == nil || personalResponse.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || sameProjectCalls.Load() != 0 {
		t.Fatal("remaining genuine unknown no longer blocked an unproved continuation")
	}
	guardFound := false
	for _, id := range []string{"21474e7f-197a-46b3-b383-35c02eca29b2", "bcdb0b25-6dae-4916-8071-a7d2d5bdec4d"} {
		if !strings.Contains(personalResponse.Error.Detail, id) {
			continue
		}
		guard, err := activeStore.LoadAttempt(id)
		if err != nil || guard == nil || guard.State != recoveryAttemptUnknown || guard.Context.ProjectUUID != project || (!guard.Dangerous && !guard.ContentWrite) {
			t.Fatal("personal-project rejection did not retain its genuine historical guard")
		}
		guardFound = true
		t.Logf("remaining personal-project guard: id=%s action=%s state=%s", id, guard.Request.Action, guard.State)
	}
	if !guardFound {
		t.Fatal("new personal-project write was blocked by an unexpected historical action")
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "legacy-acceptance-personal-read", "legacy-personal-new-task", "schematic.components.list"))
	if sameProjectCalls.Load() != 1 {
		t.Fatal("remaining personal-project unknown blocked scoped readback")
	}
	before = legacyDispatchFingerprint(t, activeStore, "recovery_attempts")
	retainedEvidence := legacyDispatchEvidenceSnapshot(t, activeStore)
	if report, err = activeStore.ImportLegacy(checkpoint); err != nil || !report.AlreadyImported {
		t.Fatal("explicit repeat import failed")
	}
	if legacyDispatchFingerprint(t, activeStore, "recovery_attempts") != before {
		t.Fatal("repeat import overwrote later attempts")
	}
	afterEvidence := legacyDispatchEvidenceSnapshot(t, activeStore)
	for key, digest := range retainedEvidence {
		if afterEvidence[key] != digest {
			t.Fatal("repeat import replaced or removed existing evidence")
		}
	}
	// The first repeat can retain newly appended audit rows, including rejected
	// duplicate IDs. Repeating those same inputs again must add nothing.
	evidence = legacyDispatchFingerprint(t, activeStore, "recovery_evidence")
	if _, err = activeStore.ImportLegacy(checkpoint); err != nil || legacyDispatchFingerprint(t, activeStore, "recovery_attempts") != before || legacyDispatchFingerprint(t, activeStore, "recovery_evidence") != evidence {
		t.Fatal("identical repeat import changed attempts or duplicated evidence")
	}
}

func legacyDispatchFingerprint(t *testing.T, store *recoveryStore, table string) string {
	t.Helper()
	query := `SELECT request_id,hash FROM recovery_attempts ORDER BY request_id`
	if table == "recovery_evidence" {
		query = `SELECT kind || ':' || record_id,hash FROM recovery_evidence ORDER BY kind,record_id,hash`
	}
	rows, err := store.db.Query(query)
	if err != nil {
		t.Fatal("read temporary migration fingerprint")
	}
	defer rows.Close()
	hash := sha256.New()
	for rows.Next() {
		var id, digest string
		if rows.Scan(&id, &digest) != nil {
			t.Fatal("read migration fingerprint row")
		}
		_, _ = io.WriteString(hash, id+"\x00"+digest+"\n")
	}
	if rows.Err() != nil {
		t.Fatal("migration fingerprint iteration failed")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func legacyDispatchEvidenceSnapshot(t *testing.T, store *recoveryStore) map[string]string {
	t.Helper()
	rows, err := store.db.Query(`SELECT kind,record_id,hash,source,raw FROM recovery_evidence`)
	if err != nil {
		t.Fatal("read retained migration evidence")
	}
	defer rows.Close()
	snapshot := map[string]string{}
	for rows.Next() {
		var kind, id, digest, source string
		var raw []byte
		if rows.Scan(&kind, &id, &digest, &source, &raw) != nil || recoveryHash(raw) != digest {
			t.Fatal("existing migration evidence failed its integrity check")
		}
		snapshot[kind+"\x00"+id+"\x00"+digest] = recoveryHash(append([]byte(source+"\x00"), raw...))
	}
	if rows.Err() != nil {
		t.Fatal("retained migration evidence iteration failed")
	}
	return snapshot
}

func legacyDispatchRows(t *testing.T, dir string) map[string]auditEntry {
	t.Helper()
	selected := map[string]auditEntry{}
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal("read temporary authorized audit")
		}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 64*1024), 64<<20)
		for scanner.Scan() {
			raw := scanner.Bytes()
			for _, id := range []string{legacyDispatchCreateID, legacyDispatchBatchID, legacyDispatchSaveID} {
				if !bytes.Contains(raw, []byte(`"requestId":"`+id+`"`)) {
					continue
				}
				var entry auditEntry
				if json.Unmarshal(raw, &entry) != nil || entry.RequestID != id {
					t.Fatal("decode selected personal-engineering audit row")
				}
				if _, exists := selected[id]; exists {
					t.Fatal("selected known-success source ID was not unique")
				}
				selected[id] = entry
			}
		}
		if scanner.Err() != nil {
			t.Fatal("scan authorized audit input")
		}
		file.Close()
	}
	if len(selected) != 3 {
		t.Fatal("authorized selected audit rows were missing")
	}
	return selected
}

func legacyDispatchVariant(t *testing.T, base string, source auditEntry, mutate func(*auditEntry), conflict bool) (string, auditEntry) {
	t.Helper()
	raw, _ := json.Marshal(source)
	var entry auditEntry
	if json.Unmarshal(raw, &entry) != nil {
		t.Fatal("clone selected authorized row")
	}
	mutate(&entry)
	dir := t.TempDir()
	row, err := json.Marshal(entry)
	if err != nil {
		t.Fatal("encode temporary derived audit")
	}
	paths, err := filepath.Glob(filepath.Join(base, "*"))
	if err != nil {
		t.Fatal("find original temporary audit inputs")
	}
	changed := false
	for _, path := range paths {
		name := filepath.Base(path)
		output := filepath.Join(dir, name)
		if name != source.Timestamp.UTC().Format("2006-01-02")+".jsonl" {
			input, err := os.Open(path)
			if err != nil {
				t.Fatal("read unchanged temporary migration input")
			}
			copy, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				input.Close()
				t.Fatal("create independent temporary migration input")
			}
			_, copyErr := io.Copy(copy, input)
			readErr, writeErr := input.Close(), copy.Close()
			if copyErr != nil || readErr != nil || writeErr != nil {
				t.Fatal("copy independent temporary migration input")
			}
			continue
		}
		input, err := os.Open(path)
		if err != nil {
			t.Fatal("read temporary audit for one-row variant")
		}
		outputFile, err := os.OpenFile(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			input.Close()
			t.Fatal("create temporary one-row audit variant")
		}
		scanner, writer := bufio.NewScanner(input), bufio.NewWriter(outputFile)
		scanner.Buffer(make([]byte, 64*1024), 64<<20)
		for scanner.Scan() {
			line := scanner.Bytes()
			if bytes.Contains(line, []byte(`"requestId":"`+source.RequestID+`"`)) {
				changed = true
				if conflict {
					_, _ = writer.Write(append(append([]byte(nil), line...), '\n'))
				}
				line = row
			}
			if _, err = writer.Write(append(append([]byte(nil), line...), '\n')); err != nil {
				t.Fatal("write one-row audit variant")
			}
		}
		scanErr, flushErr := scanner.Err(), writer.Flush()
		readErr, writeErr := input.Close(), outputFile.Close()
		if scanErr != nil || flushErr != nil || readErr != nil || writeErr != nil {
			t.Fatal("complete temporary one-row audit variant")
		}
	}
	if !changed {
		t.Fatal("one-row variant did not alter its selected source")
	}
	return dir, entry
}

func TestLegacyDispatchAuthorizedBackupUnsafeVariants(t *testing.T) {
	base, _ := legacyDispatchBackup(t)
	rows := legacyDispatchRows(t, base)
	t.Run("known-create-cropped-fixture", func(t *testing.T) {
		// This isolates the original create's classification; the full-backup
		// test above independently retains and rejects the remaining unknowns.
		dir := t.TempDir()
		create := rows[legacyDispatchCreateID]
		raw, err := json.Marshal(create)
		if err != nil || os.WriteFile(filepath.Join(dir, "known-create.jsonl"), append(raw, '\n'), 0600) != nil || os.WriteFile(filepath.Join(dir, "runtime-0.json"), []byte(`{"version":1,"documents":{},"receipts":[]}`), 0600) != nil {
			t.Fatal("write cropped original-create fixture")
		}
		s, transport := acceptanceServer(t, dir)
		project, _ := create.Result["uuid"].(string)
		if project == "" {
			t.Fatal("original create result lost project identity")
		}
		_, calls := acceptanceConnector(t, s, transport, "cropped-new-task", project, "synthetic-cropped-new-document", nil)
		duplicate := acceptanceAction(t, s, create.RequestID, "cropped-new-task", create.Action, create.Payload)
		if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || calls.Load() != 0 {
			t.Fatal("cropped successful create acquired replay permission")
		}
		acceptanceWantOK(t, acceptanceAction(t, s, "cropped-new-write", "cropped-new-task", "schematic.component.modify"))
		acceptanceWantOK(t, acceptanceAction(t, s, "cropped-new-save", "cropped-new-task", "schematic.save"))
		if calls.Load() != 2 {
			t.Fatal("known successful create still blocked a new task in its project")
		}
	})
	cases := []struct {
		name     string
		id       string
		mutate   func(*auditEntry)
		conflict bool
	}{
		{"missing-success-response", legacyDispatchCreateID, func(e *auditEntry) { e.Result = nil }, false},
		{"different-response-target", legacyDispatchCreateID, func(e *auditEntry) { e.Context.ProjectUUID = "synthetic-conflicting-project" }, false},
		{"batch-target-mismatch", legacyDispatchBatchID, func(e *auditEntry) {
			e.Payload["_target"].(map[string]any)["documentUuid"] = "synthetic-conflicting-document"
		}, false},
		{"batch-partial", legacyDispatchBatchID, func(e *auditEntry) { e.Result["partial"] = true }, false},
		{"batch-child-partial", legacyDispatchBatchID, func(e *auditEntry) {
			e.Result["results"].([]any)[0].(map[string]any)["result"].(map[string]any)["partial"] = true
		}, false},
		{"batch-child-error", legacyDispatchBatchID, func(e *auditEntry) { e.Result["results"].([]any)[0].(map[string]any)["ok"] = false }, false},
		{"same-id-conflict", legacyDispatchCreateID, func(e *auditEntry) { e.Payload["friendlyName"] = "synthetic-conflict" }, true},
		{"unknown-save", legacyDispatchSaveID, func(e *auditEntry) { e.Result["saved"] = nil }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("full-backup", func(t *testing.T) {
				dir, entry := legacyDispatchVariant(t, base, rows[tc.id], tc.mutate, tc.conflict)
				s, transport := acceptanceServer(t, dir)
				if s.runtime.recoveryError != nil {
					t.Fatalf("full-backup migration failed: %v", s.runtime.recoveryError)
				}
				attempt, err := s.runtime.recovery.LoadAttempt(tc.id)
				if err != nil || attempt == nil || attempt.State != recoveryAttemptUnknown || !attempt.EvidenceUnknown || attempt.ExecutionKnown || attempt.Executed {
					t.Fatal("incomplete or conflicting legacy evidence escaped unknown state")
				}
				if tc.name == "unknown-save" {
					if attempt.Receipt != nil && attempt.Receipt.Status == "saved" {
						t.Fatal("unknown historical save became saved:true")
					}
				}
				project, document := legacyDispatchAffectedTarget(t, rows[tc.id], entry)
				_, calls := acceptanceConnector(t, s, transport, "derived-affected", project, document, nil)
				duplicate := acceptanceAction(t, s, tc.id, "derived-affected", entry.Action, entry.Payload)
				if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || calls.Load() != 0 {
					t.Fatal("unknown historical ID was replayable")
				}
				response := acceptanceContinuationAction(t, s, "derived-unsafe-new-write", "derived-affected", "schematic.component.modify", nil)
				if response.OK || response.Error == nil || response.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || calls.Load() != 0 {
					t.Fatal("unknown legacy result allowed an unproved continuation to dispatch")
				}
				acceptanceWantOK(t, acceptanceAction(t, s, "derived-affected-read", "derived-affected", "schematic.components.list"))
				if calls.Load() != 1 {
					t.Fatal("related scoped readback did not dispatch once")
				}
				_, unrelated := acceptanceConnector(t, s, transport, "derived-unrelated", "synthetic-other-project", "synthetic-other-document", nil)
				acceptanceWantOK(t, acceptanceAction(t, s, "derived-unrelated-new-write", "derived-unrelated", "schematic.component.modify"))
				if unrelated.Load() != 1 {
					t.Fatal("unknown legacy scope blocked unrelated new work")
				}
			})
			t.Run("cropped-guard", func(t *testing.T) {
				original := rows[tc.id]
				raw, err := json.Marshal(original)
				var entry auditEntry
				if err != nil || json.Unmarshal(raw, &entry) != nil {
					t.Fatal("clone selected original audit for cropped guard")
				}
				tc.mutate(&entry)
				dir := legacyDispatchCroppedInput(t, original, entry, tc.conflict)
				s, transport := acceptanceServer(t, dir)
				attempt, err := s.runtime.recovery.LoadAttempt(tc.id)
				if err != nil || attempt == nil || attempt.State != recoveryAttemptUnknown || !attempt.EvidenceUnknown || attempt.ExecutionKnown || attempt.Executed {
					t.Fatal("cropped unsafe source escaped unknown state")
				}
				project, document := legacyDispatchAffectedTarget(t, original, entry)
				_, calls := acceptanceConnector(t, s, transport, "cropped-affected", project, document, nil)
				duplicate := acceptanceAction(t, s, tc.id, "cropped-affected", entry.Action, entry.Payload)
				if duplicate.OK || duplicate.Error == nil || duplicate.Error.Code != "DUPLICATE_REQUEST" || calls.Load() != 0 {
					t.Fatal("cropped unknown historical ID was replayable")
				}
				response := acceptanceContinuationAction(t, s, "cropped-unsafe-new-write", "cropped-affected", "schematic.component.modify", nil)
				if response.OK || response.Error == nil || response.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || !strings.Contains(response.Error.Detail, tc.id) || calls.Load() != 0 {
					t.Fatal("specific cropped unknown did not guard its actual request target")
				}
				if tc.name == "batch-target-mismatch" {
					if !attempt.ProjectWide || attempt.Context.ProjectUUID != project {
						t.Fatal("ambiguous batch document did not retain its project guard")
					}
					_, originalDocumentCalls := acceptanceConnector(t, s, transport, "cropped-original-document", project, original.Context.DocumentUUID, nil)
					originalResponse := acceptanceContinuationAction(t, s, "cropped-original-document-write", "cropped-original-document", "schematic.component.modify", nil)
					if originalResponse.OK || originalResponse.Error == nil || originalResponse.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || !strings.Contains(originalResponse.Error.Detail, tc.id) || originalDocumentCalls.Load() != 0 {
						t.Fatal("ambiguous batch failed to guard its original response document")
					}
				}
				if tc.name == "different-response-target" {
					_, freshCalls := acceptanceConnector(t, s, transport, "cropped-fresh-window", project, "synthetic-fresh-document", nil)
					freshResponse := acceptanceContinuationAction(t, s, "cropped-fresh-window-write", "cropped-fresh-window", "schematic.component.modify", nil)
					if freshResponse.OK || freshResponse.Error == nil || freshResponse.Error.Code != "RUNTIME_SCOPE_UNKNOWN" || !strings.Contains(freshResponse.Error.Detail, tc.id) || freshCalls.Load() != 0 {
						t.Fatal("unknown create lost its actual project guard after changing windows")
					}
				}
				acceptanceWantOK(t, acceptanceAction(t, s, "cropped-affected-read", "cropped-affected", "schematic.components.list"))
				if calls.Load() != 1 {
					t.Fatal("specific cropped unknown blocked scoped readback")
				}
				_, unrelated := acceptanceConnector(t, s, transport, "cropped-unrelated", "synthetic-other-project", "synthetic-other-document", nil)
				acceptanceWantOK(t, acceptanceAction(t, s, "cropped-unrelated-write", "cropped-unrelated", "schematic.component.modify"))
				if unrelated.Load() != 1 {
					t.Fatal("specific cropped unknown blocked unrelated new work")
				}
			})
		})
	}
}

func legacyDispatchAffectedTarget(t *testing.T, original, variant auditEntry) (string, string) {
	t.Helper()
	if original.Context == nil || variant.WindowID == "" {
		t.Fatal("selected original source lost identity")
	}
	project, document := original.Context.ProjectUUID, original.Context.DocumentUUID
	if variant.Action == "project.create" {
		// The created UUID names the actual project even when response context
		// conflicts; a fresh window must retain that project's guard.
		project, _ = original.Result["uuid"].(string)
	} else if target, ok := variant.Payload["_target"].(map[string]any); ok {
		project, _ = target["projectUuid"].(string)
		document, _ = target["documentUuid"].(string)
	}
	if project == "" {
		t.Fatal("selected original request lost its affected project")
	}
	if document == "" {
		document = "synthetic-affected-document"
	}
	return project, document
}

func legacyDispatchCroppedInput(t *testing.T, original, variant auditEntry, conflict bool) string {
	t.Helper()
	dir := t.TempDir()
	row, err := json.Marshal(variant)
	if err != nil {
		t.Fatal("encode cropped audit variant")
	}
	if conflict {
		before, err := json.Marshal(original)
		if err != nil {
			t.Fatal("encode original conflicting audit source")
		}
		row = append(append(before, '\n'), row...)
	}
	if os.WriteFile(filepath.Join(dir, "cropped-source.jsonl"), append(row, '\n'), 0600) != nil || os.WriteFile(filepath.Join(dir, "runtime-0.json"), []byte(`{"version":1,"documents":{},"receipts":[]}`), 0600) != nil {
		t.Fatal("write cropped source fixture")
	}
	return dir
}

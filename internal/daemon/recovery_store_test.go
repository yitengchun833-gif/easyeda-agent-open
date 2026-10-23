package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRecoveryStoreScopeCASAndPrivateBasis(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	s, err := openRecoveryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.Identity()
	if err != nil {
		t.Fatal(err)
	}
	s.db.SetMaxIdleConns(0)
	var synchronous int
	if err = s.db.QueryRow("PRAGMA synchronous").Scan(&synchronous); err != nil || synchronous != 3 {
		t.Fatalf("reopened connection lost durability: %d %v", synchronous, err)
	}
	s.db.SetMaxIdleConns(1)
	ctx := protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}
	scope := &recoveryScope{Key: "w|p|d", Document: &runtimeDocument{WindowID: "w", Context: ctx, ChangesSinceSave: true, Objects: map[string]*runtimeObject{"old": {Data: map[string]any{"primitiveId": "old"}}}}, Receipts: []*runtimeReceipt{{RequestID: "r", WindowID: "w", Status: "needs_readback", ObservedFacts: map[string]any{"private": "basis"}}}, Requests: map[string]protocol.Request{"r": {Envelope: protocol.Envelope{ID: "r"}, Action: "debug.batch"}}, Baselines: map[string]any{"r": map[string]any{"generation": 1}}}
	if err = s.SaveScope(scope); err != nil {
		t.Fatal(err)
	}
	if scope.Revision != 1 {
		t.Fatal(scope.Revision)
	}
	stale := *scope
	if err = s.SaveScope(scope); err != nil {
		t.Fatal(err)
	}
	if err = s.SaveScope(&stale); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = openRecoveryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if now, e := s.Identity(); e != nil || now != id {
		t.Fatalf("identity: %q %v", now, e)
	}
	loaded, err := s.LoadScope(scope.Key)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Revision != 2 || loaded.Receipts[0].Status != "needs_readback" || runtimeMap(loaded.Receipts[0].ObservedFacts)["private"] != "basis" || loaded.Baselines["r"] == nil {
		t.Fatalf("lost recovery basis: %#v", loaded)
	}
	index, err := s.ListIndex()
	if err != nil || len(index) != 1 || index[0].ObjectCount != 1 || !index[0].ChangesSinceSave {
		t.Fatalf("index %#v %v", index, err)
	}
	if missing, e := s.LoadScope("missing"); e != nil || missing != nil {
		t.Fatal("missing scope is not an error", e)
	}
}

func TestRecoveryStoreDurableAttemptsBeyondCacheAndRollback(t *testing.T) {
	s, err := openRecoveryStore(filepath.Join(t.TempDir(), "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var first recoveryAttempt
	for i := 0; i < 300; i++ {
		a := recoveryAttempt{RequestID: fmt.Sprintf("r-%03d", i), Request: protocol.Request{Envelope: protocol.Envelope{ID: fmt.Sprintf("r-%03d", i)}, Action: "debug.batch", Payload: map[string]any{"script": "private large payload"}}, State: recoveryAttemptPending, ScopeKey: "w|p|d", WindowID: "w", Baseline: map[string]any{"before": true}, ContentWrite: true}
		if err = s.SaveAttempt(&a); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = a
		}
	}
	if exists, e := s.HasRequest("r-000"); e != nil || !exists {
		t.Fatalf("old request forgotten: %v %v", exists, e)
	}
	duplicate := first
	duplicate.Revision = 0
	if err = s.SaveAttempt(&duplicate); !errors.Is(err, ErrRecoveryDuplicate) {
		t.Fatalf("duplicate: %v", err)
	}
	stale := first
	first.State = recoveryAttemptDispatched
	if err = s.SaveAttempt(&first); err != nil {
		t.Fatal(err)
	}
	first.State = recoveryAttemptSettled
	first.ContinuationBaseline = map[string]any{"after": true}
	first.ExecutionKnown = true
	first.Executed = true
	first.Receipt = &runtimeReceipt{RequestID: first.RequestID, Status: "paused", ObservedFacts: map[string]any{"private": "scoped-proof"}}
	if err = s.SaveAttempt(&first); err != nil {
		t.Fatal(err)
	}
	first.ContinuationBaseline = nil
	if err = s.SaveAttempt(&first); err != nil {
		t.Fatal(err)
	}
	stale.State = recoveryAttemptNotDispatched
	if err = s.SaveAttempt(&stale); !errors.Is(err, ErrRecoveryConflict) {
		t.Fatalf("old outcome replaced new: %v", err)
	}
	unknown, err := s.ListUnresolved()
	if err != nil || len(unknown) != 299 || unknown[0].Baseline != nil || unknown[0].ContinuationBaseline != nil || unknown[0].Request.Payload != nil {
		t.Fatalf("unresolved payload retained: %d %v", len(unknown), err)
	}
	scope := &recoveryScope{Key: "w|p|d", Document: &runtimeDocument{WindowID: "w", Context: protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}}, Receipts: []*runtimeReceipt{first.Receipt}, Requests: map[string]protocol.Request{first.RequestID: first.Request}, Baselines: map[string]any{first.RequestID: first.Baseline}}
	if err = s.SaveScope(scope); err != nil {
		t.Fatal(err)
	}
	scope.Receipts, scope.Requests, scope.Baselines = nil, nil, nil
	if err = s.SaveScope(scope); err != nil {
		t.Fatal(err)
	}
	old, err := s.LoadAttempt("r-000")
	if err != nil || old == nil || old.Receipt.Status != "paused" || runtimeMap(old.Baseline)["before"] != true || runtimeMap(old.ContinuationBaseline)["after"] != true || old.Request.Payload["script"] != "private large payload" || runtimeMap(old.Receipt.ObservedFacts)["private"] != "scoped-proof" {
		t.Fatalf("evicted paused basis inaccessible: %#v %v", old, err)
	}
	if missing, e := s.LoadAttempt("missing"); e != nil || missing != nil {
		t.Fatalf("missing attempt: %#v %v", missing, e)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER reject_recovery_evidence BEFORE INSERT ON recovery_evidence BEGIN SELECT RAISE(ABORT,'injected ledger failure'); END`); err != nil {
		t.Fatal(err)
	}
	failed := recoveryAttempt{RequestID: "fail", State: recoveryAttemptPending}
	if err = s.SaveAttempt(&failed); err == nil {
		t.Fatal("expected transaction failure")
	}
	if exists, e := s.HasRequest("fail"); e != nil || exists || failed.Revision != 0 {
		t.Fatalf("failed transaction leaked: %v %v revision %d", exists, e, failed.Revision)
	}
}

func TestRecoveryStoreLegacyRawAndAuditIdempotence(t *testing.T) {
	dir := t.TempDir()
	legacyPath := filepath.Join(dir, "runtime.json")
	raw := []byte("{\n  \"version\": 1, \"documents\": {\"w|p|d\": {\"windowId\":\"w\",\"context\":{\"projectUuid\":\"p\",\"documentUuid\":\"d\"},\"objects\":{},\"mutationAttemptedSinceConfirmedSave\":true}}, \"receipts\":[{\"requestId\":\"old\",\"windowId\":\"w\",\"projectUuid\":\"p\",\"documentUuid\":\"d\",\"status\":\"needs_readback\",\"contentWrite\":true}]\n}")
	if err := os.WriteFile(legacyPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	var audit strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&audit, "{\"requestId\":\"audit-%d\",\"windowId\":\"w\",\"action\":\"debug.batch\",\"ok\":false,\"context\":{\"projectUuid\":\"p\",\"documentUuid\":\"d\"}}\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, "history.jsonl"), []byte(audit.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openRecoveryStore(filepath.Join(dir, "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	report, err := s.ImportLegacy(legacyPath)
	if err != nil || report.Scopes != 1 || report.Receipts != 1 || report.AuditRecords != 300 {
		t.Fatalf("migration %#v %v", report, err)
	}
	loaded, err := s.LoadScope("w|p|d")
	if err != nil || !loaded.EvidenceUnknown || loaded.Receipts[0].Status != "needs_readback" {
		t.Fatalf("legacy status changed %#v %v", loaded, err)
	}
	if exists, e := s.HasRequest("audit-0"); e != nil || !exists {
		t.Fatalf("older audit dedup %v %v", exists, e)
	}
	var saved []byte
	if err = s.db.QueryRow(`SELECT raw FROM recovery_evidence WHERE kind='legacy-source'`).Scan(&saved); err != nil || string(saved) != string(raw) {
		t.Fatal("raw source changed", err)
	}
	before := 0
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM recovery_evidence`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	again, err := s.ImportLegacy(legacyPath)
	if err != nil || !again.AlreadyImported {
		t.Fatalf("repeat import %#v %v", again, err)
	}
	after := 0
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM recovery_evidence`).Scan(&after); err != nil || before != after {
		t.Fatalf("non-idempotent %d/%d %v", before, after, err)
	}
	actual, err := os.ReadFile(legacyPath)
	if err != nil || string(actual) != string(raw) {
		t.Fatal("original changed", err)
	}
	loaded.Document.ChangesSinceSave = false
	if err = s.SaveScope(loaded); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ImportLegacy(legacyPath); err != nil {
		t.Fatal(err)
	}
	fresh, err := s.LoadScope(loaded.Key)
	if err != nil || fresh.Document.ChangesSinceSave {
		t.Fatal("legacy overwrote newer SQL", err)
	}
}

func TestRecoveryStoreMalformedMigrationAndCorruption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "runtime.json")
	s, err := openRecoveryStore(filepath.Join(dir, "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err = s.ImportLegacy(path); !os.IsNotExist(err) {
		t.Fatalf("missing source: %v", err)
	}
	for _, broken := range []string{
		`{"version":1,"documents":{},"receipts":[null]}`,
		`{"version":1,"documents":{"w|p|d":{"windowId":"w","context":{"projectUuid":"p","documentUuid":"d"},"objects":{"r":null}}},"receipts":[]}`,
		`{"version":1,"documents":{"w|p|d":{"windowId":"w","context":{"projectUuid":"p","documentUuid":"d"},"objects":{"r":{}}}},"receipts":[]}`,
	} {
		if err = os.WriteFile(path, []byte(broken), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err = s.ImportLegacy(path); !errors.Is(err, ErrRecoveryCorrupt) {
			t.Fatalf("broken source: %v", err)
		}
	}
	var n int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM recovery_meta WHERE key LIKE 'legacy:%'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("failed import marked complete: %d %v", n, err)
	}
	a := recoveryAttempt{RequestID: "r", State: recoveryAttemptPending}
	if err = s.SaveAttempt(&a); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE recovery_attempts SET metadata='{}' WHERE request_id='r'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ListUnresolved(); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("corrupt evidence trusted: %v", err)
	}
	if _, err = s.db.Exec(`UPDATE recovery_attempts SET payload='{}' WHERE request_id='r'`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.LoadAttempt("r"); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatalf("corrupt original attempt trusted: %v", err)
	}
}

func TestRecoveryStoreProcessExitIntent(t *testing.T) {
	if path := os.Getenv("EASYEDA_RECOVERY_TEST_CHILD"); path != "" {
		s, err := openRecoveryStore(path)
		if err != nil {
			t.Fatal(err)
		}
		a := recoveryAttempt{RequestID: "durable-before-dispatch", State: recoveryAttemptPending, ContentWrite: true, Baseline: map[string]any{"original": true}}
		if err = s.SaveAttempt(&a); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	path := filepath.Join(t.TempDir(), "recovery.sqlite")
	cmd := exec.Command(os.Args[0], "-test.run=^TestRecoveryStoreProcessExitIntent$")
	cmd.Env = append(os.Environ(), "EASYEDA_RECOVERY_TEST_CHILD="+path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child %v: %s", err, out)
	}
	s, err := openRecoveryStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if exists, e := s.HasRequest("durable-before-dispatch"); e != nil || !exists {
		t.Fatalf("intent lost after process exit: %v %v", exists, e)
	}
	var raw []byte
	if err = s.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id='durable-before-dispatch'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var record recoveryAttemptRecord
	if json.Unmarshal(raw, &record) != nil || record.Attempt.Baseline == nil || record.Attempt.State != recoveryAttemptPending {
		t.Fatal("intent basis lost")
	}
}

func TestRecoveryStoreTransitionAndEvidenceMetadata(t *testing.T) {
	s, err := openRecoveryStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a := recoveryAttempt{RequestID: "write", ScopeKey: "w|p|d", WindowID: "w", Context: protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}, Request: protocol.Request{Action: "debug.batch", Payload: map[string]any{"steps": []any{map[string]any{"action": "debug.exec_js", "payload": map[string]any{"script": "private"}}}}}, State: recoveryAttemptPending}
	if err = s.SaveAttempt(&a); err != nil {
		t.Fatal(err)
	}
	metadata, err := s.ListUnresolved()
	if err != nil || len(metadata) != 1 || !metadata[0].ProjectWide || !metadata[0].Dangerous || metadata[0].Request.Payload != nil {
		t.Fatalf("classification %#v %v", metadata, err)
	}
	a.State = recoveryAttemptSettled
	if err = s.SaveAttempt(&a); err == nil {
		t.Fatal("pending directly settled")
	}
	a.State = recoveryAttemptDispatched
	if err = s.SaveAttempt(&a); err != nil {
		t.Fatal(err)
	}
	a.State = recoveryAttemptNotDispatched
	if err = s.SaveAttempt(&a); err == nil {
		t.Fatal("dispatched falsely changed to not dispatched")
	}
	a.State = recoveryAttemptUnknown
	a.Evidence = map[string]any{"ok": false, "result": map[string]any{"original": "partial"}}
	if err = s.SaveAttempt(&a); err != nil {
		t.Fatal(err)
	}
	for _, state := range []recoveryAttemptState{recoveryAttemptPending, recoveryAttemptDispatched, recoveryAttemptSettled, recoveryAttemptNotDispatched} {
		a.State = state
		if err = s.SaveAttempt(&a); err == nil {
			t.Fatalf("unknown illegally changed to %s", state)
		}
	}
	a.State = recoveryAttemptUnknown
	a.Evidence = nil
	a.Context.ProjectUUID = "unrelated"
	if err = s.SaveAttempt(&a); err == nil {
		t.Fatal("unknown request moved to another project")
	}
	a.Context.ProjectUUID = "p"
	if err = s.SaveAttempt(&a); err != nil {
		t.Fatal(err)
	}
	metadata, err = s.ListUnresolved()
	if err != nil || metadata[0].Evidence != nil {
		t.Fatal("large evidence leaked in index", err)
	}
	var raw []byte
	if err = s.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id='write'`).Scan(&raw); err != nil || !strings.Contains(string(raw), `"original":"partial"`) {
		t.Fatal("raw outcome missing from SQLite", err)
	}
	terminal := recoveryAttempt{RequestID: "terminal", State: recoveryAttemptPending}
	if err = s.SaveAttempt(&terminal); err != nil {
		t.Fatal(err)
	}
	terminal.State = recoveryAttemptDispatched
	if err = s.SaveAttempt(&terminal); err != nil {
		t.Fatal(err)
	}
	terminal.State = recoveryAttemptSettled
	if err = s.SaveAttempt(&terminal); err != nil {
		t.Fatal(err)
	}
	terminal.State = recoveryAttemptPending
	if err = s.SaveAttempt(&terminal); err == nil {
		t.Fatal("terminal restarted")
	}
}

func TestRecoveryStoreAuditOnlyImportAndReadClassification(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.jsonl")
	raw := []byte("{\"requestId\":\"old-read\",\"action\":\"schematic.read\",\"ok\":true}\n{\"requestId\":\"old-project\",\"action\":\"debug.batch\",\"payload\":{\"steps\":[{\"action\":\"debug.exec_js\",\"payload\":{\"script\":\"private\"}}]},\"ok\":false}\n")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := openRecoveryStore(filepath.Join(dir, "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if n, e := s.ImportAuditDir(dir); e != nil || n != 2 {
		t.Fatalf("audit-only import %d %v", n, e)
	}
	if exists, e := s.HasRequest("old-read"); e != nil || !exists {
		t.Fatalf("audit-only dedup %v %v", exists, e)
	}
	unknown, err := s.ListUnresolved()
	if err != nil {
		t.Fatal(err)
	}
	if len(unknown) != 1 {
		t.Fatalf("read-only history became unknown write: %#v", unknown)
	}
	found := false
	for _, attempt := range unknown {
		if attempt.RequestID == "old-project" {
			found = attempt.Dangerous && attempt.ProjectWide
		}
	}
	if !found {
		t.Fatal("project unknown scope lost")
	}
}

package daemon

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func acceptanceFileHash(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal("read acceptance evidence:", err)
	}
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func acceptanceRename(path string) error { return os.Rename(path, path+".removed") }

func acceptanceLegacy(t *testing.T, dir string, documents, receipts int) string {
	t.Helper()
	saved := runtimeCheckpoint{Version: 1, At: time.Unix(1, 0).UTC(), Documents: map[string]*runtimeDocument{}, Current: map[string]string{}}
	for i := 0; i < documents; i++ {
		window, project, document := fmt.Sprintf("history-window-%d", i), fmt.Sprintf("history-project-%d", i), fmt.Sprintf("history-document-%d", i)
		key := window + "|" + project + "|" + document
		saved.Current[window] = key
		saved.Documents[key] = &runtimeDocument{WindowID: window, Context: protocol.Context{ProjectUUID: project, DocumentUUID: document, DocumentType: "schematic"}, Generation: 4, Task: &runtimeTask{Revision: 1, Goal: "synthetic historical intent"}, Objects: map[string]*runtimeObject{}, ChangesSinceSave: true}
	}
	for i := 0; i < receipts; i++ {
		scope := i % documents
		saved.Receipts = append(saved.Receipts, &runtimeReceipt{RequestID: fmt.Sprintf("history-%03d", i), WindowID: fmt.Sprintf("history-window-%d", scope), ProjectUUID: fmt.Sprintf("history-project-%d", scope), DocumentUUID: fmt.Sprintf("history-document-%d", scope), Action: "schematic.component.modify", ContentWrite: true, Status: "needs_readback", Generation: 4})
	}
	data, err := json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runtime-0.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRecoveryMigrationAcceptanceLegacyPreservedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	legacy := acceptanceLegacy(t, dir, 8, 256)
	before := acceptanceFileHash(t, legacy)
	store, err := openRecoveryStore(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal("open synthetic recovery:", err)
	}
	defer store.Close()
	first, err := store.ImportLegacy(legacy)
	if err != nil || first.Scopes != 8 || first.Receipts != 256 || first.SourceSHA256 != before {
		t.Fatalf("migration counts/hash mismatch: scopes=%d receipts=%d failed=%v", first.Scopes, first.Receipts, err != nil)
	}
	second, err := store.ImportLegacy(legacy)
	if err != nil || !second.AlreadyImported || acceptanceFileHash(t, legacy) != before {
		t.Fatal("legacy import was not idempotent or modified its source")
	}
	index, err := store.ListIndex()
	if err != nil || len(index) != 8 {
		t.Fatal("scope index lost migration history")
	}
	for i := 0; i < 256; i++ {
		found, err := store.HasRequest(fmt.Sprintf("history-%03d", i))
		if err != nil || !found {
			t.Fatalf("legacy request %d lost", i)
		}
	}
}

func TestRecoveryMigrationAcceptanceCorruptLegacyFailsClosed(t *testing.T) {
	for _, source := range []string{`{"version":1`, `{"version":999}`, `{"version":1,"documents":{"bad":null},"receipts":[]}`} {
		dir := t.TempDir()
		legacy := filepath.Join(dir, "runtime-0.json")
		if os.WriteFile(legacy, []byte(source), 0600) != nil {
			t.Fatal("write synthetic corrupt fixture")
		}
		s, base := acceptanceServer(t, dir)
		_, calls := acceptanceConnector(t, s, base, "new", "synthetic-project", "synthetic-document", nil)
		response := acceptanceAction(t, s, "corrupt-migration-write", "new", "schematic.component.modify")
		if response.OK || response.Error == nil || response.Error.Code != "RECOVERY_STORE_FAILED" || calls.Load() != 0 {
			t.Fatal("invalid migration became an empty fresh state")
		}
	}
}

func TestRecoveryMigrationAcceptanceAuthorizedBackup(t *testing.T) {
	path := os.Getenv("EASYEDA_ACCEPTANCE_BACKUP_ZIP")
	if path == "" {
		t.Skip("explicit authorized backup ZIP was not supplied")
	}
	archiveBefore := acceptanceFileHash(t, path)
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal("open authorized backup ZIP")
	}
	defer archive.Close()
	var data []byte
	matches := 0
	for _, entry := range archive.File {
		if entry.Name != "runtime-60932.json" {
			continue
		}
		matches++
		reader, err := entry.Open()
		if err != nil {
			t.Fatal("read authorized checkpoint entry")
		}
		data, err = io.ReadAll(io.LimitReader(reader, 64<<20+1))
		reader.Close()
		if err != nil || len(data) > 64<<20 {
			t.Fatal("authorized checkpoint could not be bounded")
		}
	}
	if matches != 1 {
		t.Fatal("authorized checkpoint entry was not unique")
	}
	var legacy runtimeCheckpoint
	if json.Unmarshal(data, &legacy) != nil {
		t.Fatal("authorized backup checkpoint is invalid")
	}
	statuses := map[string]int{}
	for _, receipt := range legacy.Receipts {
		if receipt == nil {
			t.Fatal("authorized backup contains nil receipt")
		}
		statuses[receipt.Status]++
	}
	dir := t.TempDir()
	copyPath := filepath.Join(dir, "runtime-0.json")
	if os.WriteFile(copyPath, data, 0600) != nil {
		t.Fatal("copy authorized checkpoint to temporary migration input")
	}
	s, base := acceptanceServer(t, dir)
	if s.runtime.recoveryError != nil {
		t.Fatal("authorized backup migration failed")
	}
	if len(s.runtime.documents) != 0 || len(s.runtime.requests) != 0 || len(s.runtime.baselines) != 0 {
		t.Fatal("historical backup consumed active resources or created replay permission")
	}
	for _, receipt := range legacy.Receipts {
		found, err := s.runtime.recovery.HasRequest(receipt.RequestID)
		if err != nil || !found {
			t.Fatal("an authorized historical request ID was lost")
		}
	}
	_, calls := acceptanceConnector(t, s, base, "fresh", "synthetic-new-project", "synthetic-new-document", nil)
	acceptanceWantOK(t, acceptanceAction(t, s, "authorized-backup-new-write", "fresh", "schematic.component.modify"))
	acceptanceWantOK(t, acceptanceAction(t, s, "authorized-backup-new-save", "fresh", "schematic.save"))
	if calls.Load() != 2 || acceptanceFileHash(t, path) != archiveBefore || acceptanceFileHash(t, copyPath) != acceptanceFileHashBytes(data) {
		t.Fatal("migration changed backup bytes or did not dispatch new work once")
	}
	t.Logf("anonymous migration: documents=%d receipts=%d statuses=%v checkpointSHA256=%s archiveSHA256=%s; source unchanged; active startup caches=0", len(legacy.Documents), len(legacy.Receipts), statuses, acceptanceFileHashBytes(data), archiveBefore)
}

func acceptanceFileHashBytes(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

func TestRecoveryMigrationAcceptanceInterruptedTransactionIsAtomic(t *testing.T) {
	if dir := os.Getenv("EASYEDA_MIGRATION_FAULT_CHILD_DIR"); dir != "" {
		store, err := openRecoveryStore(filepath.Join(dir, "migration.sqlite"))
		if err != nil {
			t.Fatal("child cannot open temporary database")
		}
		defer store.Close()
		if _, err := store.ImportLegacy(filepath.Join(dir, "runtime-0.json")); err != nil {
			t.Fatal("child migration failed before interruption")
		}
		t.Fatal("migration completed without the intended interruption")
	}
	dir := t.TempDir()
	legacy := acceptanceLegacy(t, dir, 8, 256)
	before := acceptanceFileHash(t, legacy)
	store, err := openRecoveryStore(filepath.Join(dir, "migration.sqlite"))
	if err != nil {
		t.Fatal("open migration fault fixture")
	}
	defer store.Close()
	// Slow the real migration inside its write transaction, after attempt rows.
	_, err = store.db.Exec(`CREATE TRIGGER acceptance_migration_pause BEFORE INSERT ON recovery_scopes BEGIN SELECT sum(x) FROM (WITH RECURSIVE counter(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM counter WHERE x<100000000) SELECT x FROM counter); END`)
	if err != nil {
		t.Fatal("install temporary migration interruption trigger")
	}
	if _, err := store.db.Exec(`PRAGMA busy_timeout=0`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestRecoveryMigrationAcceptanceInterruptedTransactionIsAtomic$", "-test.timeout=12s")
	command.Env = append(os.Environ(), "EASYEDA_MIGRATION_FAULT_CHILD_DIR="+dir, "TMP="+dir, "TEMP="+dir)
	if command.Start() != nil {
		t.Fatal("start migration fault subprocess")
	}
	defer command.Process.Kill()
	locked := false
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		_, err := store.db.Exec(`BEGIN IMMEDIATE`)
		if err == nil {
			_, _ = store.db.Exec(`ROLLBACK`)
		} else if strings.Contains(strings.ToLower(err.Error()), "locked") || strings.Contains(strings.ToLower(err.Error()), "busy") {
			locked = true
			break
		} else {
			t.Fatal("unexpected migration transaction probe failure")
		}
		time.Sleep(time.Millisecond)
	}
	if !locked {
		t.Fatal("migration never entered its write transaction")
	}
	if command.Process.Kill() != nil {
		t.Fatal("could not interrupt migration subprocess")
	}
	_ = command.Wait()
	for _, table := range []string{"recovery_scopes", "recovery_attempts", "recovery_evidence"} {
		var count int
		if err := store.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("interrupted migration committed partial evidence")
		}
	}
	if acceptanceFileHash(t, legacy) != before {
		t.Fatal("interrupted migration changed its source")
	}
	if _, err := store.db.Exec(`DROP TRIGGER acceptance_migration_pause`); err != nil {
		t.Fatal("remove temporary migration interruption trigger")
	}
	report, err := store.ImportLegacy(legacy)
	if err != nil || report.Scopes != 8 || report.Receipts != 256 || report.AlreadyImported {
		t.Fatal("interrupted import could not be retried from its unchanged source")
	}
}

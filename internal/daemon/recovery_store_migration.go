package daemon

import (
	"bufio"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func (s *recoveryStore) ImportLegacy(path string) (recoveryMigrationReport, error) {
	report := recoveryMigrationReport{}
	f, err := os.Open(path)
	if err != nil {
		return report, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 64*1024*1024+1))
	closeErr := f.Close()
	if err != nil {
		return report, err
	}
	if closeErr != nil {
		return report, closeErr
	}
	var checkpoint struct {
		Version   int                        `json:"version"`
		Documents map[string]json.RawMessage `json:"documents"`
		Receipts  []json.RawMessage          `json:"receipts"`
	}
	if len(raw) > 64*1024*1024 || json.Unmarshal(raw, &checkpoint) != nil || checkpoint.Version != 1 || len(checkpoint.Documents) > 8 {
		return report, fmt.Errorf("%w: legacy checkpoint unreadable or unsupported", ErrRecoveryCorrupt)
	}
	report.SourceSHA256 = recoveryHash(raw)
	tx, err := s.db.Begin()
	if err != nil {
		return report, err
	}
	defer tx.Rollback()
	marker := "legacy:" + report.SourceSHA256
	var exists bool
	if err = tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM recovery_meta WHERE key=?)`, marker).Scan(&exists); err != nil {
		return report, err
	}
	report.AlreadyImported = exists
	ids := map[string]bool{}
	scopes := map[string]*recoveryScope{}
	if !exists {
		if err = recoveryEvidence(tx, "legacy-source", report.SourceSHA256, raw, filepath.Base(path)); err != nil {
			return report, err
		}
		for key, documentRaw := range checkpoint.Documents {
			var document runtimeDocument
			if json.Unmarshal(documentRaw, &document) != nil || document.WindowID == "" || document.Context.ProjectUUID == "" || document.Context.DocumentUUID == "" || key != document.WindowID+"|"+document.Context.ProjectUUID+"|"+document.Context.DocumentUUID {
				return report, fmt.Errorf("%w: invalid legacy scope", ErrRecoveryCorrupt)
			}
			for _, object := range document.Objects {
				if object == nil || object.Data == nil {
					return report, fmt.Errorf("%w: invalid legacy observation", ErrRecoveryCorrupt)
				}
			}
			scope := &recoveryScope{Key: key, Revision: 1, Document: &document, EvidenceUnknown: true}
			scopes[key] = scope
			if err = recoveryEvidence(tx, "legacy-document", key, documentRaw, filepath.Base(path)); err != nil {
				return report, err
			}
		}
		for _, receiptRaw := range checkpoint.Receipts {
			var receipt runtimeReceipt
			if json.Unmarshal(receiptRaw, &receipt) != nil || receipt.RequestID == "" {
				return report, fmt.Errorf("%w: invalid legacy receipt", ErrRecoveryCorrupt)
			}
			key := receipt.WindowID + "|" + receipt.ProjectUUID + "|" + receipt.DocumentUUID
			if scope := scopes[key]; scope != nil {
				scope.Receipts = append(scope.Receipts, &receipt)
			}
			if err = recoveryEvidence(tx, "legacy-receipt", receipt.RequestID, receiptRaw, filepath.Base(path)); err != nil {
				return report, err
			}
			ids[receipt.RequestID] = true
			report.Receipts++
		}
	}
	auditPaths, err := filepath.Glob(filepath.Join(filepath.Dir(path), "*.jsonl"))
	if err != nil {
		return report, err
	}
	for _, auditPath := range auditPaths {
		n, e := recoveryImportAudit(tx, auditPath, ids)
		if e != nil {
			return report, e
		}
		report.AuditRecords += n
	}
	if err = recoveryImportLegacyAttempts(tx, ids); err != nil {
		return report, err
	}
	for key, scope := range scopes {
		payload, index, e := recoveryScopeBytes(scope)
		if e != nil {
			return report, e
		}
		if _, err = tx.Exec(`INSERT OR IGNORE INTO recovery_scopes(key,revision,payload,hash,index_payload,index_hash) VALUES(?,?,?,?,?,?)`, key, scope.Revision, payload, recoveryHash(payload), index, recoveryHash(index)); err != nil {
			return report, err
		}
		report.Scopes++
	}
	if !exists {
		if _, err = tx.Exec(`INSERT INTO recovery_meta(key,value) VALUES(?,?)`, marker, filepath.Base(path)); err != nil {
			return report, err
		}
	}
	if err = tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

func recoveryImportAudit(tx *sql.Tx, path string, ids map[string]bool) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	hasher := sha256.New()
	scanner := bufio.NewScanner(io.TeeReader(f, hasher))
	scanner.Buffer(make([]byte, 64*1024), 64*1024*1024)
	count := 0
	for scanner.Scan() {
		raw := append([]byte(nil), scanner.Bytes()...)
		if len(raw) == 0 {
			continue
		}
		var entry auditEntry
		if json.Unmarshal(raw, &entry) != nil || entry.RequestID == "" {
			return 0, fmt.Errorf("%w: invalid legacy audit row", ErrRecoveryCorrupt)
		}
		if err = recoveryEvidence(tx, "legacy-audit", entry.RequestID, raw, filepath.Base(path)); err != nil {
			return 0, err
		}
		ids[entry.RequestID] = true
		count++
	}
	if err = scanner.Err(); err != nil {
		return 0, err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO recovery_meta(key,value) VALUES(?,?)`, "audit:"+hex.EncodeToString(hasher.Sum(nil)), filepath.Base(path))
	return count, err
}

// Audit-only installations still retain every previous request ID.
func (s *recoveryStore) ImportAuditDir(dir string) (int, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return 0, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	count := 0
	ids := map[string]bool{}
	for _, path := range paths {
		n, e := recoveryImportAudit(tx, path, ids)
		if e != nil {
			return 0, e
		}
		count += n
	}
	if err = recoveryImportLegacyAttempts(tx, ids); err != nil {
		return 0, err
	}
	if err = tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

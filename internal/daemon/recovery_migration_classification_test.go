package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func migrationClassificationStore(t *testing.T, receipts []*runtimeReceipt, entries ...auditEntry) (*recoveryStore, string) {
	t.Helper()
	dir := t.TempDir()
	checkpoint, err := json.Marshal(runtimeCheckpoint{Version: 1, Documents: map[string]*runtimeDocument{}, Receipts: receipts})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "runtime.json")
	if err := os.WriteFile(path, checkpoint, 0600); err != nil {
		t.Fatal(err)
	}
	var audit []byte
	for _, entry := range entries {
		raw, err := json.Marshal(entry)
		if err != nil {
			t.Fatal(err)
		}
		audit = append(append(audit, raw...), '\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "audit.jsonl"), audit, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openRecoveryStore(filepath.Join(dir, "recovery.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, path
}

func TestRecoveryMigrationHistoricalProjectCreateSettled(t *testing.T) {
	entry := auditEntry{RequestID: "created-project", WindowID: "w", Action: "project.create", OK: true,
		Payload: map[string]any{"friendlyName": "synthetic project"},
		Result:  map[string]any{"uuid": "new-project", "friendlyName": "synthetic project", "projectName": nil, "created": true, "opened": false},
		Context: &protocol.Context{ProjectUUID: "previous-project", DocumentUUID: "previous-document"}}
	receipt := &runtimeReceipt{RequestID: entry.RequestID, WindowID: entry.WindowID, Action: entry.Action,
		ProjectUUID: entry.Context.ProjectUUID, DocumentUUID: entry.Context.DocumentUUID, Status: "action_finished"}
	store, path := migrationClassificationStore(t, []*runtimeReceipt{receipt}, entry)
	if _, err := store.ImportLegacy(path); err != nil {
		t.Fatal(err)
	}
	attempt, err := store.LoadAttempt(entry.RequestID)
	if err != nil || attempt == nil {
		t.Fatal("missing imported attempt", err)
	}
	if attempt.State != recoveryAttemptSettled || !attempt.ExecutionKnown || !attempt.Executed {
		t.Fatalf("confirmed historical project.create remains unresolved: state=%s known=%v executed=%v", attempt.State, attempt.ExecutionKnown, attempt.Executed)
	}
	if !attempt.EvidenceUnknown || attempt.ConnectionEpoch != "" || attempt.Receipt.Status != receipt.Status {
		t.Fatal("historical success acquired fresh authority or changed the original receipt")
	}
}

func migrationSuccessfulBatch() auditEntry {
	return auditEntry{RequestID: "batch", WindowID: "w", Action: "debug.batch", OK: true,
		Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"},
		Payload: map[string]any{"_target": map[string]any{"projectUuid": "p", "documentUuid": "d"}, "steps": []any{
			map[string]any{"action": "schematic.component.modify", "payload": map[string]any{"primitiveId": "part", "patch": map[string]any{"x": 1}}},
		}},
		Result: map[string]any{"ok": true, "partial": false, "paused": false, "cancelled": false, "completed": 1, "total": 1,
			"rules": map[string]any{"status": "fail"}, "remaining": []any{"old-task"},
			"results": []any{map[string]any{"index": 0, "action": "schematic.component.modify", "ok": true, "result": map[string]any{"component": map[string]any{"primitiveId": "part"}}}}}}
}

func TestRecoveryMigrationExecutionClassification(t *testing.T) {
	tests := []struct {
		name   string
		change func(*auditEntry)
		known  bool
	}{
		{"complete-steps-ignore-old-rules-and-remaining", func(*auditEntry) {}, true},
		{"failed-outer", func(e *auditEntry) { e.OK = false }, false},
		{"partial", func(e *auditEntry) { e.Result["partial"] = true }, false},
		{"paused", func(e *auditEntry) { e.Result["paused"] = true }, false},
		{"cancelled", func(e *auditEntry) { e.Result["cancelled"] = true }, false},
		{"missing-completed", func(e *auditEntry) { delete(e.Result, "completed") }, false},
		{"fractional-completed", func(e *auditEntry) { e.Result["completed"] = 1.5 }, false},
		{"wrong-total", func(e *auditEntry) { e.Result["total"] = 2 }, false},
		{"missing-results", func(e *auditEntry) { delete(e.Result, "results") }, false},
		{"wrong-step-action", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Result["results"])[0])["action"] = "schematic.wire.create"
		}, false},
		{"failed-step", func(e *auditEntry) { runtimeMap(runtimeArray(e.Result["results"])[0])["ok"] = false }, false},
		{"fractional-index", func(e *auditEntry) { runtimeMap(runtimeArray(e.Result["results"])[0])["index"] = 0.5 }, false},
		{"negative-index", func(e *auditEntry) { runtimeMap(runtimeArray(e.Result["results"])[0])["index"] = -1 }, false},
		{"partial-child", func(e *auditEntry) {
			runtimeMap(runtimeMap(runtimeArray(e.Result["results"])[0])["result"])["partial"] = true
		}, false},
		{"unverified-child", func(e *auditEntry) {
			runtimeMap(runtimeMap(runtimeArray(e.Result["results"])[0])["result"])["verified"] = false
		}, false},
		{"missing-child-result", func(e *auditEntry) { delete(runtimeMap(runtimeArray(e.Result["results"])[0]), "result") }, false},
		{"malformed-child-context", func(e *auditEntry) { runtimeMap(runtimeArray(e.Result["results"])[0])["context"] = "invalid" }, false},
		{"no-before-target", func(e *auditEntry) { delete(e.Payload, "_target") }, false},
		{"child-complete-before-target", func(e *auditEntry) {
			delete(e.Payload, "_target")
			runtimeMap(runtimeMap(runtimeArray(e.Payload["steps"])[0])["payload"])["_target"] = map[string]any{"projectUuid": "p", "documentUuid": "d"}
		}, true},
		{"malformed-step-payload", func(e *auditEntry) { runtimeMap(runtimeArray(e.Payload["steps"])[0])["payload"] = "invalid" }, false},
		{"unknown-step-action", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Payload["steps"])[0])["action"] = "not-a-handler"
			runtimeMap(runtimeArray(e.Result["results"])[0])["action"] = "not-a-handler"
		}, false},
		{"nested-batch", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Payload["steps"])[0])["action"] = "debug.batch"
			runtimeMap(runtimeArray(e.Result["results"])[0])["action"] = "debug.batch"
		}, false},
		{"target-mismatch", func(e *auditEntry) { e.Context.ProjectUUID = "other-project" }, false},
		{"document-target-mismatch", func(e *auditEntry) { e.Context.DocumentUUID = "other-document" }, false},
		{"child-other-project", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Result["results"])[0])["context"] = map[string]any{"projectUuid": "other-project", "documentUuid": "other-document"}
			runtimeMap(runtimeMap(runtimeArray(e.Payload["steps"])[0])["payload"])["_target"] = map[string]any{"projectUuid": "other-project", "documentUuid": "other-document"}
		}, false},
		{"child-target-other-project", func(e *auditEntry) {
			runtimeMap(runtimeMap(runtimeArray(e.Payload["steps"])[0])["payload"])["_target"] = map[string]any{"projectUuid": "other-project", "documentUuid": "other-document"}
		}, false},
		{"child-result-other-project", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Result["results"])[0])["context"] = map[string]any{"projectUuid": "other-project", "documentUuid": "other-document"}
		}, false},
		{"child-other-document", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Result["results"])[0])["context"] = map[string]any{"projectUuid": "p", "documentUuid": "other-document"}
		}, false},
		{"child-target-other-document", func(e *auditEntry) {
			runtimeMap(runtimeMap(runtimeArray(e.Payload["steps"])[0])["payload"])["_target"] = map[string]any{"projectUuid": "p", "documentUuid": "other-document"}
		}, false},
		{"save-step-without-saved", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Payload["steps"])[0])["action"] = "schematic.save"
			runtimeMap(runtimeArray(e.Result["results"])[0])["action"] = "schematic.save"
		}, false},
		{"save-step-saved", func(e *auditEntry) {
			runtimeMap(runtimeArray(e.Payload["steps"])[0])["action"] = "schematic.save"
			runtimeMap(runtimeArray(e.Result["results"])[0])["action"] = "schematic.save"
			runtimeMap(runtimeMap(runtimeArray(e.Result["results"])[0])["result"])["saved"] = true
		}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			entry := migrationSuccessfulBatch()
			tt.change(&entry)
			store, path := migrationClassificationStore(t, nil, entry)
			if _, err := store.ImportLegacy(path); tt.name == "target-mismatch" || tt.name == "child-other-project" || tt.name == "child-target-other-project" || tt.name == "child-result-other-project" {
				if !errors.Is(err, ErrRecoveryCorrupt) {
					t.Fatal("cross-project target ambiguity did not fail closed", err)
				}
				var count int
				if e := store.db.QueryRow(`SELECT count(*) FROM recovery_attempts`).Scan(&count); e != nil || count != 0 {
					t.Fatal("cross-project ambiguity partially committed", e)
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			attempt, err := store.LoadAttempt(entry.RequestID)
			if err != nil || attempt == nil {
				t.Fatal("missing candidate", err)
			}
			if (attempt.State == recoveryAttemptSettled) != tt.known || attempt.ExecutionKnown != tt.known || attempt.Executed != tt.known || !attempt.EvidenceUnknown || attempt.ConnectionEpoch != "" {
				t.Fatalf("classification state=%s known=%v executed=%v, want known=%v", attempt.State, attempt.ExecutionKnown, attempt.Executed, tt.known)
			}
			if tt.name == "document-target-mismatch" && (attempt.Context.ProjectUUID != "p" || !attempt.ProjectWide) {
				t.Fatal("conflicting documents lost their common project guard")
			}
			if tt.name == "child-other-document" || tt.name == "child-target-other-document" {
				runtime := newRuntimeStore()
				runtime.recovery = store
				for _, document := range []string{"d", "other-document"} {
					req := protocol.Request{Envelope: protocol.Envelope{ID: "continuation", WindowID: "fresh-window"}, Action: "schematic.component.modify", Payload: map[string]any{"_taskParent": "old"}}
					if err := runtime.checkExecutionScopeLocked(req, &protocol.Context{ProjectUUID: "p", DocumentUUID: document}); !errors.Is(err, errRuntimeScopeUnknown) {
						t.Fatal("child identity ambiguity released one document in a new window", err)
					}
				}
			}
		})
	}
}

func TestRecoveryMigrationCombinesAllSourcesBeforeFirstInsert(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*auditEntry)
		known  bool
	}{
		{"identical-response", func(*auditEntry) {}, true},
		{"duplicate-refusal", func(e *auditEntry) { e.OK, e.Result, e.ErrorCode = false, nil, "DUPLICATE_REQUEST" }, true},
		{"different-outcome", func(e *auditEntry) { e.Result["partial"] = true }, false},
		{"different-payload", func(e *auditEntry) { e.Payload["unrelated"] = true }, false},
		{"different-target", func(e *auditEntry) {
			e.Context.ProjectUUID = "q"
			runtimeMap(e.Payload["_target"])["projectUuid"] = "q"
		}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry, other := migrationSuccessfulBatch(), migrationSuccessfulBatch()
			tt.change(&other)
			store, path := migrationClassificationStore(t, nil, entry)
			raw, err := json.Marshal(other)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(filepath.Join(filepath.Dir(path), "later.jsonl"), append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err = store.ImportLegacy(path); tt.name == "different-target" {
				if !errors.Is(err, ErrRecoveryCorrupt) {
					t.Fatal("cross-project multi-source ambiguity did not fail closed", err)
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			attempt, err := store.LoadAttempt(entry.RequestID)
			if err != nil || attempt == nil || (attempt.State == recoveryAttemptSettled) != tt.known {
				t.Fatalf("all-source classification mismatch, want known=%v: %v", tt.known, err)
			}
			var count int
			if err = store.db.QueryRow(`SELECT count(*) FROM recovery_evidence WHERE kind='legacy-classification' AND record_id=?`, entry.RequestID).Scan(&count); err != nil || count != 1 {
				t.Fatal("classification provenance missing", err)
			}
		})
	}
}

func TestRecoveryMigrationExistingUnknownNeverUpgraded(t *testing.T) {
	entry := migrationSuccessfulBatch()
	store, path := migrationClassificationStore(t, nil, entry)
	existing := recoveryAttempt{RequestID: entry.RequestID, Request: protocol.Request{Envelope: protocol.Envelope{ID: entry.RequestID, WindowID: "new-window"}, Action: "debug.exec_js"}, State: recoveryAttemptUnknown, WindowID: "new-window", Context: protocol.Context{ProjectUUID: "new-project"}}
	if err := store.SaveAttempt(&existing); err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(existing)
	for i := 0; i < 2; i++ {
		if _, err := store.ImportLegacy(path); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := store.LoadAttempt(entry.RequestID)
	after, _ := json.Marshal(actual)
	if err != nil || string(before) != string(after) {
		t.Fatal("import overwrote an existing current attempt", err)
	}
	var count int
	if err = store.db.QueryRow(`SELECT count(*) FROM recovery_evidence WHERE kind='legacy-classification' AND record_id=?`, entry.RequestID).Scan(&count); err != nil || count != 0 {
		t.Fatal("existing attempt was reclassified", err)
	}
}

func TestRecoveryMigrationTypedOutcomeContracts(t *testing.T) {
	for _, tt := range []struct {
		name    string
		action  string
		payload map[string]any
		result  map[string]any
		known   bool
	}{
		{"place", "schematic.component.place", map[string]any{}, map[string]any{"primitiveId": "part", "component": map[string]any{"primitiveId": "part"}}, true},
		{"place-other-instance", "schematic.component.place", map[string]any{}, map[string]any{"primitiveId": "part", "component": map[string]any{"primitiveId": "other"}}, false},
		{"modify", "schematic.component.modify", map[string]any{"primitiveId": "part", "patch": map[string]any{}}, map[string]any{"component": map[string]any{"primitiveId": "part"}}, true},
		{"modify-other-instance", "schematic.component.modify", map[string]any{"primitiveId": "part", "patch": map[string]any{}}, map[string]any{"component": map[string]any{"primitiveId": "other"}}, false},
		{"modify-missing-patch", "schematic.component.modify", map[string]any{"primitiveId": "part"}, map[string]any{"component": map[string]any{"primitiveId": "part"}}, false},
		{"wire", "schematic.wire.create", map[string]any{}, map[string]any{"primitiveId": "wire", "line": []any{0, 0, 10, 10}}, true},
		{"wire-no-line", "schematic.wire.create", map[string]any{}, map[string]any{"primitiveId": "wire"}, false},
		{"labels", "schematic.wire.labels.apply", map[string]any{"wireIds": []any{"wire"}, "labels": []any{}}, map[string]any{"wireIds": []any{"wire"}, "verified": true, "partial": false, "remaining": []any{}}, true},
		{"labels-other-wire", "schematic.wire.labels.apply", map[string]any{"wireIds": []any{"wire"}, "labels": []any{}}, map[string]any{"wireIds": []any{"other"}, "verified": true, "partial": false, "remaining": []any{}}, false},
		{"delete", "schematic.primitives.delete", map[string]any{"primitiveIds": []any{"wire", "wire"}}, map[string]any{"requested": 1, "total": 1, "deletedIds": map[string]any{"wires": []any{"wire"}}, "deleted": map[string]any{"wires": 1}}, true},
		{"delete-other-id", "schematic.primitives.delete", map[string]any{"primitiveIds": []any{"wire"}}, map[string]any{"requested": 1, "total": 1, "deletedIds": map[string]any{"wires": []any{"other"}}, "deleted": map[string]any{"wires": 1}}, false},
		{"delete-survivor", "schematic.primitives.delete", map[string]any{"primitiveIds": []any{"wire"}}, map[string]any{"requested": 1, "total": 1, "deletedIds": map[string]any{"wires": []any{"wire"}}, "deleted": map[string]any{"wires": 1}, "survived": map[string]any{"wires": []any{"wire"}}}, false},
		{"save", "schematic.save", map[string]any{}, map[string]any{"saved": true}, true},
		{"save-unknown", "schematic.save", map[string]any{}, map[string]any{"saved": nil}, false},
		{"unsupported-script", "debug.exec_js", map[string]any{"script": "synthetic"}, map[string]any{}, false},
		{"unsupported-write-dryrun", "schematic.page.clear", map[string]any{"dryRun": true}, map[string]any{}, false},
		{"malformed-target", "schematic.save", map[string]any{"_target": []any{}}, map[string]any{"saved": true}, false},
		{"partial-target", "schematic.save", map[string]any{"_target": map[string]any{"documentUuid": "d"}}, map[string]any{"saved": true}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, present := tt.payload["_target"]; !present {
				tt.payload["_target"] = map[string]any{"projectUuid": "p", "documentUuid": "d"}
			}
			entry := auditEntry{RequestID: "typed", WindowID: "w", Action: tt.action, OK: true, Payload: tt.payload, Result: tt.result, Context: &protocol.Context{ProjectUUID: "p", DocumentUUID: "d"}}
			raw, err := json.Marshal(entry)
			if err != nil || json.Unmarshal(raw, &entry) != nil {
				t.Fatal("invalid synthetic entry", err)
			}
			if known := legacyAuditSuccess(entry); known != tt.known {
				t.Fatalf("typed outcome known=%v, want %v", known, tt.known)
			}
		})
	}
}

func TestRecoveryMigrationProjectCreateContracts(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*auditEntry)
		known  bool
	}{
		{"open-new-project", func(*auditEntry) {}, true},
		{"missing-uuid", func(e *auditEntry) { delete(e.Result, "uuid") }, false},
		{"created-false", func(e *auditEntry) { e.Result["created"] = false }, false},
		{"opened-false", func(e *auditEntry) { e.Result["opened"] = false }, false},
		{"other-open-project", func(e *auditEntry) { e.Context.ProjectUUID = "other-project" }, false},
		{"partial-create", func(e *auditEntry) { e.Result["partial"] = true }, false},
		{"same-name-wrong-result", func(e *auditEntry) { e.Result["friendlyName"] = "other-name" }, false},
		{"malformed-result-name", func(e *auditEntry) {
			e.Payload["projectName"] = map[string]any{}
			e.Result["projectName"] = map[string]any{}
		}, false},
		{"optional-name-normalized", func(e *auditEntry) { e.Payload["projectName"] = map[string]any{} }, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := auditEntry{RequestID: "create", WindowID: "w", Action: "project.create", OK: true,
				Payload: map[string]any{"friendlyName": "synthetic", "open": true}, Result: map[string]any{"uuid": "created", "friendlyName": "synthetic", "projectName": nil, "created": true, "opened": true},
				Context: &protocol.Context{ProjectUUID: "created"}}
			tt.change(&entry)
			if known := legacyAuditSuccess(entry); known != tt.known {
				t.Fatalf("create outcome known=%v, want %v", known, tt.known)
			}
		})
	}
}

func TestRecoveryMigrationProjectCreatePreAndPostIdentity(t *testing.T) {
	for _, tt := range []struct {
		name     string
		document string
		change   func(*auditEntry)
		known    bool
	}{
		{"new-project-with-document", "new-document", func(*auditEntry) {}, true},
		{"new-project-without-document", "", func(*auditEntry) {}, true},
		{"post-context-conflicts", "", func(e *auditEntry) { e.Context.ProjectUUID = "other-project" }, false},
		{"result-uuid-replaced", "", func(e *auditEntry) { e.Result["uuid"] = "other-project" }, false},
		{"pre-target-conflicts-with-receipt", "", func(e *auditEntry) { runtimeMap(e.Payload["_target"])["projectUuid"] = "other-before" }, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			entry := auditEntry{RequestID: "transition-create", WindowID: "w", Action: "project.create", OK: true,
				Payload: map[string]any{"friendlyName": "synthetic", "open": true, "_target": map[string]any{"projectUuid": "before-project", "documentUuid": "before-document"}},
				Result:  map[string]any{"uuid": "created-project", "friendlyName": "synthetic", "projectName": nil, "created": true, "opened": true},
				Context: &protocol.Context{ProjectUUID: "created-project", DocumentUUID: tt.document}}
			tt.change(&entry)
			receipt := &runtimeReceipt{RequestID: entry.RequestID, WindowID: entry.WindowID, Action: entry.Action, Status: "action_finished", ProjectUUID: "before-project", DocumentUUID: "before-document"}
			store, path := migrationClassificationStore(t, []*runtimeReceipt{receipt}, entry)
			if _, err := store.ImportLegacy(path); tt.name == "pre-target-conflicts-with-receipt" {
				if !errors.Is(err, ErrRecoveryCorrupt) {
					t.Fatal("conflicting pre-project evidence did not fail closed", err)
				}
				return
			} else if err != nil {
				t.Fatal(err)
			}
			attempt, err := store.LoadAttempt(entry.RequestID)
			if err != nil || attempt == nil || (attempt.State == recoveryAttemptSettled) != tt.known || attempt.ExecutionKnown != tt.known {
				t.Fatalf("pre/post classification mismatch: want known=%v: %v", tt.known, err)
			}
			if tt.known && (attempt.Context.ProjectUUID != "created-project" || attempt.Context.DocumentUUID != tt.document) {
				t.Fatal("successful creation was not linked to its actual new UUID")
			}
			if !tt.known && (attempt.Context.ProjectUUID != runtimeString(entry.Result["uuid"]) || !attempt.ProjectWide) {
				t.Fatal("conflicting post-context released the actual created project")
			}
			if attempt.Receipt.ProjectUUID != receipt.ProjectUUID || attempt.Receipt.DocumentUUID != receipt.DocumentUUID || !attempt.EvidenceUnknown || attempt.ConnectionEpoch != "" {
				t.Fatal("creation rewrote pre-context or granted fresh authority")
			}
		})
	}
}

func TestRecoveryMigrationCrossWindowConflict(t *testing.T) {
	for _, otherProject := range []bool{false, true} {
		entry, other := migrationSuccessfulBatch(), migrationSuccessfulBatch()
		other.WindowID = "other-window"
		if otherProject {
			other.Context.ProjectUUID = "other-project"
			runtimeMap(other.Payload["_target"])["projectUuid"] = "other-project"
		}
		store, path := migrationClassificationStore(t, nil, entry, other)
		_, err := store.ImportLegacy(path)
		if otherProject {
			if !errors.Is(err, ErrRecoveryCorrupt) {
				t.Fatal("unrepresentable cross-window/project conflict did not fail closed", err)
			}
			var count int
			if e := store.db.QueryRow(`SELECT count(*) FROM recovery_attempts`).Scan(&count); e != nil || count != 0 {
				t.Fatal("failed migration partially committed", e)
			}
			continue
		}
		if err != nil {
			t.Fatal("same-project conflict should retain a representable guard", err)
		}
		attempt, err := store.LoadAttempt(entry.RequestID)
		if err != nil || attempt == nil || attempt.State != recoveryAttemptUnknown || !attempt.ProjectWide || attempt.Context.ProjectUUID != "p" {
			t.Fatal("same-project cross-window conflict lost its project guard", err)
		}
	}
}

func TestRecoveryMigrationAuditOnlyHistoricalSuccess(t *testing.T) {
	entry := migrationSuccessfulBatch()
	store, path := migrationClassificationStore(t, nil, entry)
	for i := 0; i < 2; i++ {
		if _, err := store.ImportAuditDir(filepath.Dir(path)); err != nil {
			t.Fatal(err)
		}
	}
	attempt, err := store.LoadAttempt(entry.RequestID)
	if err != nil || attempt == nil || attempt.State != recoveryAttemptSettled || attempt.Revision != 1 || !attempt.EvidenceUnknown {
		t.Fatal("audit-only migration did not classify exactly once", err)
	}
}

func TestRecoveryMigrationScriptObservationDoesNotBecomeWriteTarget(t *testing.T) {
	for _, withReceipt := range []bool{false, true} {
		entry := auditEntry{RequestID: "unknown-script", WindowID: "old-window", Action: "debug.exec_js", OK: true,
			Payload: map[string]any{"script": "synthetic", "_target": map[string]any{"projectUuid": "requested-project", "documentUuid": "requested-document"}},
			Result:  map[string]any{"synthetic": true}, Context: &protocol.Context{ProjectUUID: "observed-project"}}
		var receipts []*runtimeReceipt
		if withReceipt {
			receipts = []*runtimeReceipt{{RequestID: entry.RequestID, WindowID: entry.WindowID, Action: entry.Action, Status: "action_finished", ContentWrite: true,
				ProjectUUID: "observed-project", StartedContext: &protocol.Context{ProjectUUID: "requested-project", DocumentUUID: "requested-document"}}}
		}
		store, path := migrationClassificationStore(t, receipts, entry)
		if _, err := store.ImportLegacy(path); err != nil {
			t.Fatal("post-execution observation was treated as a second write target", err)
		}
		attempt, err := store.LoadAttempt(entry.RequestID)
		if err != nil || attempt == nil || attempt.State != recoveryAttemptUnknown || attempt.ExecutionKnown || attempt.Executed || attempt.Context.ProjectUUID != "requested-project" || !attempt.ProjectWide {
			t.Fatal("unsupported script lost the original project guard or became known", err)
		}
		runtime := newRuntimeStore()
		runtime.recovery = store
		req := protocol.Request{Envelope: protocol.Envelope{ID: "continuation", WindowID: "fresh-window"}, Action: "schematic.component.modify", Payload: map[string]any{"_taskParent": "old"}}
		if err := runtime.checkExecutionScopeLocked(req, &protocol.Context{ProjectUUID: "requested-project", DocumentUUID: "fresh-document"}); !errors.Is(err, errRuntimeScopeUnknown) {
			t.Fatal("unknown script did not guard the requested project across windows", err)
		}
		if err := runtime.checkExecutionScopeLocked(req, &protocol.Context{ProjectUUID: "observed-project", DocumentUUID: "other-document"}); err != nil {
			t.Fatal("post-execution observation became an unproved write scope", err)
		}
	}
}

func TestRecoveryMigrationScriptDifferentDeclaredTargetsFailClosed(t *testing.T) {
	first := auditEntry{RequestID: "ambiguous-script", WindowID: "w", Action: "debug.exec_js", OK: true,
		Payload: map[string]any{"script": "synthetic", "_target": map[string]any{"projectUuid": "first-project", "documentUuid": "first-document"}},
		Result:  map[string]any{"synthetic": true}, Context: &protocol.Context{ProjectUUID: "observed-project"}}
	second := first
	second.Payload = map[string]any{"script": "synthetic", "_target": map[string]any{"projectUuid": "second-project", "documentUuid": "second-document"}}
	store, path := migrationClassificationStore(t, nil, first, second)
	if _, err := store.ImportLegacy(path); !errors.Is(err, ErrRecoveryCorrupt) {
		t.Fatal("different explicit script targets did not fail closed", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT count(*) FROM recovery_attempts`).Scan(&count); err != nil || count != 0 {
		t.Fatal("ambiguous declared targets partially committed", err)
	}
}

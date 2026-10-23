package daemon

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

func TestRuntimeManualAcceptanceAuthorizedBackup(t *testing.T) {
	dir, checkpoint := legacyDispatchBackup(t)
	store, err := openRecoveryStore(filepath.Join(dir, "recovery-0.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	report, err := store.ImportLegacy(checkpoint)
	if err != nil || report.Scopes != 8 || report.Receipts != 256 || report.AuditRecords != 3129 {
		t.Fatalf("backup import counts: %+v %v", report, err)
	}
	old, err := store.LoadAttempt("21474e7f-197a-46b3-b383-35c02eca29b2")
	if err != nil || old == nil || old.State != recoveryAttemptUnknown {
		t.Fatal("old unknown unavailable", err)
	}
	var originalAttempt []byte
	if err := store.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id=?`, old.RequestID).Scan(&originalAttempt); err != nil {
		t.Fatal(err)
	}
	originalScopes := map[string][]byte{}
	scopeRows, err := store.db.Query(`SELECT key,payload FROM recovery_scopes`)
	if err != nil {
		t.Fatal(err)
	}
	for scopeRows.Next() {
		var key string
		var raw []byte
		if err := scopeRows.Scan(&key, &raw); err != nil {
			t.Fatal(err)
		}
		originalScopes[key] = raw
	}
	if err := scopeRows.Close(); err != nil {
		t.Fatal(err)
	}
	rows := legacyDispatchRows(t, dir)
	ctx := rows[legacyDispatchSaveID].Context
	if ctx == nil || ctx.ProjectUUID == "" || ctx.DocumentUUID == "" {
		t.Fatal("backup save target unavailable")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	s, base := acceptanceServer(t, dir)
	activeDocument, paused := ctx.DocumentUUID, false
	_, calls := acceptanceConnector(t, s, base, "manual-backup-current", ctx.ProjectUUID, ctx.DocumentUUID, func(req protocol.Request) protocol.Response {
		result := map[string]any{"_scene": map[string]any{"instance": "manual-backup-fresh-scene", "sequence": 0}}
		switch req.Action {
		case "debug.control":
			if req.Payload["operation"] == "pause" {
				paused = true
			}
			if req.Payload["operation"] == "resume" {
				paused = false
			}
			result["targetChecked"], result["paused"], result["pending"], result["inFlight"] = true, paused, 0, []string{}
		case "project.current":
			result["uuid"] = ctx.ProjectUUID
		case "schematic.pages.list":
			page := map[string]any{"uuid": ctx.DocumentUUID, "name": "accepted test page", "parentSchematicUuid": "test-only-sch"}
			result["schematics"], result["pages"] = []any{map[string]any{"uuid": "test-only-sch", "name": "test-only-sch", "parentProjectUuid": ctx.ProjectUUID, "page": []any{page}}}, []any{page}
		case "board.list":
			result["boards"], result["count"] = []any{map[string]any{"name": "test-only-board", "schematicUuid": "test-only-sch", "pcbUuid": "test-only-pcb", "parentProjectUuid": ctx.ProjectUUID}}, 1
		case "schematic.save":
			result["saved"] = true
		case "document.close":
			result["closed"], result["uuid"], result["tabId"] = true, ctx.DocumentUUID, "test-only-tab"
			activeDocument = "test-only-other"
		case "document.open":
			result["ready"], result["tabId"] = true, "test-only-tab"
			activeDocument = ctx.DocumentUUID
		default:
			result["modified"] = true
		}
		return protocol.Response{OK: true, Result: result, Context: &protocol.Context{ProjectUUID: ctx.ProjectUUID, DocumentUUID: activeDocument, DocumentType: "schematic", TabID: "test-only-tab"}}
	})
	target := map[string]any{"projectUuid": ctx.ProjectUUID, "documentUuid": ctx.DocumentUUID}
	init := acceptanceCall(s, map[string]any{"operation": "task_update", "window": "manual-backup-current", "target": target, "expectedRevision": 0, "task": map[string]any{"goal": "test-only preliminary intent", "primitiveIds": []string{}, "remaining": []string{"review baseline"}}})
	if init.Code != 200 {
		t.Fatalf("init: %d %s", init.Code, init.Body.String())
	}
	preview := acceptanceCall(s, map[string]any{"operation": "task_accept_current_baseline", "window": "manual-backup-current", "target": target, "acceptance": map[string]any{"acceptCurrentState": false, "requestIds": []string{old.RequestID}}})
	var observed map[string]any
	if preview.Code != 200 || json.Unmarshal(preview.Body.Bytes(), &observed) != nil {
		t.Fatalf("preview: %d %s", preview.Code, preview.Body.String())
	}
	input := map[string]any{"operation": "task_accept_current_baseline", "window": "manual-backup-current", "target": target, "expectedRevision": 1, "generation": runtimeMap(observed["current"])["generation"], "task": map[string]any{"goal": "new bounded test-only page edit", "primitiveIds": []string{}, "remaining": []string{"write", "save", "close", "reopen"}}, "acceptance": map[string]any{"acceptCurrentState": true, "connectionEpoch": runtimeMap(observed["current"])["connectionEpoch"], "expectedBaselineSha256": observed["baselineSha256"], "attempts": observed["attempts"], "unsavedScopes": observed["unsavedScopes"], "reason": "test-only explicit acceptance from authorized backup", "nativeEndEvidence": map[string]any{"type": "manual_session_termination_attestation", "attestedBy": "user", "oldWindow": old.WindowID, "statement": "Test-only representation of explicit user old-session termination attestation; no real EDA was opened", "terminal": []any{map[string]any{"requestId": old.RequestID, "statusRequestId": "41942c61-0b5a-4438-a572-2f5098272206", "sha256": "c33dad0434df11633b58d3af35f3292572ee635a858927ca4ea307835338f7aa", "initialStatusRequestId": "e62537fc-23d3-464d-8eac-d0ef8bb9e2f1", "initialSha256": "6bc6740ff8a5223817e5549acf027a1cebca402fb563ad59055a09a2aaa303cf"}}}}}
	accepted := acceptanceCall(s, input)
	if accepted.Code != 200 {
		t.Fatalf("real evidence acceptance: %d %s", accepted.Code, accepted.Body.String())
	}
	if resumed := runtimeTestControl(s, `{"operation":"resume_queue","window":"manual-backup-current"}`); resumed.Code != 200 {
		t.Fatal("explicit new queue resume failed")
	}
	before := calls.Load()
	for _, id := range []string{old.RequestID, "new-raw-fake-preview"} {
		response := acceptanceAction(t, s, id, "manual-backup-current", "debug.exec_js", map[string]any{"dryRun": true, "code": "not executed", "_target": target})
		if response.OK || calls.Load() != before {
			t.Fatal("old request or new raw dryRun dispatched")
		}
	}
	write := acceptanceAction(t, s, "manual-backup-new-write", "manual-backup-current", "schematic.component.modify", map[string]any{"primitiveId": "test-only-part", "_target": target})
	if !write.OK {
		t.Fatalf("new scoped write rejected: %+v", write.Error)
	}
	save := acceptanceAction(t, s, "manual-backup-new-save", "manual-backup-current", "schematic.save", map[string]any{"_target": target})
	if !save.OK {
		t.Fatalf("new scoped save rejected: %+v", save.Error)
	}
	acceptanceWantOK(t, acceptanceAction(t, s, "manual-backup-new-close", "manual-backup-current", "document.close", map[string]any{"uuid": ctx.DocumentUUID, "tabId": "test-only-tab", "_target": target}))
	acceptanceWantOK(t, acceptanceAction(t, s, "manual-backup-new-open", "manual-backup-current", "document.open", map[string]any{"uuid": ctx.DocumentUUID, "_target": map[string]any{"projectUuid": ctx.ProjectUUID, "documentUuid": "test-only-other"}}))
	retained, _ := s.runtime.recovery.LoadAttempt(old.RequestID)
	if retained.State != recoveryAttemptUnknown || retained.Revision != old.Revision {
		t.Fatal("real legacy unknown was settled")
	}
	activeStore := s.runtime.recovery.(*recoveryStore)
	var retainedRaw []byte
	if err := activeStore.db.QueryRow(`SELECT payload FROM recovery_attempts WHERE request_id=?`, old.RequestID).Scan(&retainedRaw); err != nil || !bytes.Equal(originalAttempt, retainedRaw) {
		t.Fatal("original unknown bytes changed", err)
	}
	for key, raw := range originalScopes {
		var retained []byte
		if err := activeStore.db.QueryRow(`SELECT payload FROM recovery_scopes WHERE key=?`, key).Scan(&retained); err != nil || !bytes.Equal(raw, retained) {
			t.Fatal("original scope/task/receipt bytes changed", err)
		}
	}
}

package daemon

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zhoushoujianwork/easyeda-agent/internal/protocol"
)

const legacyClassificationVersion = "legacy-execution/v1"

type legacySourceReference struct {
	Kind   string `json:"kind"`
	Source string `json:"source"`
	SHA256 string `json:"sha256"`
}

// Classify only a new row, after all original evidence is available. An import
// must never turn an existing unknown (or a newer runtime attempt) into success.
func recoveryImportLegacyAttempts(tx *sql.Tx, ids map[string]bool) error {
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	for _, id := range ordered {
		var exists bool
		if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM recovery_attempts WHERE request_id=?)`, id).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		attempt, refs, reason, err := recoveryLegacyCandidate(tx, id)
		if err != nil {
			return err
		}
		if err = recoveryInsertAttempt(tx, attempt, true); err != nil {
			return err
		}
		createdProject := ""
		if attempt.Request.Action == "project.create" {
			createdProject = runtimeString(runtimeMap(runtimeMap(runtimeClone(attempt.Evidence))["result"])["uuid"])
		}
		provenance, err := json.Marshal(map[string]any{"version": legacyClassificationVersion, "reason": reason, "state": attempt.State,
			"executionKnown": attempt.ExecutionKnown, "evidenceUnknown": true, "sources": refs,
			"createdProjectUuid": createdProject, "scope": attempt.Context})
		if err != nil {
			return err
		}
		if err = recoveryEvidence(tx, "legacy-classification", id, provenance, legacyClassificationVersion); err != nil {
			return err
		}
	}
	return nil
}

func recoveryLegacyCandidate(tx *sql.Tx, id string) (*recoveryAttempt, []legacySourceReference, string, error) {
	a := &recoveryAttempt{RequestID: id, Revision: 1, State: recoveryAttemptUnknown, EvidenceUnknown: true}
	a.Request.ID = id
	rows, err := tx.Query(`SELECT kind,raw,source,hash FROM recovery_evidence WHERE record_id=? AND kind IN ('legacy-receipt','legacy-audit') ORDER BY CASE kind WHEN 'legacy-receipt' THEN 0 ELSE 1 END,rowid`, id)
	if err != nil {
		return nil, nil, "", err
	}
	defer rows.Close()
	var refs []legacySourceReference
	var receiptHash, requestHash, responseHash string
	var createdProject string
	var success *auditEntry
	conflict, rejected := false, false
	mergeScope := func(window string, ctx protocol.Context) error {
		if a.WindowID == "" {
			a.WindowID = window
		}
		if a.WindowID != "" && window != "" && a.WindowID != window {
			conflict = true
			if a.Context.ProjectUUID == "" || ctx.ProjectUUID == "" || a.Context.ProjectUUID != ctx.ProjectUUID {
				return fmt.Errorf("%w: legacy request %s has conflicting windows and projects", ErrRecoveryCorrupt, id)
			}
			a.ProjectWide = true
		}
		if legacyContextConflict(a.Context, ctx) {
			conflict = true
			if a.Context.ProjectUUID != "" && ctx.ProjectUUID != "" && a.Context.ProjectUUID != ctx.ProjectUUID {
				return fmt.Errorf("%w: legacy request %s has conflicting projects", ErrRecoveryCorrupt, id)
			} else {
				a.Context.DocumentUUID = ""
				a.ProjectWide = true
			}
			return nil
		}
		if a.Context.ProjectUUID == "" {
			a.Context.ProjectUUID = ctx.ProjectUUID
		}
		if a.Context.DocumentUUID == "" {
			a.Context.DocumentUUID = ctx.DocumentUUID
		}
		return nil
	}
	for rows.Next() {
		var ref legacySourceReference
		var raw []byte
		if err = rows.Scan(&ref.Kind, &raw, &ref.Source, &ref.SHA256); err != nil {
			return nil, nil, "", err
		}
		if recoveryHash(raw) != ref.SHA256 {
			return nil, nil, "", ErrRecoveryCorrupt
		}
		refs = append(refs, ref)
		if ref.Kind == "legacy-receipt" {
			var receipt runtimeReceipt
			if json.Unmarshal(raw, &receipt) != nil || receipt.RequestID != id {
				return nil, nil, "", ErrRecoveryCorrupt
			}
			if receiptHash != "" && receiptHash != legacyJSONHash(receipt) {
				conflict = true
			}
			receiptHash = legacyJSONHash(receipt)
			if a.Receipt == nil {
				a.Receipt = &receipt
				a.Request.Action = receipt.Action
			}
			a.ContentWrite = a.ContentWrite || receipt.ContentWrite
			if err = mergeScope(receipt.WindowID, protocol.Context{ProjectUUID: receipt.ProjectUUID, DocumentUUID: receipt.DocumentUUID}); err != nil {
				return nil, nil, "", err
			}
			continue
		}
		var entry auditEntry
		if json.Unmarshal(raw, &entry) != nil || entry.RequestID != id {
			return nil, nil, "", ErrRecoveryCorrupt
		}
		request := protocol.Request{Envelope: protocol.Envelope{ID: id, WindowID: entry.WindowID}, Action: entry.Action, Payload: entry.Payload}
		hash := legacyJSONHash(request)
		if requestHash != "" && requestHash != hash {
			conflict = true
		}
		if requestHash == "" {
			a.Request, requestHash = request, hash
		}
		a.ContentWrite = a.ContentWrite || runtimeContentWrite(entry.Action, entry.Payload)
		a.Dangerous = a.Dangerous || runtimeDangerous(request) || entry.Action == "debug.exec_js"
		a.ProjectWide = a.ProjectWide || runtimeProjectWide(request)
		if entry.ErrorCode == "DUPLICATE_REQUEST" && !entry.OK && entry.Result == nil {
			continue // A refused replay has no response context for the original execution.
		}
		ctx := protocol.Context{}
		if entry.Context != nil {
			ctx = *entry.Context
		}
		target := legacyPayloadTarget(entry.Payload)
		scriptTarget := entry.Action == "debug.exec_js" && target.ProjectUUID != "" && target.DocumentUUID != ""
		if scriptTarget && responseHash == "" && !conflict {
			// Unsupported scripts retain the declared target; later observations are not write scopes.
			a.Context = target
		}
		if entry.Action == "project.create" {
			if uuid := runtimeString(entry.Result["uuid"]); entry.Result["created"] == true && uuid != "" {
				if createdProject != "" && createdProject != uuid {
					return nil, nil, "", fmt.Errorf("%w: legacy creation %s has conflicting project UUIDs", ErrRecoveryCorrupt, id)
				}
				createdProject = uuid
			}
			if responseHash == "" && !conflict {
				a.Context = protocol.Context{}
			}
			pre := []protocol.Context{}
			if target.ProjectUUID != "" || target.DocumentUUID != "" {
				pre = append(pre, target)
			}
			if a.Receipt != nil {
				if a.Receipt.StartedContext != nil {
					pre = append(pre, *a.Receipt.StartedContext)
				}
				// A receipt can still name the old document, or already name the
				// created project. Neither requires a new UUID before creation.
				if a.Receipt.ProjectUUID != runtimeString(entry.Result["uuid"]) || entry.Result["created"] != true {
					pre = append(pre, protocol.Context{ProjectUUID: a.Receipt.ProjectUUID, DocumentUUID: a.Receipt.DocumentUUID})
				} else if legacyContextConflict(protocol.Context{ProjectUUID: a.Receipt.ProjectUUID, DocumentUUID: a.Receipt.DocumentUUID}, ctx) {
					conflict = true
				}
			}
			if len(pre) == 0 {
				pre = append(pre, ctx)
			}
			for _, before := range pre {
				if err = mergeScope(entry.WindowID, before); err != nil {
					return nil, nil, "", err
				}
			}
		} else {
			if target.ProjectUUID != "" || target.DocumentUUID != "" {
				if legacyContextConflict(target, ctx) {
					conflict = true
					a.ProjectWide = true
					if target.ProjectUUID != "" && ctx.ProjectUUID != "" && target.ProjectUUID != ctx.ProjectUUID && !scriptTarget {
						return nil, nil, "", fmt.Errorf("%w: legacy request %s target conflicts with its response project", ErrRecoveryCorrupt, id)
					}
				}
				// A response for another target must not release the requested target.
				if target.ProjectUUID != "" {
					ctx.ProjectUUID = target.ProjectUUID
				}
				if target.DocumentUUID != "" {
					ctx.DocumentUUID = target.DocumentUUID
				}
			}
			if err = mergeScope(entry.WindowID, ctx); err != nil {
				return nil, nil, "", err
			}
		}
		if entry.Action == "debug.batch" && a.Dangerous {
			for _, value := range runtimeArray(entry.Payload["steps"]) {
				target := legacyPayloadTarget(runtimeMap(runtimeMap(value)["payload"]))
				if target.ProjectUUID == "" && target.DocumentUUID == "" {
					continue
				}
				if err = mergeScope(entry.WindowID, target); err != nil {
					return nil, nil, "", err
				}
			}
			for _, value := range runtimeArray(entry.Result["results"]) {
				context := runtimeMap(runtimeMap(value)["context"])
				child := protocol.Context{ProjectUUID: runtimeString(context["projectUuid"]), DocumentUUID: runtimeString(context["documentUuid"])}
				if child.ProjectUUID == "" && child.DocumentUUID == "" {
					continue
				}
				if err = mergeScope(entry.WindowID, child); err != nil {
					return nil, nil, "", err
				}
			}
		}
		response := legacyJSONHash(map[string]any{"ok": entry.OK, "result": entry.Result, "errorCode": entry.ErrorCode,
			"error": entry.ErrorMsg, "errorDetail": entry.ErrorDetail, "context": entry.Context})
		if responseHash != "" && responseHash != response {
			conflict = true
		}
		responseHash = response
		if !legacyAuditSuccess(entry) {
			rejected = true
			continue
		}
		if success == nil {
			copy := entry
			success = &copy
		}
	}
	if err = rows.Err(); err != nil {
		return nil, nil, "", err
	}
	a.Request.WindowID = a.WindowID
	// Do not copy a checkpoint's epoch, task basis, or observations into authority.
	a.Dangerous = a.Dangerous || runtimeDangerous(a.Request) || a.ContentWrite
	if a.Receipt != nil && (a.Receipt.Action != a.Request.Action || a.Receipt.WindowID != a.WindowID || a.Receipt.ErrorCode != "" || a.Receipt.Error != "") {
		conflict = true
	}
	if success != nil && a.Receipt != nil && !legacyReceiptSuccess(a.Receipt, *success) {
		conflict = true
	}
	reason := "missing-or-incomplete-execution-evidence"
	if !a.Dangerous && (conflict || rejected || success == nil) {
		a.State = recoveryAttemptSettled
		reason = "non-dangerous-history"
	} else if conflict {
		reason = "conflicting-legacy-evidence"
	} else if rejected {
		reason = "unsuccessful-or-partial-execution"
	} else if success != nil {
		a.State, a.ExecutionKnown, a.Executed = recoveryAttemptSettled, true, true
		a.Evidence, a.UpdatedAt = *success, success.Timestamp
		if success.Action == "project.create" {
			a.Context = protocol.Context{ProjectUUID: runtimeString(success.Result["uuid"])}
			if success.Context != nil && success.Context.ProjectUUID == a.Context.ProjectUUID {
				a.Context = *success.Context
			}
		}
		reason = "matched-historical-success"
	}
	if a.State == recoveryAttemptUnknown && a.Request.Action == "project.create" && createdProject != "" {
		a.Context = protocol.Context{ProjectUUID: createdProject}
		a.ProjectWide = true
	}
	a.ScopeKey = runtimeScopeKey(a.WindowID, a.Context)
	return a, refs, reason, nil
}

func legacyJSONHash(value any) string {
	raw, _ := json.Marshal(value)
	return recoveryHash(raw)
}

func legacyPayloadTarget(payload map[string]any) protocol.Context {
	target := runtimeMap(payload["_target"])
	return protocol.Context{ProjectUUID: runtimeString(target["projectUuid"]), DocumentUUID: runtimeString(target["documentUuid"])}
}

func legacyContextConflict(a, b protocol.Context) bool {
	return a.ProjectUUID != "" && b.ProjectUUID != "" && a.ProjectUUID != b.ProjectUUID || a.DocumentUUID != "" && b.DocumentUUID != "" && a.DocumentUUID != b.DocumentUUID
}

func legacyAuditSuccess(entry auditEntry) bool {
	if !entry.OK || entry.WindowID == "" || !knownActions[entry.Action] || entry.Result == nil || entry.ErrorCode != "" || entry.ErrorMsg != "" || entry.ErrorDetail != "" || !legacyPayloadTargetValid(entry.Payload) {
		return false
	}
	if entry.Action != "project.create" && runtimeDangerous(protocol.Request{Action: entry.Action, Payload: entry.Payload}) && (entry.Context == nil || entry.Context.ProjectUUID == "" || entry.Context.DocumentUUID == "") {
		return false
	}
	return legacyOutcomeSuccess(entry.Action, entry.Payload, entry.Result, entry.Context, protocol.Context{})
}

func legacyOutcomeSuccess(action string, payload, result map[string]any, ctx *protocol.Context, inheritedTarget protocol.Context) bool {
	if !knownActions[action] || !legacyPayloadTargetValid(payload) {
		return false
	}
	target := legacyPayloadTarget(payload)
	if target.ProjectUUID != "" && target.DocumentUUID != "" {
		inheritedTarget = target
	}
	if action != "project.create" && action != "debug.batch" && runtimeDangerous(protocol.Request{Action: action}) && (ctx == nil || inheritedTarget.ProjectUUID == "" || inheritedTarget.DocumentUUID == "" || ctx.ProjectUUID != inheritedTarget.ProjectUUID || ctx.DocumentUUID != inheritedTarget.DocumentUUID) {
		return false
	}
	for key, want := range map[string]bool{"ok": true, "partial": false, "verified": true, "paused": false, "cancelled": false} {
		if value, exists := result[key]; exists && value != want {
			return false
		}
	}
	if result["error"] != nil {
		return false
	}
	if strings.HasSuffix(action, ".save") {
		return result["saved"] == true
	}
	if action == "project.create" {
		uuid, name := runtimeString(result["uuid"]), runtimeString(payload["friendlyName"])
		projectName, hasProjectName := payload["projectName"].(string)
		if uuid == "" || name == "" || result["created"] != true || result["friendlyName"] != name {
			return false
		}
		if hasProjectName && result["projectName"] != projectName || !hasProjectName && result["projectName"] != nil {
			return false
		}
		if payload["open"] == true {
			return result["opened"] == true && ctx != nil && ctx.ProjectUUID == uuid
		}
		return result["opened"] == false
	}
	if action != "debug.batch" {
		return legacyTypedOutcomeSuccess(action, payload, result)
	}
	steps, results := runtimeArray(payload["steps"]), runtimeArray(result["results"])
	if len(steps) == 0 || len(steps) > 500 || len(steps) != len(results) || !legacyExactCount(result["completed"], len(steps)) || !legacyExactCount(result["total"], len(steps)) {
		return false
	}
	seen := map[int]bool{}
	for _, item := range results {
		stepResult := runtimeMap(item)
		index, ok := stepResult["index"].(float64)
		if !ok || index < 0 || index >= float64(len(steps)) || index != float64(int(index)) || seen[int(index)] {
			return false
		}
		seen[int(index)] = true
		step := runtimeMap(steps[int(index)])
		stepAction := runtimeString(step["action"])
		if stepAction == "" || stepAction == "debug.batch" || stepResult["action"] != stepAction || stepResult["ok"] != true || stepResult["error"] != nil {
			return false
		}
		if raw, present := step["payload"]; present && runtimeMap(raw) == nil || runtimeMap(stepResult["result"]) == nil {
			return false
		}
		stepContext := ctx
		if value, present := stepResult["context"]; present {
			raw := runtimeMap(value)
			if raw == nil {
				return false
			}
			stepContext = &protocol.Context{ProjectUUID: runtimeString(raw["projectUuid"]), DocumentUUID: runtimeString(raw["documentUuid"])}
			if ctx == nil || stepContext.ProjectUUID == "" || stepContext.DocumentUUID == "" || legacyContextConflict(*ctx, *stepContext) {
				return false
			}
		}
		stepPayload := runtimeMap(step["payload"])
		if stepContext == nil || legacyContextConflict(legacyPayloadTarget(stepPayload), *stepContext) || !legacyOutcomeSuccess(stepAction, stepPayload, runtimeMap(stepResult["result"]), stepContext, inheritedTarget) {
			return false
		}
		if runtimeProjectWide(protocol.Request{Action: stepAction, Payload: stepPayload}) || stepAction == "document.open" || stepAction == "document.close" || stepAction == "schematic.page.open" || stepAction == "system.page_reload" {
			return false // A final context cannot establish ownership of earlier cross-document steps.
		}
	}
	return true
}

// These are execution receipts, not new geometry, electrical, or delivery checks.
// Unrecognised write result contracts retain the historical unknown guard.
func legacyTypedOutcomeSuccess(action string, payload, result map[string]any) bool {
	if !runtimeDangerous(protocol.Request{Action: action}) {
		return true
	}
	switch action {
	case "schematic.component.place":
		id := runtimeString(result["primitiveId"])
		return id != "" && runtimeString(runtimeMap(result["component"])["primitiveId"]) == id
	case "schematic.component.modify":
		id := runtimeString(payload["primitiveId"])
		return id != "" && runtimeMap(payload["patch"]) != nil && runtimeString(runtimeMap(result["component"])["primitiveId"]) == id && legacyEmptyOptional(result, "notApplied")
	case "schematic.wire.create":
		return runtimeString(result["primitiveId"]) != "" && len(runtimeArray(result["line"])) > 0
	case "schematic.wire.labels.apply":
		ids, valid := legacyIDs(payload["wireIds"])
		remaining, hasRemaining := result["remaining"].([]any)
		_, hasLabels := payload["labels"].([]any)
		return valid && len(ids) > 0 && len(ids) == len(runtimeArray(payload["wireIds"])) && hasLabels && result["verified"] == true && result["partial"] == false && hasRemaining && len(remaining) == 0 && legacyJSONHash(result["wireIds"]) == legacyJSONHash(payload["wireIds"])
	case "schematic.primitives.delete":
		requested, valid := legacyIDs(payload["primitiveIds"])
		if !valid || !legacyExactCount(result["requested"], len(requested)) || !legacyExactCount(result["total"], len(requested)) || !legacyEmptyOptional(result, "survived", "unverified", "notFound") {
			return false
		}
		if count, present := result["survivedTotal"]; present && !legacyExactCount(count, 0) {
			return false
		}
		groups, counts := runtimeMap(result["deletedIds"]), runtimeMap(result["deleted"])
		if groups == nil || counts == nil {
			return false
		}
		deleted := map[string]bool{}
		for group, value := range groups {
			ids, valid := legacyIDs(value)
			if !valid || len(ids) != len(runtimeArray(value)) || !legacyExactCount(counts[group], len(ids)) {
				return false
			}
			for id := range ids {
				if !requested[id] || deleted[id] {
					return false
				}
				deleted[id] = true
			}
		}
		for group, count := range counts {
			if _, present := groups[group]; !present && !legacyExactCount(count, 0) {
				return false
			}
		}
		return len(deleted) == len(requested)
	}
	return false
}

func legacyIDs(value any) (map[string]bool, bool) {
	if id, ok := value.(string); ok {
		return map[string]bool{id: true}, id != ""
	}
	values, ok := value.([]any)
	if !ok {
		return nil, false
	}
	ids := map[string]bool{}
	for _, value := range values {
		id := runtimeString(value)
		if id == "" {
			return nil, false
		}
		ids[id] = true
	}
	return ids, true
}

func legacyEmptyOptional(result map[string]any, keys ...string) bool {
	for _, key := range keys {
		if value, present := result[key]; present {
			switch values := value.(type) {
			case []any:
				if len(values) != 0 {
					return false
				}
			case map[string]any:
				if len(values) != 0 {
					return false
				}
			default:
				return false
			}
		}
	}
	return true
}

func legacyPayloadTargetValid(payload map[string]any) bool {
	if value, present := payload["_target"]; present {
		target := runtimeMap(value)
		if target == nil {
			return false
		}
		for _, key := range []string{"projectUuid", "documentUuid"} {
			if value, present := target[key]; present && runtimeString(value) == "" {
				return false
			}
		}
	}
	return true
}

func legacyExactCount(value any, count int) bool {
	n, ok := value.(float64)
	return ok && n == float64(count)
}

func legacyReceiptSuccess(receipt *runtimeReceipt, entry auditEntry) bool {
	if receipt.Status != "action_finished" && receipt.Status != "needs_review" && receipt.Status != "saved" {
		return false
	}
	if entry.Action != "debug.batch" {
		return true
	}
	progress := runtimeMap(receipt.Progress)
	if !legacyExactCount(progress["completed"], len(runtimeArray(entry.Payload["steps"]))) || !legacyExactCount(progress["total"], len(runtimeArray(entry.Payload["steps"]))) {
		return false
	}
	results := runtimeArray(progress["results"])
	if len(results) != len(runtimeArray(entry.Payload["steps"])) {
		return false
	}
	seen := map[int]bool{}
	for _, value := range results {
		item := runtimeMap(value)
		index, ok := item["index"].(float64)
		if !ok || index < 0 || index >= float64(len(results)) || index != float64(int(index)) || seen[int(index)] || item["ok"] != true || item["error"] != nil || runtimeString(item["action"]) != runtimeString(runtimeMap(runtimeArray(entry.Payload["steps"])[int(index)])["action"]) {
			return false
		}
		seen[int(index)] = true
	}
	return true
}

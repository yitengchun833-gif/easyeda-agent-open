package daemon

// Reuse real results, never infer evidence from an action name or a plan.
func (r *runtimeReceipt) observeEvidence(entry auditEntry, current bool) {
	r.ImageAvailable, r.DRCPassed, r.ImageScope = false, false, nil
	if !current || !entry.OK || entry.Result["partial"] == true || entry.Result["ok"] == false || runtimeMap(entry.Result["_scene"])["changedDuringAction"] == true {
		return
	}
	accept := func(action string, payload, result map[string]any) {
		if result["partial"] == true || result["ok"] == false || runtimeMap(result["_scene"])["changedDuringAction"] == true {
			return
		}
		switch action {
		case "schematic.drc.check":
			summary := runtimeMap(result["summary"])
			r.DRCPassed = result["countsAvailable"] == true && runtimeZero(summary["error"]) && runtimeZero(summary["fatal"]) && runtimeZero(summary["unknown"])
		case "view.capture":
			r.ImageAvailable = runtimeString(runtimeMap(result["value"])["base64"]) != ""
			r.ImageScope = nil
		case "schematic.export.image":
			r.ImageAvailable = false
			r.ImageScope = nil
			if result["format"] != "png" && result["format"] != "svg" {
				return
			}
			scope := runtimeString(result["scope"])
			if scope == "selection" {
				if id := runtimeString(payload["primitiveIds"]); id != "" {
					r.ImageScope = []string{id}
				} else {
					for _, value := range runtimeArray(payload["primitiveIds"]) {
						if id := runtimeString(value); id != "" {
							r.ImageScope = append(r.ImageScope, id)
						}
					}
				}
				if len(r.ImageScope) == 0 || runtimeNumber(result["selectedCount"]) != int64(len(r.ImageScope)) {
					return
				}
			} else if scope != "page" {
				return
			} // Project exports cannot prove active-page image identity.
			for _, artifact := range entry.Artifacts {
				if artifact.ID == runtimeString(result["artifactId"]) && artifact.Kind == "schematic_export" && artifact.Path != "" && artifact.Size > 0 && artifact.SHA256 != "" && (artifact.MimeType == "image/png" || artifact.MimeType == "image/svg+xml") {
					r.ImageAvailable = true
				}
			}
		}
	}
	if entry.Action != "debug.batch" {
		accept(entry.Action, entry.Payload, entry.Result)
		return
	}
	// Mixed write/read batches need finer observation boundaries. Require a
	// completed read-only batch here, so an early check cannot certify later edits.
	if runtimeContentWrite(entry.Action, entry.Payload) {
		return
	}
	steps, results := runtimeArray(entry.Payload["steps"]), runtimeArray(entry.Result["results"])
	if len(steps) != len(results) {
		return
	}
	for i, value := range results {
		step, result := runtimeMap(steps[i]), runtimeMap(value)
		inner := runtimeMap(result["result"])
		if result["ok"] != true || result["action"] != step["action"] || runtimeNumber(result["index"]) != int64(i) || inner["partial"] == true || inner["ok"] == false || runtimeMap(inner["_scene"])["changedDuringAction"] == true {
			r.ImageAvailable, r.DRCPassed = false, false
			return
		}
		accept(runtimeString(result["action"]), runtimeMap(step["payload"]), inner)
	}
}

func (r *runtimeReceipt) coversImage(scope any) bool {
	if !r.ImageAvailable {
		return false
	}
	if len(r.ImageScope) == 0 {
		return true
	}
	required := runtimeArray(scope)
	if len(required) == 0 {
		return false
	}
	for _, value := range required {
		found := false
		for _, id := range r.ImageScope {
			if id == runtimeString(value) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

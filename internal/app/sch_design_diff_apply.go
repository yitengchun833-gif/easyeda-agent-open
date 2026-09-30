package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// Compile a deliberately bounded patch. Complete before/after plans describe
// intent; the fresh native snapshot protects electrical state, and queued frame
// checks prove ownership and the baseline appearance at execution time.
func schDesignDiffPlaybook(beforeRaw, afterRaw, snapshot []byte) (*playbook, error) {
	beforeInput, err := decodeSchDesignInput(beforeRaw)
	if err != nil {
		return nil, err
	}
	afterInput, err := decodeSchDesignInput(afterRaw)
	if err != nil {
		return nil, err
	}
	if beforeInput.kind != "compose-plan" || afterInput.kind != "compose-plan" || len(beforeInput.missingDrawing) != 0 || len(afterInput.missingDrawing) != 0 {
		return nil, fmt.Errorf("frame diff Apply requires two complete compose plans (first BEFORE baseline, second AFTER desired)")
	}
	diff, err := compareSchDesignInputs(beforeInput, afterInput)
	if err != nil {
		return nil, err
	}
	if !diff.Coverage.CanonicalComplete || diff.Status == "wrong-target" || diff.Status == "incomplete" {
		return nil, fmt.Errorf("frame diff Apply requires complete evidence for the same target")
	}
	for _, change := range diff.Changes {
		if !strings.HasPrefix(change.Path, "/drawing/layout/frames/") {
			return nil, fmt.Errorf("frame diff Apply supports only existing frame/title changes; unsupported change %s", change.Path)
		}
	}
	var before, after schCompositionPlan
	if err = json.Unmarshal(beforeRaw, &before); err != nil {
		return nil, err
	}
	if err = json.Unmarshal(afterRaw, &after); err != nil {
		return nil, err
	}
	beforeFrames := schFrameDocument{SchemaVersion: 1, DocumentID: before.Layout.DocumentID, Frames: before.Layout.Frames}
	afterFrames := schFrameDocument{SchemaVersion: 1, DocumentID: after.Layout.DocumentID, Frames: after.Layout.Frames}
	if err = beforeFrames.validate(); err != nil {
		return nil, fmt.Errorf("baseline frames: %w", err)
	}
	if err = afterFrames.validate(); err != nil {
		return nil, fmt.Errorf("desired frames: %w", err)
	}
	if len(beforeFrames.Frames) != len(afterFrames.Frames) {
		return nil, fmt.Errorf("frame diff Apply cannot add or delete frames")
	}
	changed := schFrameDocument{SchemaVersion: 1, DocumentID: after.Layout.DocumentID}
	usable, _, err := schCompositionUsableBounds(after.Sheet, after.SheetBorder)
	if err != nil {
		return nil, err
	}
	beforeState := beforeInput.drawing["layout"].(map[string]any)["frames"].(map[string]any)
	afterState := afterInput.drawing["layout"].(map[string]any)["frames"].(map[string]any)
	for i, f := range afterFrames.Frames {
		old := beforeFrames.Frames[i]
		if f.ID != old.ID {
			return nil, fmt.Errorf("frame diff Apply cannot rename or reorder frames")
		}
		if old.TitleLayout == nil || f.TitleLayout == nil {
			return nil, fmt.Errorf("frame %s requires modeled title/circuit occupancy in both plans", f.ID)
		}
		oldFrame := beforeState[f.ID].(map[string]any)
		newFrame := afterState[f.ID].(map[string]any)
		oldOccupancy := oldFrame["titleLayout"].(map[string]any)["obstacles"]
		newOccupancy := newFrame["titleLayout"].(map[string]any)["obstacles"]
		if !reflect.DeepEqual(oldOccupancy, newOccupancy) || f.TitleLayout.Clearance != old.TitleLayout.Clearance {
			return nil, fmt.Errorf("frame %s cannot change circuit occupancy or its clearance in a frame-only patch", f.ID)
		}
		if !boxInside(f.Rect, usable) {
			return nil, fmt.Errorf("frame %s exceeds the usable drawing boundary", f.ID)
		}
		for _, k := range after.Keepouts {
			if boxesGapOverlap(f.Rect, k, schModulePageMargin) {
				return nil, fmt.Errorf("frame %s overlaps a keepout", f.ID)
			}
		}
		for _, other := range afterFrames.Frames[:i] {
			if boxesGapOverlap(f.Rect, other.Rect, schModuleGap) {
				return nil, fmt.Errorf("frames %s/%s overlap their required gap", f.ID, other.ID)
			}
		}
		if !reflect.DeepEqual(oldFrame, newFrame) {
			changed.Frames = append(changed.Frames, f)
		}
	}
	// Keep malformed native inventory from reaching the existing composition
	// helper's native-record type assertions.
	var native struct {
		Result map[string]any `json:"result"`
	}
	if err = json.Unmarshal(snapshot, &native); err != nil {
		return nil, err
	}
	parts, ok := native.Result["components"].([]any)
	if !ok {
		return nil, fmt.Errorf("fresh --before snapshot requires an explicit components inventory")
	}
	for _, part := range parts {
		if _, ok := part.(map[string]any); !ok {
			return nil, fmt.Errorf("fresh --before snapshot contains malformed component records")
		}
	}
	// This is a pure compiler/validator call: false forbids the reset path and
	// requires the native electrical drawing to match the BEFORE baseline.
	baseline, err := schCompositionPlaybook(&before, snapshot, false)
	if err != nil {
		return nil, fmt.Errorf("fresh snapshot must match the BEFORE baseline: %w", err)
	}
	pb := &playbook{Version: 1, RequireFullExecution: true, Meta: baseline.Meta, Defaults: baseline.Defaults}
	pb.Meta.Name = "Owned frame/title design diff (BEFORE to AFTER)"
	for _, step := range baseline.Steps {
		if step.ID == "verify-project-unique-designators" || step.ID == "verify-existing-composition" {
			pb.Steps = append(pb.Steps, step)
		}
	}
	if len(pb.Steps) != 2 {
		return nil, fmt.Errorf("baseline compiler did not provide both electrical guards")
	}
	frameStep := func(id, run string, frames schFrameDocument) playbookStep {
		raw, _ := json.Marshal(frames)
		return playbookStep{ID: id, Run: run, Flags: map[string]any{"data": string(raw)}, Assert: map[string]string{"$.verified": "true"}}
	}
	pb.Steps = append(pb.Steps, frameStep("verify-baseline-owned-frames", "sch frame check", beforeFrames))
	if len(changed.Frames) > 0 {
		pb.Steps = append(pb.Steps, frameStep("apply-changed-owned-frames", "sch frame apply", changed), frameStep("verify-desired-owned-frames", "sch frame check", afterFrames))
	}
	read := map[string]any{"includePins": true, "includeBBox": true, "includeDeviceIdentity": true, "includeWires": true, "includeConnectivitySummary": true}
	pb.Steps = append(pb.Steps, playbookStep{ID: "verify-unchanged-circuit-after-frames", Action: "schematic.components.list", Payload: read, ExpectSchematic: schCompositionExpectation(&after, true)})
	if len(changed.Frames) > 0 {
		pb.Steps = append(pb.Steps, playbookStep{ID: "save-frame-diff", Action: "schematic.save", Assert: map[string]string{"$.saved": "true"}})
	}
	if errs := preflight(pb, nil); len(errs) > 0 {
		return nil, fmt.Errorf("frame diff Apply preflight: %v", errs)
	}
	return pb, nil
}

func writeSchDesignDiffPlaybook(raw [2][]byte, paths []string, snapshotPath, out string) error {
	if sameSchDiffPath(out, snapshotPath) || sameSchDiffPath(out, paths[0]) || sameSchDiffPath(out, paths[1]) {
		return fmt.Errorf("--playbook must not overwrite either plan or the fresh snapshot")
	}
	snapshot, err := os.ReadFile(snapshotPath)
	if err != nil {
		return err
	}
	pb, err := schDesignDiffPlaybook(raw[0], raw[1], snapshot)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(pb, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, append(data, '\n'), 0644)
}

func sameSchDiffPath(a, b string) bool {
	x, _ := filepath.Abs(a)
	y, _ := filepath.Abs(b)
	if x == y {
		return true
	}
	fi, e1 := os.Stat(a)
	fj, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(fi, fj)
}

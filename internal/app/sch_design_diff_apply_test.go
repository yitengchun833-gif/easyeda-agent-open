package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func frameDiffFixture(t *testing.T) (*schCompositionPlan, schCompositionPlan, map[string]any) {
	t.Helper()
	before, snapshot := composeApplyFixture(t, true)
	var after schCompositionPlan
	if err := json.Unmarshal(designDiffBytes(t, before), &after); err != nil {
		t.Fatal(err)
	}
	after.Layout.Frames[0].Title = "POWER"
	return before, after, snapshot
}

func TestFrameDiffApplyCompilesOnlyChangedOwnedFrame(t *testing.T) {
	before, after, snapshot := frameDiffFixture(t)
	pb, err := schDesignDiffPlaybook(designDiffBytes(t, before), designDiffBytes(t, after), composeApplyBytes(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if !pb.RequireFullExecution || len(pb.Steps) != 7 || pb.Steps[len(pb.Steps)-1].Action != "schematic.save" {
		t.Fatalf("expected a complete guarded patch with final save: %+v", pb)
	}
	guard, _ := composeStep(t, pb, "verify-existing-composition")
	frameGuard, oldStep := composeStep(t, pb, "verify-baseline-owned-frames")
	apply, changedStep := composeStep(t, pb, "apply-changed-owned-frames")
	post, _ := composeStep(t, pb, "verify-unchanged-circuit-after-frames")
	if guard >= frameGuard || frameGuard >= apply || apply >= post {
		t.Fatal("writes may only follow both baseline guards and precede final electrical checks")
	}
	var oldFrames, changed schFrameDocument
	_ = json.Unmarshal([]byte(oldStep.Flags["data"].(string)), &oldFrames)
	_ = json.Unmarshal([]byte(changedStep.Flags["data"].(string)), &changed)
	if oldFrames.Frames[0].Title != before.Layout.Frames[0].Title || len(changed.Frames) != 1 || changed.Frames[0].Title != "POWER" || changed.Frames[0].ID != before.Layout.Frames[0].ID {
		t.Fatal("patch direction or changed-frame selection is incorrect")
	}
	for _, step := range pb.Steps {
		if step.Action != "" && step.Action != "schematic.components.list" && step.Action != "schematic.save" {
			t.Fatalf("unexpected electrical action %s", step.Action)
		}
		if step.Run != "" && step.Run != "sch frame check" && step.Run != "sch frame apply" {
			t.Fatalf("unexpected mutation %s", step.Run)
		}
	}
}

func TestFrameDiffApplySamePlanIsReadOnly(t *testing.T) {
	before, _, snapshot := frameDiffFixture(t)
	pb, err := schDesignDiffPlaybook(designDiffBytes(t, before), designDiffBytes(t, before), composeApplyBytes(t, snapshot))
	if err != nil {
		t.Fatal(err)
	}
	if len(pb.Steps) != 4 {
		t.Fatalf("equal plans should only verify current circuit and owned frames: %+v", pb.Steps)
	}
	for _, step := range pb.Steps {
		if step.Run != "" && step.Run != "sch frame check" || step.Action != "" && step.Action != "schematic.components.list" {
			t.Fatalf("equal data must not write or save: %+v", step)
		}
	}
}

func TestFrameDiffApplyRejectsChangesOutsideOwnedFrames(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*schCompositionPlan)
	}{
		{"wire", func(p *schCompositionPlan) { p.Layout.Wires[0].Points[0][0] += 5 }},
		{"marker", func(p *schCompositionPlan) { p.Layout.Flags[0].Offset += 5 }},
		{"value", func(p *schCompositionPlan) { p.Layout.Placements[0].Value = "changed" }},
		{"diagnostic", func(p *schCompositionPlan) { p.RowHeight += 5 }},
		{"target", func(p *schCompositionPlan) { p.Connectivity.ProjectID = "other" }},
		{"frame addition", func(p *schCompositionPlan) {
			f := p.Layout.Frames[0]
			f.ID = "NEW"
			p.Layout.Frames = append(p.Layout.Frames, f)
		}},
		{"frame deletion", func(p *schCompositionPlan) { p.Layout.Frames = p.Layout.Frames[:1] }},
		{"frame reorder", func(p *schCompositionPlan) {
			p.Layout.Frames[0], p.Layout.Frames[1] = p.Layout.Frames[1], p.Layout.Frames[0]
		}},
		{"occupancy deletion", func(p *schCompositionPlan) {
			p.Layout.Frames[0].TitleLayout.Obstacles = p.Layout.Frames[0].TitleLayout.Obstacles[:1]
		}},
		{"clearance reduction", func(p *schCompositionPlan) { p.Layout.Frames[0].TitleLayout.Clearance = 0 }},
		{"missing occupancy", func(p *schCompositionPlan) { p.Layout.Frames[0].TitleLayout = nil }},
		{"outside frame", func(p *schCompositionPlan) { p.Layout.Frames[0].TitleX = -1000 }},
		{"unsupported solid style", func(p *schCompositionPlan) { p.Layout.Frames[0].LineType = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after, snapshot := frameDiffFixture(t)
			tc.edit(&after)
			if _, err := schDesignDiffPlaybook(designDiffBytes(t, before), designDiffBytes(t, after), composeApplyBytes(t, snapshot)); err == nil {
				t.Fatal("unsupported/unsafe change compiled into a patch")
			}
		})
	}
}

func TestFrameDiffApplyRequiresMatchingFreshNativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wrong target", func(s map[string]any) { s["context"].(map[string]any)["projectUuid"] = "other" }},
		{"missing wires", func(s map[string]any) { delete(s["result"].(map[string]any), "wires") }},
		{"missing summary", func(s map[string]any) { delete(s["result"].(map[string]any), "connectivitySummary") }},
		{"missing identity", func(s map[string]any) {
			delete(s["result"].(map[string]any)["components"].([]any)[1].(map[string]any), "device")
		}},
		{"moved part", func(s map[string]any) {
			p := s["result"].(map[string]any)["components"].([]any)[1].(map[string]any)
			p["x"] = p["x"].(float64) + 5
		}},
		{"malformed inventory", func(s map[string]any) { s["result"].(map[string]any)["components"] = []any{nil} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after, snapshot := frameDiffFixture(t)
			tc.edit(snapshot)
			if _, err := schDesignDiffPlaybook(designDiffBytes(t, before), designDiffBytes(t, after), composeApplyBytes(t, snapshot)); err == nil {
				t.Fatal("unverified native evidence compiled into a patch")
			}
		})
	}
}

func TestFrameDiffApplyCLIAndInputPreservation(t *testing.T) {
	before, after, snapshot := frameDiffFixture(t)
	dir := t.TempDir()
	a, b, live, out := filepath.Join(dir, "before.json"), filepath.Join(dir, "after.json"), filepath.Join(dir, "live.json"), filepath.Join(dir, "apply.json")
	for path, data := range map[string][]byte{a: designDiffBytes(t, before), b: designDiffBytes(t, after), live: composeApplyBytes(t, snapshot)} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var stdout, stderr bytes.Buffer
	cmd := newSchDesignDiffCmd(&stdout, &stderr)
	cmd.SetArgs([]string{a, b, "--before", live, "--playbook", out})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(out)
	if err != nil || !bytes.Contains(raw, []byte("apply-changed-owned-frames")) {
		t.Fatalf("missing compiled queue: %v", err)
	}
	cmd = newSchDesignDiffCmd(&stdout, &stderr)
	cmd.SetArgs([]string{a, b, "--before", live, "--playbook", a})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "must not overwrite") {
		t.Fatalf("source overwrite accepted: %v", err)
	}
	remaining, _ := os.ReadFile(a)
	if !bytes.Equal(remaining, designDiffBytes(t, before)) {
		t.Fatal("baseline source changed")
	}
	// A rejected non-frame diff must preserve a previously good queue.
	after.RowHeight += 5
	_ = os.WriteFile(b, designDiffBytes(t, after), 0600)
	cmd = newSchDesignDiffCmd(&stdout, &stderr)
	cmd.SetArgs([]string{a, b, "--before", live, "--playbook", out})
	if err := cmd.Execute(); err == nil {
		t.Fatal("diagnostic change accepted")
	}
	remaining, _ = os.ReadFile(out)
	if !bytes.Equal(remaining, raw) {
		t.Fatal("failed compile overwrote previous queue")
	}
}

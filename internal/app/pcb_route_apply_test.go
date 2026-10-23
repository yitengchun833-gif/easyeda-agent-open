package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func routeApplyTestReceipt(in pcbRouteRequest) *pcbRouteApplyReceipt {
	return &pcbRouteApplyReceipt{SchemaVersion: 1, Status: "not_executed", Candidate: pcbRouteCandidate{Net: in.Net, Layer: in.Layer, WidthMil: in.WidthMil, Points: [][2]float64{{50, 80}, {150, 80}, {250, 80}}}, Remaining: []int{0, 1}, Persistence: "unverified", DRC: map[string]any{"status": "not_checked"}}
}

func routeApplyTestLine(id, net string, x1, y1, x2, y2, width float64) map[string]any {
	return map[string]any{"primitiveId": id, "net": net, "layer": 1.0, "startX": x1, "startY": y1, "endX": x2, "endY": y2, "lineWidth": width, "locked": false}
}

func routeApplyNativeTestComponents(t *testing.T, components []boardComp) []any {
	var native []any
	if err := json.Unmarshal(routeJSON(t, components), &native); err != nil {
		t.Fatal(err)
	}
	// Native pad reads always include rotation; the offline schema omits zero.
	for _, c := range native {
		for _, raw := range c.(map[string]any)["pads"].([]any) {
			p := raw.(map[string]any)
			if _, ok := p["rotation"]; !ok {
				p["rotation"] = 0.0
			}
		}
	}
	return native
}

func TestPCBRouteApplyExecutionBoundaries(t *testing.T) {
	for _, mode := range []string{"success", "split-new-line", "already-present", "silent-noop", "second-write-error", "readback-error", "malformed-readback", "scope-changed", "drc-fail", "drc-error", "dry-run", "receipt-error", "post-receipt-error"} {
		t.Run(mode, func(t *testing.T) {
			in, before := routeSolverFixture()
			if mode == "already-present" {
				before.Copper.Lines = []any{routeApplyTestLine("old-a", in.Net, 50, 80, 150, 80, in.WidthMil), routeApplyTestLine("old-b", in.Net, 150, 80, 250, 80, in.WidthMil)}
			}
			state := cloneEscapeSnapshot(before)
			r := routeApplyTestReceipt(in)
			r.DryRun = mode == "dry-run"
			writes, reads, saves := 0, 0, 0
			call := func(action string, p map[string]any) (*actionResult, error) {
				switch action {
				case "pcb.line.create":
					writes++
					if mode == "second-write-error" && writes == 2 {
						return &actionResult{ID: "failed-request", Result: map[string]any{}}, fmt.Errorf("write outcome unavailable")
					}
					id := fmt.Sprintf("new-%d", writes)
					if mode != "silent-noop" {
						a, b := asFloat(p["startX"]), asFloat(p["endX"])
						if mode == "split-new-line" {
							mid := (a + b) / 2
							state.Copper.Lines = append(state.Copper.Lines, routeApplyTestLine(id+"a", in.Net, a, 80, mid, 80, in.WidthMil), routeApplyTestLine(id+"b", in.Net, mid, 80, b, 80, in.WidthMil))
						} else {
							state.Copper.Lines = append(state.Copper.Lines, routeApplyTestLine(id, in.Net, a, 80, b, 80, in.WidthMil))
						}
					}
					return &actionResult{ID: fmt.Sprintf("request-%d", writes), Result: map[string]any{"primitiveId": id}}, nil
				case "pcb.line.list":
					if mode == "readback-error" && writes > 0 {
						return nil, fmt.Errorf("cannot read copper")
					}
					if mode == "malformed-readback" && writes > 0 {
						return &actionResult{Result: map[string]any{"lines": state.Copper.Lines, "arcsAvailable": false}}, nil
					}
					return &actionResult{Result: map[string]any{"lines": state.Copper.Lines, "arcs": state.Copper.Arcs, "arcsAvailable": true}}, nil
				case "pcb.drc.check":
					if mode == "drc-error" {
						return nil, fmt.Errorf("native DRC unavailable")
					}
					return &actionResult{ID: "drc", Result: map[string]any{"passed": mode != "drc-fail", "violations": []any{}}}, nil
				default:
					t.Fatalf("unexpected action %s", action)
					return nil, nil
				}
			}
			snapshot := func() (*boardSnapshot, error) {
				reads++
				s := cloneEscapeSnapshot(state)
				if mode == "scope-changed" && reads == 2 {
					s.Components[0].Pads[1].Net = "CHANGED"
				}
				return s, nil
			}
			persist := func() error {
				saves++
				if mode == "receipt-error" || mode == "post-receipt-error" && saves == 2 {
					return fmt.Errorf("cannot persist receipt")
				}
				return nil
			}
			err := executePCBRouteApply(context.Background(), in, before, r, call, snapshot, persist)
			switch mode {
			case "success", "split-new-line", "drc-fail", "drc-error":
				if err != nil || writes != 2 || !r.Applied || !r.GeometryVerified || r.Partial || r.Status != "applied" || len(r.Remaining) != 0 {
					t.Fatalf("receipt=%+v err=%v writes=%d", r, err, writes)
				}
				wantDRC := "pass"
				if mode == "drc-fail" {
					wantDRC = "fail"
				}
				if mode == "drc-error" {
					wantDRC = "unknown"
				}
				if r.DRC["status"] != wantDRC || r.Verified != (wantDRC == "pass") || r.Persistence != "unverified" || r.SaveRequested {
					t.Fatalf("conflated write/DRC/persistence: %+v", r)
				}
			case "dry-run":
				if err != nil || writes != 0 || r.Status != "ready" || r.Applied {
					t.Fatalf("dry run wrote: %+v %v", r, err)
				}
			case "receipt-error":
				if err == nil || writes != 0 {
					t.Fatalf("write after receipt failure: %+v %v", r, err)
				}
			case "post-receipt-error":
				if err == nil || writes != 1 || !r.Applied || r.Status != "unknown" || len(r.Remaining) != 1 || r.Steps[0].PrimitiveID == "" {
					t.Fatalf("lost copper after receipt failure: %+v %v", r, err)
				}
			case "already-present":
				if err != nil || writes != 0 || r.Applied || r.Status != "already_present" || !r.GeometryVerified || !r.Verified {
					t.Fatalf("known route was rewritten: %+v %v", r, err)
				}
			default:
				if err == nil || !r.Partial || r.Verified {
					t.Fatalf("false success: %+v %v", r, err)
				}
				if mode == "second-write-error" {
					if writes != 2 || r.Status != "unknown" || r.Steps[0].Status != "applied" || r.Steps[1].RequestID != "failed-request" {
						t.Fatalf("lost partial write: %+v", r)
					}
				} else if mode != "scope-changed" && writes != 1 {
					t.Fatalf("replayed or continued failed write: %d", writes)
				}
				if (mode == "silent-noop" || mode == "readback-error") && len(r.Remaining) != 1 {
					t.Fatalf("lost unexecuted suffix: %+v", r)
				}
			}
			if saves == 0 {
				t.Fatal("no receipt update")
			}
		})
	}
}

func TestPCBRouteApplyRejectsStaleIdentityAndExistingSplitsBeforeWriting(t *testing.T) {
	for _, mode := range []string{"pad-id", "pad-net", "component-id", "existing-crossing", "existing-overlap", "duplicate-pad-id"} {
		t.Run(mode, func(t *testing.T) {
			in, before := routeSolverFixture()
			fresh := cloneEscapeSnapshot(before)
			switch mode {
			case "pad-id":
				fresh.Components[0].Pads[0].ID = "other"
			case "pad-net":
				fresh.Components[0].Pads[0].Net = "other"
			case "component-id":
				fresh.Components[0].ID = "other"
			case "duplicate-pad-id":
				fresh.Components[0].Pads[0].ID = fresh.Components[1].Pads[0].ID
			case "existing-crossing":
				before.Copper.Lines = append(before.Copper.Lines, routeApplyTestLine("old", in.Net, 150, 60, 150, 100, in.WidthMil))
				fresh = cloneEscapeSnapshot(before)
			case "existing-overlap":
				before.Copper.Lines = append(before.Copper.Lines, routeApplyTestLine("old", in.Net, 100, 80, 180, 80, in.WidthMil))
				fresh = cloneEscapeSnapshot(before)
			}
			r := routeApplyTestReceipt(in)
			calls := 0
			err := executePCBRouteApply(context.Background(), in, before, r, func(string, map[string]any) (*actionResult, error) { calls++; return nil, nil }, func() (*boardSnapshot, error) { return fresh, nil }, func() error { return nil })
			if err == nil || calls != 0 || r.Status != "not_executed" || r.Applied {
				t.Fatalf("stale/split candidate dispatched: %+v %v calls=%d", r, err, calls)
			}
		})
	}
}

func TestPCBRouteApplyPreservesLockedCopperAndAcceptsOnlyExactCoverage(t *testing.T) {
	in, before := routeSolverFixture()
	old := routeApplyTestLine("locked", in.Net, 50, 20, 250, 20, in.WidthMil)
	old["locked"] = true
	before.Copper.Lines = append(before.Copper.Lines, old)
	r := routeApplyTestReceipt(in)
	c := r.Candidate
	for _, mode := range []string{"complete", "gap", "wrong-net", "old-lock-changed", "extra-track", "duplicate-new-coverage", "legal-split"} {
		t.Run(mode, func(t *testing.T) {
			after := cloneEscapeSnapshot(before)
			q := 150.0
			if mode == "gap" {
				q = 149
			}
			line := routeApplyTestLine("new", in.Net, 50, 80, q, 80, in.WidthMil)
			if mode == "wrong-net" {
				line["net"] = "OTHER"
			}
			after.Copper.Lines = append(after.Copper.Lines, line)
			if mode == "duplicate-new-coverage" {
				after.Copper.Lines = append(after.Copper.Lines, routeApplyTestLine("duplicate", in.Net, 50, 80, 150, 80, in.WidthMil))
			}
			if mode == "legal-split" {
				after.Copper.Lines = after.Copper.Lines[:1]
				after.Copper.Lines = append(after.Copper.Lines, routeApplyTestLine("split-a", in.Net, 50, 80, 100, 80, in.WidthMil), routeApplyTestLine("split-b", in.Net, 100, 80, 150, 80, in.WidthMil))
			}
			if mode == "old-lock-changed" {
				after.Copper.Lines[0].(map[string]any)["locked"] = false
			}
			if mode == "extra-track" {
				after.Copper.Lines = append(after.Copper.Lines, routeApplyTestLine("extra", in.Net, 10, 10, 20, 10, in.WidthMil))
			}
			_, err := verifyRouteApplyLines(before, map[string]any{"lines": after.Copper.Lines, "arcs": []any{}, "arcsAvailable": true}, c, 1)
			if (err == nil) != (mode == "complete" || mode == "legal-split") {
				t.Fatalf("coverage %s: %v", mode, err)
			}
		})
	}
}

func TestPCBRouteApplyCommandRefusesWrongTargetWithoutNavigation(t *testing.T) {
	in, before := routeSolverFixture()
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	plan := filepath.Join(dir, "plan.json")
	out := filepath.Join(dir, "receipt.json")
	braw := writeRouteJSON(t, board, before)
	rraw := writeRouteJSON(t, request, in)
	report := newPCBRouteReport(braw, rraw)
	if err := solvePCBRoute(context.Background(), in, before, &report); err != nil {
		t.Fatal(err)
	}
	writeRouteJSON(t, plan, report)
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if call.Action != "pcb.components.list" {
			t.Errorf("unexpected action %s", call.Action)
		}
		if target, ok := call.Payload["_target"].(map[string]any); !ok || target["projectUuid"] != "project" || target["documentUuid"] != "pcb" {
			t.Error("missing explicit native target")
		}
		return `{"ok":true,"result":{},"context":{"projectUuid":"other-project","documentUuid":"pcb","documentType":"pcb"}}`
	})
	defer cleanup()
	cfg.project = "project"
	cfg.doc = "pcb"
	window := "w1"
	var stdout, stderr bytes.Buffer
	cmd := newPcbRouteApplyCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--from", request, "--plan", plan, "--out", out})
	if err := cmd.Execute(); err == nil || len(calls.snapshot()) != 1 {
		t.Fatalf("wrong target accepted: %v %+v", err, calls.snapshot())
	}
	if raw, err := os.ReadFile(out); err != nil || !bytes.Contains(raw, []byte(`"status": "not_executed"`)) {
		t.Fatalf("lost refusal receipt: %s %v", raw, err)
	}
	if !strings.Contains(stdout.String(), "not_executed") {
		t.Fatal("missing caller-visible receipt")
	}
}

func TestPCBRouteApplyCommandNativeReadWriteRoundTrip(t *testing.T) {
	in, source := routeSolverFixture()
	in.WidthMil = 6
	for i := range source.Components {
		c := &source.Components[i]
		c.BBox = &layoutBBox{MinX: c.X - 15, MinY: c.Y - 15, MaxX: c.X + 15, MaxY: c.Y + 15}
	}
	lines := []any{}
	writes := 0
	boundCalls, foreignContext, changingSnapshot := false, false, false
	componentReads := 0
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		if boundCalls {
			if target, ok := call.Payload["_target"].(map[string]any); !ok || target["projectUuid"] != "project" || target["documentUuid"] != "pcb" {
				t.Errorf("snapshot/write lost target on %s", call.Action)
			}
		}
		var result any
		switch call.Action {
		case "document.current":
			result = map[string]any{"uuid": "pcb"}
		case "pcb.components.list":
			if boundCalls {
				release, err := acquirePCBRouteApplyLock("project", "pcb")
				if err == nil {
					release()
					t.Error("apply lease did not cover snapshot reads")
				}
			}
			result = map[string]any{"components": routeApplyNativeTestComponents(t, source.Components)}
			if boundCalls && changingSnapshot {
				componentReads++
				if componentReads >= 2 {
					changed := cloneEscapeSnapshot(source)
					changed.Components[0].Pads[0].X++
					result = map[string]any{"components": routeApplyNativeTestComponents(t, changed.Components)}
				}
			}
		case "pcb.outline.get":
			result = map[string]any{"points": source.Outline.Points, "bbox": source.Outline.BBox, "outlineFormat": "polyline"}
		case "pcb.layers.list":
			result = map[string]any{"copperLayerCount": 2}
		case "pcb.drc.rules":
			result = ceshiRuleResult()
		case "pcb.line.list":
			result = map[string]any{"lines": lines, "arcs": []any{}, "arcsAvailable": true}
		case "pcb.via.list":
			result = map[string]any{"vias": []any{}}
		case "pcb.pour.list":
			result = map[string]any{"pours": []any{}}
		case "pcb.poured.list":
			result = map[string]any{"poured": []any{}}
		case "pcb.region.list":
			result = map[string]any{"regions": []any{}}
		case "pcb.fill.list":
			result = map[string]any{"fills": []any{}}
		case "pcb.silk.list":
			result = map[string]any{"texts": []any{map[string]any{"primitiveId": "silk-1", "kind": "string", "text": "U1", "layer": 3, "x": 40, "y": 45, "rotation": 0, "fontSize": 10}}}
		case "pcb.line.create":
			if target, ok := call.Payload["_target"].(map[string]any); !ok || target["documentUuid"] != "pcb" {
				t.Fatal("write lost native target")
			}
			writes++
			id := fmt.Sprintf("native-%d", writes)
			p := call.Payload
			lines = append(lines, routeApplyTestLine(id, in.Net, asFloat(p["startX"]), asFloat(p["startY"]), asFloat(p["endX"]), asFloat(p["endY"]), asFloat(p["lineWidth"])))
			result = map[string]any{"primitiveId": id}
		case "pcb.drc.check":
			if boundCalls {
				release, err := acquirePCBRouteApplyLock("project", "pcb")
				if err == nil {
					release()
					t.Error("apply lease ended before the final native read")
				}
			}
			result = map[string]any{"passed": false, "violations": []any{map[string]any{"net": "UNRELATED"}}}
		default:
			t.Fatalf("unexpected native action %s", call.Action)
		}
		project := "project"
		if boundCalls && foreignContext && call.Action == "pcb.layers.list" {
			project = "foreign-project"
			foreignContext = false
		}
		return `{"ok":true,"result":` + string(routeJSON(t, result)) + `,"context":{"projectUuid":"` + project + `","documentUuid":"pcb","documentType":"pcb"}}`
	})
	defer cleanup()
	before, err := fetchBoardSnapshot(cfg, "w1", boardSnapshotOpts{withSilk: true, withRules: true, withLayers: true, withCopper: true})
	if err != nil || len(before.Partial) > 0 {
		t.Fatalf("fixture snapshot: %+v %v", before, err)
	}
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	plan := filepath.Join(dir, "plan.json")
	out := filepath.Join(dir, "receipt.json")
	braw := writeRouteJSON(t, board, before)
	rraw := writeRouteJSON(t, request, in)
	report := newPCBRouteReport(braw, rraw)
	if err := solvePCBRoute(context.Background(), in, before, &report); err != nil {
		t.Fatal(err)
	}
	writeRouteJSON(t, plan, report)
	cfg.project, cfg.doc = "project", "pcb"
	window := "w1"
	var stdout, stderr bytes.Buffer
	boundCalls = true
	for _, mode := range []string{"A-B-A", "changed-during-read"} {
		foreignContext = mode == "A-B-A"
		changingSnapshot = mode == "changed-during-read"
		componentReads = 0
		refused := newPcbRouteApplyCmd(cfg, &window, &stdout, &stderr)
		refused.SetArgs([]string{"--board", board, "--from", request, "--plan", plan, "--out", filepath.Join(dir, mode+".json")})
		if err := refused.Execute(); err == nil || writes != 0 {
			t.Fatalf("inconsistent bound snapshot accepted (%s): %v writes=%d", mode, err, writes)
		}
		if mode == "A-B-A" && calls.snapshot()[len(calls.snapshot())-1].Action != "pcb.layers.list" {
			t.Fatal("continued reading after foreign target response")
		}
	}
	foreignContext, changingSnapshot = false, false
	if err := os.WriteFile(out+".tmp", []byte("unrelated existing file"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newPcbRouteApplyCmd(cfg, &window, &stdout, &stderr)
	cmd.SetArgs([]string{"--board", board, "--from", request, "--plan", plan, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("normal native application failed: %v %s", err, stderr.String())
	}
	var receipt pcbRouteApplyReceipt
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &receipt); err != nil {
		t.Fatal(err)
	}
	if writes != len(report.Candidate.Points)-1 || !receipt.Applied || !receipt.GeometryVerified || receipt.Status != "applied" || receipt.Verified || receipt.DRC["status"] != "fail" {
		t.Fatalf("conflated unrelated DRC with write failure: %+v writes=%d", receipt, writes)
	}
	if kept, err := os.ReadFile(out + ".tmp"); err != nil || string(kept) != "unrelated existing file" {
		t.Fatalf("receipt overwrote an unrelated temporary path: %q %v", kept, err)
	}
	for _, call := range calls.snapshot() {
		if call.Action == "document.open" || call.Action == "pcb.save" || call.Action == "pcb.via.create" || call.Action == "pcb.route.delete" {
			t.Fatalf("out-of-scope action %s", call.Action)
		}
	}
	var help, helpErr bytes.Buffer
	if code := Run([]string{"pcb", "route", "apply", "--help"}, &help, &helpErr); code != 0 || !strings.Contains(help.String(), "--plan") {
		t.Fatalf("route apply not registered: %s %s", help.String(), helpErr.String())
	}
}

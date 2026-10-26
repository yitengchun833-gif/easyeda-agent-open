package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func replacementFixture() (pcbRouteRequest, *boardSnapshot, []string) {
	in, s := routeSolverFixture()
	s.Copper.Lines = []any{routeApplyTestLine("old-a", in.Net, 50, 80, 150, 50, 4), routeApplyTestLine("old-b", in.Net, 150, 50, 250, 80, 4)}
	return in, s, []string{"old-a", "old-b"}
}

func TestPCBRouteReplaceBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "duplicate", "wrong-net", "layer", "locked", "unknown-lock", "embedded", "unselected-branch", "selected-branch", "via", "third-pad", "not-pad-center", "same-net-area"} {
		t.Run(mode, func(t *testing.T) {
			in, s, ids := replacementFixture()
			line := s.Copper.Lines[0].(map[string]any)
			switch mode {
			case "duplicate":
				ids = append(ids, ids[0])
			case "wrong-net":
				line["net"] = "WRONG"
			case "layer":
				line["layer"] = 2.0
			case "locked":
				line["locked"] = true
			case "unknown-lock":
				delete(line, "locked")
			case "embedded":
				line["primitiveId"] = "u-embedded"
				ids[0] = "u-embedded"
			case "unselected-branch", "selected-branch":
				s.Copper.Lines = append(s.Copper.Lines, routeApplyTestLine("branch", in.Net, 150, 50, 150, 120, 4))
				if mode == "selected-branch" {
					ids = append(ids, "branch")
				}
			case "via":
				s.Copper.Vias = []any{map[string]any{"primitiveId": "via", "net": in.Net, "x": 150.0, "y": 50.0, "holeDiameter": 8.0, "diameter": 16.0}}
			case "third-pad":
				s.Components[0].Pads[1].X, s.Components[0].Pads[1].Y = 150, 50
			case "not-pad-center":
				line["startX"] = 49.0
			case "same-net-area":
				s.Copper.Pours = []any{map[string]any{"primitiveId": "pour", "net": in.Net, "layer": 1.0}}
			}
			projected, removed, err := preparePCBRouteReplacement(s, in, ids)
			if mode != "valid" {
				if err == nil {
					t.Fatal("unsafe boundary accepted")
				}
				return
			}
			if err != nil || len(removed) != 2 || len(projected.Copper.Lines) != 0 || len(s.Copper.Lines) != 2 {
				t.Fatalf("bad projection: %v", err)
			}
		})
	}
}

func TestPCBRouteReplaceExecution(t *testing.T) {
	for _, mode := range []string{"success", "stale", "partial-delete", "delete-false", "native-scope-incomplete", "native-geometry-changed", "delete-error", "post-delete-read-error", "collateral-delete", "add-error", "drc-fail", "cancel-after-delete", "cancel-after-line-read", "cancel-before-delete", "receipt-before-delete"} {
		t.Run(mode, func(t *testing.T) {
			in, before, ids := replacementFixture()
			if mode == "collateral-delete" {
				before.Copper.Lines = append(before.Copper.Lines, routeApplyTestLine("neighbor", "B", 100, 150, 200, 150, 4))
			}
			projected, removed, err := preparePCBRouteReplacement(before, in, ids)
			if err != nil {
				t.Fatal(err)
			}
			raw := routeJSON(t, projected)
			route := newPCBRouteReport(raw, routeJSON(t, in))
			if err := solvePCBRoute(context.Background(), in, projected, &route); err != nil {
				t.Fatal(err)
			}
			checked := newPCBRouteReport(raw, routeJSON(t, in))
			if err := checkPCBRoute(context.Background(), in, projected, route, &checked); err != nil {
				t.Fatal(err)
			}
			r := &pcbRouteReplaceReceipt{SchemaVersion: 1, Status: "not_executed", Remove: removed, Route: checked, DeletedIDs: []string{}, RemainingIDs: append([]string(nil), ids...), Persistence: "unverified"}
			state := cloneEscapeSnapshot(before)
			deletes, creates, reads, persists := 0, 0, 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			call := func(action string, p map[string]any) (*actionResult, error) {
				switch action {
				case "pcb.route.delete":
					deletes++
					if p["kind"] != "track" || !reflect.DeepEqual(p["primitiveIds"], ids) || !reflect.DeepEqual(p["expectedTracks"], removed) {
						t.Fatalf("wrong deletion scope: %+v", p)
					}
					var keep []any
					for _, raw := range state.Copper.Lines {
						id := asString(raw.(map[string]any)["primitiveId"])
						if id == "old-a" || id == "old-b" && mode != "partial-delete" || id == "neighbor" && mode == "collateral-delete" {
							continue
						}
						keep = append(keep, raw)
					}
					if keep == nil {
						keep = []any{}
					}
					state.Copper.Lines = keep
					if mode == "cancel-after-delete" {
						cancel()
					}
					count := 2
					if mode == "native-scope-incomplete" {
						count = 1
					}
					nativeRemoved := []any{}
					for _, line := range removed {
						copy := map[string]any{"kind": "track"}
						for key, value := range line {
							copy[key] = value
						}
						nativeRemoved = append(nativeRemoved, copy)
					}
					if mode == "native-geometry-changed" {
						nativeRemoved[0].(map[string]any)["startX"] = 51.0
					}
					res := &actionResult{ID: "delete-1", Result: map[string]any{"deleted": map[string]any{"tracks": map[string]any{"requested": count, "ok": mode != "delete-false"}}, "count": count, "removed": nativeRemoved, "notFound": []any{}, "skippedLocked": []any{}}}
					if mode == "delete-error" {
						return res, fmt.Errorf("native delete response lost")
					}
					return res, nil
				case "pcb.line.list":
					if mode == "cancel-after-line-read" {
						cancel()
					}
					return &actionResult{Result: map[string]any{"lines": state.Copper.Lines, "arcs": state.Copper.Arcs, "arcsAvailable": true}}, nil
				case "pcb.line.create":
					creates++
					if mode == "add-error" {
						return &actionResult{ID: "add-unknown"}, fmt.Errorf("line write unknown")
					}
					id := fmt.Sprintf("replacement-%d", creates)
					state.Copper.Lines = append(state.Copper.Lines, routeApplyTestLine(id, in.Net, asFloat(p["startX"]), asFloat(p["startY"]), asFloat(p["endX"]), asFloat(p["endY"]), in.WidthMil))
					return &actionResult{ID: "add", Result: map[string]any{"primitiveId": id}}, nil
				case "pcb.drc.check":
					return &actionResult{ID: "drc", Result: map[string]any{"passed": mode != "drc-fail"}}, nil
				default:
					t.Fatalf("unexpected action %s", action)
					return nil, nil
				}
			}
			snapshot := func() (*boardSnapshot, error) {
				reads++
				if mode == "post-delete-read-error" && deletes > 0 {
					return nil, fmt.Errorf("post-delete read unavailable")
				}
				s := cloneEscapeSnapshot(state)
				if mode == "stale" {
					s.Copper.Lines[0].(map[string]any)["lineWidth"] = 7.0
				}
				return s, nil
			}
			persist := func() error {
				persists++
				if mode == "cancel-before-delete" && persists == 1 {
					cancel()
				}
				if mode == "receipt-before-delete" && persists == 1 {
					return fmt.Errorf("cannot save receipt")
				}
				return nil
			}
			err = executePCBRouteReplacement(ctx, in, before, projected, r, call, snapshot, persist)
			if mode == "success" || mode == "drc-fail" {
				if err != nil || r.Status != "replaced" || r.Partial || len(r.DeletedIDs) != 2 || len(r.RemainingIDs) != 0 || creates != 1 || deletes != 1 || !r.Addition.GeometryVerified {
					t.Fatalf("r=%+v err=%v", r, err)
				}
				if reads != 3 {
					t.Fatalf("redundant full snapshot: %d", reads)
				}
				if mode == "drc-fail" && r.Addition.DRC["status"] != "fail" {
					t.Fatal("lost independent DRC failure")
				}
				return
			}
			if err == nil {
				t.Fatal("failure accepted")
			}
			if mode == "stale" || mode == "receipt-before-delete" || mode == "cancel-before-delete" {
				if deletes != 0 || creates != 0 || r.DeleteAttempted {
					t.Fatal("wrote before verified boundary")
				}
				return
			}
			if deletes != 1 || !r.Partial {
				t.Fatalf("lost partial delete: %+v", r)
			}
			if mode != "add-error" && creates != 0 {
				t.Fatalf("continued after failed deletion boundary: %d", creates)
			}
			if mode == "delete-error" || mode == "post-delete-read-error" || mode == "add-error" {
				if r.Status != "unknown" {
					t.Fatalf("lost unknown: %+v", r)
				}
			}
			if mode == "cancel-after-line-read" && (r.Status != "partial" || r.Addition == nil || r.Addition.Status != "not_executed" || len(r.Addition.Steps) != 1 || r.Addition.Steps[0].Status != "not_executed" || !reflect.DeepEqual(r.Addition.Remaining, []int{0})) {
				t.Fatalf("canceled before new copper was dispatched: %+v", r)
			}
			if mode == "partial-delete" && (!reflect.DeepEqual(r.DeletedIDs, []string{"old-a"}) || !reflect.DeepEqual(r.RemainingIDs, []string{"old-b"})) {
				t.Fatalf("lost exact removal progress: %+v", r)
			}
		})
	}
}

func TestPCBRouteReplaceOfflinePreview(t *testing.T) {
	in, before, _ := replacementFixture()
	dir := t.TempDir()
	board, request, out := filepath.Join(dir, "board.json"), filepath.Join(dir, "request.json"), filepath.Join(dir, "preview.json")
	writeRouteJSON(t, board, before)
	writeRouteJSON(t, request, in)
	var stdout, stderr bytes.Buffer
	args := []string{"--host", "127.0.0.1", "--ports", "1", "pcb", "route", "replace", "--board", board, "--from", request, "--project", "project-uuid", "--doc", "pcb-uuid", "--ids", "old-a,old-b", "--out", out, "--dry-run"}
	if code := Run(args, &stdout, &stderr); code != 0 {
		t.Fatalf("preview contacted unavailable daemon or failed: %s", stderr.String())
	}
	var preview pcbRouteReplaceReceipt
	if err := decodePCBRouteJSON(stdout.Bytes(), &preview, true); err != nil {
		t.Fatal(err)
	}
	if preview.Status != "preview" || !preview.DryRun || preview.DeleteAttempted || len(preview.Remove) != 2 || preview.Route.Candidate == nil {
		t.Fatalf("bad diff: %+v", preview)
	}
	if _, err := os.Stat(preview.Route.Preview); err != nil {
		t.Fatal(err)
	}
	saved, _ := os.ReadFile(out)
	stdout.Reset()
	stderr.Reset()
	if Run(args, &stdout, &stderr) == 0 {
		t.Fatal("overwrote a prior preview")
	}
	after, _ := os.ReadFile(out)
	if !bytes.Equal(saved, after) {
		t.Fatal("changed existing preview")
	}
	blocked := filepath.Join(dir, "blocked.json")
	if err := os.WriteFile(filepath.Join(dir, "blocked.svg"), []byte("unrelated preview"), 0600); err != nil {
		t.Fatal(err)
	}
	args[len(args)-2] = blocked // --out value, before --dry-run
	stdout.Reset()
	stderr.Reset()
	if Run(args, &stdout, &stderr) == 0 {
		t.Fatal("accepted a stale SVG preview")
	}
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatalf("created a usable plan beside stale SVG: %v", err)
	}
	// Tampered source binding must fail offline before any current-page query.
	preview.BoardSHA256 = "tampered"
	writeRouteJSON(t, filepath.Join(dir, "tampered.json"), preview)
	args = []string{"--host", "127.0.0.1", "--ports", "1", "pcb", "route", "replace", "--board", board, "--from", request, "--project", "project-uuid", "--doc", "pcb-uuid", "--window", "w", "--plan", filepath.Join(dir, "tampered.json"), "--out", filepath.Join(dir, "execute.json")}
	stdout.Reset()
	stderr.Reset()
	if Run(args, &stdout, &stderr) == 0 || !strings.Contains(stderr.String(), "fresh unexecuted plan") {
		t.Fatalf("bad provenance behavior: %s", stderr.String())
	}
}

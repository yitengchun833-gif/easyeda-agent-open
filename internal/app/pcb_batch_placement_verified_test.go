package app

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

const verifiedPCBPlacementResult = `{"verified":true,"partial":false,"requestedCount":1,"count":1,"moved":[{"primitiveId":"kept-component","verified":true}],"attempted":["kept-component"],"skipped":[],"notApplied":[],"unverified":[],"remaining":[]}`

func TestPCBComponentPlacementResultRequiresCompleteReadback(t *testing.T) {
	for _, tc := range []struct {
		name, patch string
	}{
		{"partial", `{"partial":true}`},
		{"unverified", `{"verified":false}`},
		{"unknown", `{"executionState":"unknown"}`},
		{"missing-counter", `{"requestedCount":null}`},
		{"incomplete-count", `{"requestedCount":2}`},
		{"invalid-count", `{"count":1.5}`},
		{"skipped", `{"skipped":[{"primitiveId":"locked","reason":"locked"}]}`},
		{"not-applied", `{"notApplied":[{"primitiveId":"dropped"}]}`},
		{"unread", `{"unverified":[{"primitiveId":"unknown"}]}`},
		{"remaining", `{"remaining":["not-dispatched"]}`},
		{"unverified-pose", `{"moved":[{"primitiveId":"kept-component","verified":false}]}`},
		{"missing-attempt", `{"attempted":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var result, patch map[string]any
			if err := json.Unmarshal([]byte(verifiedPCBPlacementResult), &result); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.patch), &patch); err != nil {
				t.Fatal(err)
			}
			for key, value := range patch {
				result[key] = value
			}
			if err := pcbComponentPlacementError("pcb.components.move", result); err == nil {
				t.Fatalf("accepted incomplete batch: %+v", result)
			}
		})
	}
}

func TestPCBComponentPlacementVerifiedAcrossEntryPoints(t *testing.T) {
	for _, action := range []string{"pcb.components.move", "pcb.components.arrange"} {
		for _, result := range []string{
			verifiedPCBPlacementResult,
			`{"count":1,"moved":[{"primitiveId":"kept-component"}]}`,
			`{"verified":false,"partial":true,"moved":[{"primitiveId":"kept-component","verified":true}],"unverified":[{"primitiveId":"unknown-component"}],"remaining":["not-dispatched"]}`,
		} {
			for _, entry := range []string{"dispatch", "capture", "request", "apply"} {
				t.Run(action+"/"+entry+"/"+result, func(t *testing.T) {
					response := `{"ok":true,"result":` + result + `}`
					cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
					defer cleanup()
					var out, stderr bytes.Buffer
					var err error
					switch entry {
					case "dispatch":
						err = dispatch(cfg, action, "w1", nil, &out, &stderr)
					case "capture":
						_, err = dispatchCapture(cfg, action, "w1", nil, &out)
					case "request":
						var res *actionResult
						res, err = requestAction(cfg, action, "w1", nil)
						if res == nil || res.Result == nil {
							t.Fatal("lost partial result")
						}
					case "apply":
						r := applyRunner{cfg: cfg, window: "w1"}
						var res any
						res, err = r.runAction(action, nil, time.Second)
						if res == nil {
							t.Fatal("lost partial result")
						}
					}
					wantFailure := result != verifiedPCBPlacementResult
					if (err != nil) != wantFailure {
						t.Fatalf("err=%v; want failure=%v", err, wantFailure)
					}
					if wantFailure {
						var typed *unverifiedPCBComponentPlacementError
						if !errors.As(err, &typed) {
							t.Fatalf("lost typed error: %v", err)
						}
					}
					if (entry == "dispatch" || entry == "capture") && strings.TrimSpace(out.String()) != response {
						t.Fatalf("lost original response: %s", out.String())
					}
					if len(calls.snapshot()) != 1 {
						t.Fatalf("batch replayed: %+v", calls.snapshot())
					}
				})
			}
		}
	}
}

func TestPCBComponentPlacementApplyCannotReplayOrContinuePartialBatch(t *testing.T) {
	const partialResult = `{"verified":false,"partial":true,"moved":[{"primitiveId":"kept-component","verified":true}],"attempted":["kept-component","unknown-component"],"unverified":[{"primitiveId":"unknown-component"}],"remaining":["not-dispatched"]}`
	for _, entry := range []string{"action", "pcb move --dx 1 --dy 2 --ids kept-component,unknown-component", "pcb arrange --ids kept-component,unknown-component"} {
		for _, mode := range []string{"verify", "retry", "continue", "default-continue", "prompt"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				placements := 0
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.components.move" || call.Action == "pcb.components.arrange" {
						placements++
						return `{"ok":true,"result":` + partialResult + `}`
					}
					return `{"ok":true,"result":{}}`
				})
				defer cleanup()
				step := playbookStep{ID: "partial-placement", Action: "pcb.components.move", Payload: map[string]any{}}
				if entry != "action" {
					step.Action, step.Run = "", entry
				}
				pb := &playbook{Version: 1}
				switch mode {
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.components.list"}
				case "retry":
					retries := 1
					step.Retry = &retries
				case "continue", "prompt":
					step.OnFail = mode
				case "default-continue":
					cont := true
					pb.Defaults.ContinueOnError = &cont
				}
				pb.Steps = []playbookStep{step, {ID: "dependent", Action: "pcb.line.create", Payload: map[string]any{}}}
				var out, stderr bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &stderr, pb: pb, vars: map[string]string{}, window: "w1", yes: true, quiet: true, journalPath: journal, toIdx: 1}
				err := r.execute()
				var typed *unverifiedPCBComponentPlacementError
				if err == nil || placements != 1 || !errors.As(err, &typed) {
					t.Fatalf("partial batch bypassed: %v; placements=%d; stderr=%s", err, placements, stderr.String())
				}
				for _, call := range calls.snapshot() {
					if call.Action == "pcb.components.list" || call.Action == "pcb.line.create" {
						t.Fatalf("dependent/verify action dispatched: %+v", calls.snapshot())
					}
				}
				log, err := os.ReadFile(journal)
				if err != nil || !bytes.Contains(log, []byte(`"status":"fail"`)) || bytes.Contains(log, []byte(`"status":"ok"`)) {
					t.Fatalf("lost failure: %s (%v)", log, err)
				}
				var recorded journalEntry
				var expected map[string]any
				lines := bytes.Split(bytes.TrimSpace(log), []byte("\n"))
				if err := json.Unmarshal(lines[len(lines)-1], &recorded); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(partialResult), &expected); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(recorded.Result, expected) {
					t.Fatalf("lost partial evidence in quiet mode: %s", log)
				}
			})
		}
	}
}

func TestPCBAddComponentPlacementVerification(t *testing.T) {
	const partialResult = `{"primitiveId":"created-once","partial":true,"verified":false,"netMapping":{"verified":false,"readbackAvailable":true,"mismatched":[{"primitiveId":"pad-kept","padNumber":"1","net":"OLD","expectedNet":"SIG"}]}}`
	for _, result := range []string{
		`{"primitiveId":"legacy-created","assignedNets":1}`,
		`{"primitiveId":"verified-created","verified":true,"partial":false}`,
		`{"primitiveId":"unverified-created","verified":false}`,
		partialResult,
	} {
		for _, entry := range []string{"dispatch", "capture", "request", "apply"} {
			t.Run(entry+"/"+result, func(t *testing.T) {
				response := `{"ok":true,"result":` + result + `}`
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
				defer cleanup()
				var out, stderr bytes.Buffer
				var err error
				switch entry {
				case "dispatch":
					err = dispatch(cfg, "pcb.add_component", "w1", nil, &out, &stderr)
				case "capture":
					_, err = dispatchCapture(cfg, "pcb.add_component", "w1", nil, &out)
				case "request":
					_, err = requestAction(cfg, "pcb.add_component", "w1", nil)
				case "apply":
					r := applyRunner{cfg: cfg, window: "w1"}
					_, err = r.runAction("pcb.add_component", nil, time.Second)
				}
				wantFailure := strings.Contains(result, `"verified":false`)
				if (err != nil) != wantFailure {
					t.Fatalf("err=%v; want failure=%v", err, wantFailure)
				}
				if wantFailure {
					var typed *unverifiedPCBComponentPlacementError
					if !errors.As(err, &typed) || typed.result["primitiveId"] == nil {
						t.Fatalf("lost partial placement identity: %v", err)
					}
				}
				if (entry == "dispatch" || entry == "capture") && strings.TrimSpace(out.String()) != response {
					t.Fatalf("lost original response: %s", out.String())
				}
				if len(calls.snapshot()) != 1 {
					t.Fatalf("placement replayed: %+v", calls.snapshot())
				}
			})
		}
	}
	for _, entry := range []string{"action", "pcb add-component --library lib --uuid device --x 10 --y 20"} {
		for _, mode := range []string{"retry", "verify", "continue"} {
			t.Run(entry+"/"+mode, func(t *testing.T) {
				creates := 0
				cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
					if call.Action == "pcb.add_component" {
						creates++
						return `{"ok":true,"result":` + partialResult + `}`
					}
					return `{"ok":true,"result":{}}`
				})
				defer cleanup()
				step := playbookStep{ID: "partial-add", Action: "pcb.add_component", Payload: map[string]any{}}
				if entry != "action" {
					step.Action, step.Run = "", entry
				}
				switch mode {
				case "retry":
					retries := 1
					step.Retry = &retries
				case "verify":
					step.Verify = &verifyBlock{Action: "pcb.components.list"}
				case "continue":
					step.OnFail = "continue"
				}
				pb := &playbook{Version: 1, Steps: []playbookStep{step, {ID: "dependent", Action: "pcb.line.create", Payload: map[string]any{}}}}
				var out, stderr bytes.Buffer
				journal := filepath.Join(t.TempDir(), "journal.jsonl")
				r := applyRunner{cfg: cfg, stdout: &out, stderr: &stderr, pb: pb, vars: map[string]string{}, window: "w1", yes: true, quiet: true, journalPath: journal, toIdx: 1}
				var typed *unverifiedPCBComponentPlacementError
				if err := r.execute(); !errors.As(err, &typed) || creates != 1 {
					t.Fatalf("partial creation bypassed: %v; creates=%d", err, creates)
				}
				for _, call := range calls.snapshot() {
					if call.Action == "pcb.components.list" || call.Action == "pcb.line.create" {
						t.Fatalf("dependent/verify action dispatched: %+v", calls.snapshot())
					}
				}
				log, err := os.ReadFile(journal)
				if err != nil {
					t.Fatal(err)
				}
				lines := bytes.Split(bytes.TrimSpace(log), []byte("\n"))
				var recorded journalEntry
				var expected map[string]any
				if err := json.Unmarshal(lines[len(lines)-1], &recorded); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(partialResult), &expected); err != nil {
					t.Fatal(err)
				}
				if recorded.Status != "fail" || !reflect.DeepEqual(recorded.Result, expected) {
					t.Fatalf("lost original ID or net mapping in quiet journal: %s", log)
				}
			})
		}
	}
}

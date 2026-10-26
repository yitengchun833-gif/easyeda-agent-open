package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPCBPlaceBatchCLI(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "verified", true: "partial"}[partial], func(t *testing.T) {
			response := `{"ok":true,"result":` + verifiedPCBPlacementResult + `}`
			if partial {
				response = `{"ok":true,"result":{"partial":true,"verified":false,"moved":[{"primitiveId":"first","verified":true}],"remaining":["last"]}}`
			}
			cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
			defer cleanup()
			file := filepath.Join(t.TempDir(), "placements.json")
			const input = `{"placements":[{"primitiveId":"first","x":200,"y":-30,"rotation":180,"expected":{"uniqueId":"source-first"}}]}`
			if err := os.WriteFile(file, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			var out, stderr bytes.Buffer
			cmd := newPcbCmd(cfg, &out, &stderr)
			cmd.SetArgs([]string{"place-batch", "--from", file, "--window", "w1"})
			err := cmd.Execute()
			if (err != nil) != partial {
				t.Fatalf("err=%v; partial=%v", err, partial)
			}
			got := calls.snapshot()
			if len(got) != 1 || got[0].Action != "pcb.components.place" {
				t.Fatalf("expected exactly one batch request: %+v", got)
			}
			var expected map[string]any
			if err := json.Unmarshal([]byte(input), &expected); err != nil {
				t.Fatal(err)
			}
			actual, _ := json.Marshal(got[0].Payload["placements"])
			want, _ := json.Marshal(expected["placements"])
			if !bytes.Equal(actual, want) || !strings.Contains(out.String(), `"result"`) {
				t.Fatalf("lost payload or original result: %s; %s", actual, out.String())
			}
		})
	}
}

func TestPCBPlaceBatchRejectsEmptyInputWithoutDispatch(t *testing.T) {
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { t.Fatal("unexpected dispatch"); return "" })
	defer cleanup()
	for _, body := range []string{`null`, `{}`, `{"placements":[]}`, `[]`, `{"placements":"a"}`, `!invalid`} {
		file := filepath.Join(t.TempDir(), "placements.json")
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		cmd := newPcbCmd(cfg, &out, &out)
		cmd.SetArgs([]string{"place-batch", "--from", file})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("accepted %s", body)
		}
	}
	if len(calls.snapshot()) != 0 {
		t.Fatal("invalid input reached daemon")
	}
}

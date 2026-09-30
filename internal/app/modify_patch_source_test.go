package app

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestModifyPatchFile(t *testing.T) {
	for _, domain := range []string{"sch", "pcb"} {
		for _, tc := range []struct {
			name, content string
			extra         []string
			wantErr       bool
		}{
			{"utf8", `{"rotation":90}`, nil, false},
			{"powershell-bom", "\xef\xbb\xbf{\"rotation\":90}", nil, false},
			{"null", "null", nil, true},
			{"array", "[]", nil, true},
			{"empty", "", nil, true},
			{"invalid", "{rotation:90}", nil, true},
			{"utf16", "\xff\xfe{\x00}\x00", nil, true},
			{"conflict", `{"rotation":90}`, []string{"--patch", `{"x":1}`}, true},
		} {
			t.Run(domain+"/"+tc.name, func(t *testing.T) {
				cfg, captured, cleanup := newCapturingDaemon(t)
				defer cleanup()
				path := filepath.Join(t.TempDir(), "补丁 file.json")
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
				var cmd *cobra.Command
				if domain == "sch" {
					cmd = newSchCmd(cfg, io.Discard, io.Discard)
				} else {
					cmd = newPcbCmd(cfg, io.Discard, io.Discard)
				}
				cmd.SetOut(io.Discard)
				cmd.SetErr(io.Discard)
				cmd.SetArgs(append([]string{"modify", "--id", "p1", "--patch-file", path}, tc.extra...))
				err := cmd.Execute()
				if (err != nil) != tc.wantErr {
					t.Fatalf("error = %v, wantErr %v", err, tc.wantErr)
				}
				captured.mu.Lock()
				defer captured.mu.Unlock()
				if tc.wantErr {
					if captured.action != "" {
						t.Fatalf("invalid input dispatched %s", captured.action)
					}
				} else {
					patch, ok := captured.payload["patch"].(map[string]any)
					if !ok || patch["rotation"] != float64(90) {
						t.Fatalf("wrong payload: %#v", captured.payload)
					}
				}
			})
		}
	}
}

func TestModifyPatchFileBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, domain, content, wantError string
		missing                          bool
		extra                            []string
		want                             map[string]any
	}{
		{name: "sch missing file", domain: "sch", missing: true, wantError: "read --patch-file"},
		{name: "pcb missing file", domain: "pcb", missing: true, wantError: "read --patch-file"},
		{name: "explicit zero overrides file", domain: "sch", content: `{"rotation":90,"designator":"R12"}`, extra: []string{"--rotation", "0"}, want: map[string]any{"rotation": float64(0), "designator": "R12"}},
		{name: "center rejects rotation", domain: "pcb", content: `{"rotation":90}`, extra: []string{"--center", "--x", "10", "--y", "20"}, wantError: "rotation change are mutually exclusive"},
		{name: "center rejects anchor x", domain: "pcb", content: `{"x":90}`, extra: []string{"--center", "--x", "10", "--y", "20"}, wantError: `conflicts with "x"`},
		{name: "center rejects anchor y", domain: "pcb", content: `{"y":90}`, extra: []string{"--center", "--x", "10", "--y", "20"}, wantError: `conflicts with "y"`},
		{name: "empty explicit inline conflicts", domain: "sch", content: `{"rotation":90}`, extra: []string{"--patch", ""}, wantError: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, captured, cleanup := newCapturingDaemon(t)
			defer cleanup()
			path := filepath.Join(t.TempDir(), "补丁 file.json")
			if !tc.missing {
				if err := os.WriteFile(path, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			var cmd *cobra.Command
			if tc.domain == "sch" {
				cmd = newSchCmd(cfg, io.Discard, io.Discard)
			} else {
				cmd = newPcbCmd(cfg, io.Discard, io.Discard)
			}
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			cmd.SetArgs(append([]string{"modify", "--id", "p1", "--patch-file", path}, tc.extra...))
			err := cmd.Execute()
			captured.mu.Lock()
			defer captured.mu.Unlock()
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("error = %v, want %q", err, tc.wantError)
				}
				if captured.action != "" {
					t.Fatalf("invalid input dispatched %s", captured.action)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if captured.action != "schematic.component.modify" || captured.payload["primitiveId"] != "p1" || !reflect.DeepEqual(captured.payload["patch"], tc.want) {
					t.Fatalf("unexpected dispatch: %s %#v", captured.action, captured.payload)
				}
			}
		})
	}
}

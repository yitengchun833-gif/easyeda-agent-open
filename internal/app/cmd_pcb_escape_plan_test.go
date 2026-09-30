package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEscapePlanCommandIsOfflineAndWritesProvenance(t *testing.T) {
	in, s := escapeFixture()
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.json")
	board := filepath.Join(dir, "board.json")
	out := filepath.Join(dir, "report.json")
	lraw, _ := json.Marshal(map[string]any{"schemaVersion": 3, "units": "mil", "coordinateSemantic": "footprint-anchor", "routing": in})
	braw, _ := json.Marshal(s)
	if err := os.WriteFile(layout, lraw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(board, braw, 0600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := newPcbEscapePlanCmd(&stdout, &stderr)
	cmd.SetArgs([]string{"--from", layout, "--board", board, "--out", out})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%v %s", err, stderr.String())
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var rep pcbEscapeReport
	if err := json.Unmarshal(raw, &rep); err != nil {
		t.Fatal(err)
	}
	if rep.Status != "pass" || rep.BoardSHA256 != sha256String(braw) || rep.LayoutSHA256 != sha256String(lraw) {
		t.Fatalf("missing provenance %+v", rep)
	}
	if svg, err := os.ReadFile(filepath.Join(dir, "report.svg")); err != nil || !bytes.Contains(svg, []byte("<svg")) {
		t.Fatalf("missing SVG %v", err)
	}
	// Constructor takes no appConfig: even a dead/unconfigured daemon cannot
	// change this execution path, and inputs must remain byte-identical.
	if got, _ := os.ReadFile(board); !bytes.Equal(got, braw) {
		t.Fatal("offline planner edited input")
	}
}

func TestEscapePlanCommandRequiresV3Routing(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.json")
	board := filepath.Join(dir, "board.json")
	_ = os.WriteFile(layout, []byte(`{"schemaVersion":2,"units":"mil"}`), 0600)
	_ = os.WriteFile(board, []byte(`{}`), 0600)
	var b bytes.Buffer
	cmd := newPcbEscapePlanCmd(&b, &b)
	cmd.SetArgs([]string{"--from", layout, "--board", board, "--out", filepath.Join(dir, "out.json")})
	if err := cmd.Execute(); err == nil {
		t.Fatal("old schema incorrectly got escape proof")
	}
}

func TestEscapePlanCommandCannotOverwriteInputs(t *testing.T) {
	dir := t.TempDir()
	layout := filepath.Join(dir, "layout.json")
	board := filepath.Join(dir, "board.json")
	original := []byte(`{"preserve":true}`)
	_ = os.WriteFile(layout, original, 0600)
	_ = os.WriteFile(board, original, 0600)
	for _, out := range []string{layout, board, filepath.Join(dir, "report.svg")} {
		var b bytes.Buffer
		cmd := newPcbEscapePlanCmd(&b, &b)
		cmd.SetArgs([]string{"--from", layout, "--board", board, "--out", out})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("unsafe output accepted: %s", out)
		}
	}
	for _, p := range []string{layout, board} {
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, original) {
			t.Fatalf("input overwritten %s", p)
		}
	}
}

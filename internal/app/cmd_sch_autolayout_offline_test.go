package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOfflineAutolayoutUsesExistingSolverWithoutDaemon(t *testing.T) {
	dir := t.TempDir()
	var native map[string]any
	if err := json.Unmarshal([]byte(autolayoutScene("p", 100, 100, 2)), &native); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(native["result"])
	from := filepath.Join(dir, "geometry.json")
	if err := os.WriteFile(from, raw, 0600); err != nil {
		t.Fatal(err)
	}
	spec := filepath.Join(dir, "spec.json")
	if err := os.WriteFile(spec, []byte(`{"modules":[{"name":"MCU","zone":"center","core":"U1","parts":["U1"]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	window := "unused"
	cfg, requests, closeDaemon := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { t.Error("offline preview contacted daemon"); return "" })
	defer closeDaemon()
	cmd := newAutolayoutCmd(cfg, &window, &out, &diagnostic)
	cmd.SetArgs([]string{"--from", from, "--spec", spec, "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report alReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if !report.OK || len(report.Placements) != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}
	if len(requests.snapshot()) != 0 {
		t.Fatal("offline preview sent requests")
	}
	after, _ := os.ReadFile(from)
	if !bytes.Equal(raw, after) {
		t.Fatal("source measurement changed")
	}
	cmd = newAutolayoutCmd(&appConfig{}, &window, &out, &diagnostic)
	cmd.SetArgs([]string{"--from", from, "--spec", spec, "--apply"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("apply was not rejected: %v", err)
	}
}

func TestOfflineAutolayoutRejectsMissingMeasurement(t *testing.T) {
	file := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(file, []byte(`{"components":[{"componentType":"part","designator":"R1"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := offlineAutolayout(file, alSpec{}, defaultAutolayoutRules(), true, &bytes.Buffer{}); err == nil {
		t.Fatal("missing geometry passed")
	}
}

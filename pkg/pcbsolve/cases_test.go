package pcbsolve_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

type caseExpectation struct {
	Status                  string `json:"status"`
	MinCandidates           int    `json:"minCandidates"`
	MovedGroups             int    `json:"movedGroups"`
	NewVias                 int    `json:"newVias"`
	RequireIndependentCheck bool   `json:"requireIndependentCheck"`
}

func readCaseJSON(t *testing.T, path string, dst any) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func TestFileCases(t *testing.T) {
	dirs, err := filepath.Glob("testdata/*")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(dirs)
	if len(dirs) < 3 {
		t.Fatalf("expected at least three solver cases, got %d", len(dirs))
	}
	for _, dir := range dirs {
		dir := dir
		t.Run(filepath.Base(dir), func(t *testing.T) {
			var board pcbmodel.Board
			var request pcbsolve.Request
			var expect caseExpectation
			readCaseJSON(t, filepath.Join(dir, "board.json"), &board)
			readCaseJSON(t, filepath.Join(dir, "request.json"), &request)
			readCaseJSON(t, filepath.Join(dir, "expect.json"), &expect)
			report, solveErr := pcbsolve.Solve(context.Background(), board, request)
			if solveErr != nil {
				t.Fatalf("solve: %v", solveErr)
			}
			if report.Status != expect.Status || len(report.Candidates) < expect.MinCandidates {
				t.Fatalf("status/candidates: got %s/%d want %s/>=%d; reason=%s", report.Status, len(report.Candidates), expect.Status, expect.MinCandidates, report.Reason)
			}
			if len(report.Candidates) == 0 {
				return
			}
			candidate := report.Candidates[0]
			if candidate.Metrics.MovedGroups != expect.MovedGroups || candidate.Metrics.NewVias != expect.NewVias {
				t.Fatalf("metrics: %+v", candidate.Metrics)
			}
			if expect.RequireIndependentCheck {
				if err := pcbsolve.Check(context.Background(), board, request, candidate); err != nil {
					t.Fatalf("independent check: %v", err)
				}
			}
		})
	}
}

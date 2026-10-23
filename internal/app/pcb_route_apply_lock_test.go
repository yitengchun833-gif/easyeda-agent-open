package app

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestPCBRouteApplyLockChild(t *testing.T) {
	project := os.Getenv("EASYEDA_ROUTE_LOCK_TEST_PROJECT")
	if project == "" {
		return
	}
	release, err := acquirePCBRouteApplyLock(project, "pcb")
	if os.Getenv("EASYEDA_ROUTE_LOCK_TEST_MODE") == "busy" {
		if err == nil {
			release()
			os.Exit(2)
		}
		os.Exit(0)
	}
	if err != nil {
		os.Exit(3)
	}
	// Deliberately exit with the live handle, as after a process crash.
	os.Exit(0)
}

func TestPCBRouteApplyLockIsCrossProcessAndDiesWithOwner(t *testing.T) {
	project := t.TempDir()
	release, err := acquirePCBRouteApplyLock(project, "pcb")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	run := func(mode string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPCBRouteApplyLockChild$")
		cmd.Env = append(os.Environ(), "EASYEDA_ROUTE_LOCK_TEST_PROJECT="+project, "EASYEDA_ROUTE_LOCK_TEST_MODE="+mode)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("lock child %s: %v %s", mode, err, out)
		}
	}
	run("busy")
	release()
	run("exit-held")
	again, err := acquirePCBRouteApplyLock(project, "pcb")
	if err != nil {
		t.Fatalf("exited owner left a stale lease: %v", err)
	}
	again()
}

func TestPCBRouteApplyConcurrentCommandStopsBeforeBaseline(t *testing.T) {
	in, before := routeSolverFixture()
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	plan := filepath.Join(dir, "plan.json")
	braw := writeRouteJSON(t, board, before)
	rraw := writeRouteJSON(t, request, in)
	report := newPCBRouteReport(braw, rraw)
	if err := solvePCBRoute(context.Background(), in, before, &report); err != nil {
		t.Fatal(err)
	}
	writeRouteJSON(t, plan, report)
	project := dir
	release, err := acquirePCBRouteApplyLock(project, "pcb")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cfg, calls, cleanup := newAutolayoutTestDaemon(t, func(_ int, call autolayoutTestCall) string {
		t.Errorf("contending apply dispatched %s", call.Action)
		return ""
	})
	defer cleanup()
	cfg.project, cfg.doc = project, "pcb"
	window := "w1"
	for _, name := range []string{"out-one.json", "out-two.json"} {
		var out, stderr bytes.Buffer
		cmd := newPcbRouteApplyCmd(cfg, &window, &out, &stderr)
		cmd.SetArgs([]string{"--board", board, "--from", request, "--plan", plan, "--out", filepath.Join(dir, name)})
		if err := cmd.Execute(); err == nil {
			t.Fatal("concurrent apply accepted a different out path")
		}
	}
	if len(calls.snapshot()) != 0 {
		t.Fatal("lease did not cover the fresh baseline")
	}
}

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func routeSolverFixture() (pcbRouteRequest, *boardSnapshot) {
	_, s := escapeFixture()
	return pcbRouteRequest{SchemaVersion: 1, Units: "mil", Net: "A", Layer: 1, WidthMil: 4, From: "U1.1", To: "J1.1", StepMil: 5, MaxDetourMil: 50, MaxStates: 100000}, s
}

func routeJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func writeRouteJSON(t *testing.T, path string, v any) []byte {
	t.Helper()
	b := routeJSON(t, v)
	if err := os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	return b
}

func requireRouteSVG(t *testing.T, path string, fragments ...string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range fragments {
		if !bytes.Contains(raw, []byte(fragment)) {
			t.Fatalf("preview %s lacks %q", path, fragment)
		}
	}
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	for {
		if _, err := decoder.Token(); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("invalid SVG %s: %v", path, err)
		}
	}
	return raw
}

func TestPCBRouteSubcommandsOfflineRoundTrip(t *testing.T) {
	in, s := routeSolverFixture()
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	plan := filepath.Join(dir, "plan.json")
	check := filepath.Join(dir, "check.json")
	braw := writeRouteJSON(t, board, s)
	rraw := writeRouteJSON(t, request, in)
	var stdout, stderr bytes.Buffer
	// Exercise the real root/subcommand registration with an unusable daemon.
	for _, args := range [][]string{
		{"--host", "127.0.0.1", "--ports", "1", "pcb", "route", "solve", "--board", board, "--from", request, "--out", plan},
		{"--host", "127.0.0.1", "--ports", "1", "pcb", "route", "check", "--board", board, "--from", request, "--plan", plan, "--out", check},
	} {
		if code := Run(args, &stdout, &stderr); code != 0 {
			t.Fatalf("code %d: %s", code, stderr.String())
		}
	}
	for _, path := range []string{plan, check} {
		var rep pcbRouteReport
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &rep); err != nil {
			t.Fatal(err)
		}
		if rep.Status != "pass" || rep.BoardSHA256 != sha256String(braw) || rep.RequestSHA256 != sha256String(rraw) || rep.LengthMil != 200 || rep.Preview != strings.TrimSuffix(path, ".json")+".svg" {
			t.Fatalf("missing proof %+v", rep)
		}
		requireRouteSVG(t, rep.Preview, "PASS", "candidate route", "FROM U1.1", "TO J1.1", "bounded search envelope")
	}
	for path, want := range map[string][]byte{board: braw, request: rraw} {
		if got, _ := os.ReadFile(path); !bytes.Equal(got, want) {
			t.Fatal("offline command changed inputs")
		}
	}
	stdout.Reset()
	stderr.Reset()
	if code := Run([]string{"pcb", "route", "solve", "--help"}, &stdout, &stderr); code != 0 || !strings.Contains(stdout.String(), "--board") || !strings.Contains(stdout.String(), "maxStates") {
		t.Fatalf("help missing contract: %s %s", stdout.String(), stderr.String())
	}
}

func TestPCBRouteCheckerDoesNotTrustPlanStatusOrMetrics(t *testing.T) {
	in, s := routeSolverFixture()
	s.Copper.Lines = []any{map[string]any{"primitiveId": "upper-obstacle", "net": "OTHER", "layer": 1.0, "startX": 150.0, "startY": 115.0, "endX": 150.0, "endY": 125.0, "lineWidth": 4.0}}
	braw, rraw := routeJSON(t, s), routeJSON(t, in)
	plan := newPCBRouteReport(braw, rraw)
	if err := solvePCBRoute(context.Background(), in, s, &plan); err != nil {
		t.Fatal(err)
	}
	// Reported metrics are informational. The checker computes them independently.
	plan.LengthMil = -123
	plan.Bends = 99
	plan.Search.Points = nil
	report := newPCBRouteReport(braw, rraw)
	if err := checkPCBRoute(context.Background(), in, s, plan, &report); err != nil || report.LengthMil != 200 || report.Bends != 0 {
		t.Fatalf("trusted stored metrics %+v %v", report, err)
	}
	for _, tc := range []struct {
		name   string
		change func(*pcbRouteReport)
	}{
		{"endpoint", func(p *pcbRouteReport) { p.Candidate.Points[0][0]++ }},
		{"net", func(p *pcbRouteReport) { p.Candidate.Net = "B" }},
		{"layer", func(p *pcbRouteReport) { p.Candidate.Layer = 2 }},
		{"width", func(p *pcbRouteReport) { p.Candidate.WidthMil = 1 }},
		{"provenance", func(p *pcbRouteReport) { p.BoardSHA256 = "stale" }},
		{"missing candidate", func(p *pcbRouteReport) { p.Candidate = nil }},
		{"collision", func(p *pcbRouteReport) { p.Candidate.Points = [][2]float64{{50, 80}, {90, 120}, {210, 120}, {250, 80}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copy pcbRouteReport
			_ = json.Unmarshal(routeJSON(t, plan), &copy)
			tc.change(&copy)
			rep := newPCBRouteReport(braw, rraw)
			if err := checkPCBRoute(context.Background(), in, s, copy, &rep); err == nil || rep.Status == "pass" {
				t.Fatalf("tampered plan passed %+v", rep)
			}
		})
	}
}

func TestPCBRouteCheckRendersRejectedCandidate(t *testing.T) {
	in, s := routeSolverFixture()
	s.Copper.Lines = []any{map[string]any{"primitiveId": "block", "net": "OTHER", "layer": 1.0, "startX": 150.0, "startY": 70.0, "endX": 150.0, "endY": 90.0, "lineWidth": 12.0}}
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	planPath := filepath.Join(dir, "plan.json")
	checkPath := filepath.Join(dir, "check.json")
	braw, rraw := writeRouteJSON(t, board, s), writeRouteJSON(t, request, in)
	plan := newPCBRouteReport(braw, rraw)
	plan.Status = "pass"
	plan.Candidate = &pcbRouteCandidate{Net: in.Net, Layer: in.Layer, WidthMil: in.WidthMil, Points: [][2]float64{{50, 80}, {250, 80}}}
	writeRouteJSON(t, planPath, plan)
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"pcb", "route", "check", "--board", board, "--from", request, "--plan", planPath, "--out", checkPath}, &stdout, &stderr); code == 0 {
		t.Fatal("colliding candidate check exited zero")
	}
	var report pcbRouteReport
	raw, err := os.ReadFile(checkPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "fail" || report.Candidate == nil {
		t.Fatalf("rejected candidate was lost: %+v", report)
	}
	requireRouteSVG(t, filepath.Join(dir, "check.svg"), "FAIL", "candidate route", "#fb7185", "violates clearance")
}

func TestPCBRouteRequestRejectsUnknownAndUnsupportedGeometry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*pcbRouteRequest, *boardSnapshot)
		status string
	}{
		{"missing pad shape", func(in *pcbRouteRequest, s *boardSnapshot) { s.Components[1].Pads[0].Shape = nil }, "incomplete"},
		{"partial snapshot", func(in *pcbRouteRequest, s *boardSnapshot) { s.Partial = []string{"pads unavailable"} }, "incomplete"},
		{"unknown inventory", func(in *pcbRouteRequest, s *boardSnapshot) { s.Copper.Availability["poured"] = "unknown" }, "incomplete"},
		{"null inventory", func(in *pcbRouteRequest, s *boardSnapshot) { s.Copper.Vias = nil }, "incomplete"},
		{"fallback rules", func(in *pcbRouteRequest, s *boardSnapshot) { s.Rules.Source = "fallback" }, "incomplete"},
		{"unknown stackup", func(in *pcbRouteRequest, s *boardSnapshot) { s.CopperLayers = 0 }, "incomplete"},
		{"inner layer", func(in *pcbRouteRequest, s *boardSnapshot) { in.Layer = 15 }, "incomplete"},
		{"bad width", func(in *pcbRouteRequest, s *boardSnapshot) { in.WidthMil = 1 }, "fail"},
		{"net mismatch", func(in *pcbRouteRequest, s *boardSnapshot) { in.Net = "B" }, "fail"},
		{"unknown endpoint", func(in *pcbRouteRequest, s *boardSnapshot) { in.To = "J9.1" }, "incomplete"},
		{"duplicate endpoint", func(in *pcbRouteRequest, s *boardSnapshot) {
			s.Components[1].Pads = append(s.Components[1].Pads, s.Components[1].Pads[0])
		}, "incomplete"},
		{"wrong units", func(in *pcbRouteRequest, s *boardSnapshot) { in.Units = "mm" }, "fail"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, s := routeSolverFixture()
			tc.change(&in, s)
			rep := newPCBRouteReport(nil, nil)
			if err := solvePCBRoute(context.Background(), in, s, &rep); err == nil || rep.Status != tc.status || rep.Candidate != nil {
				t.Fatalf("%+v %v", rep, err)
			}
		})
	}
}

func TestPCBRouteHonorsCutoutsRegionsAndStrokedFills(t *testing.T) {
	poly := []any{140.0, 70.0, "L", 160.0, 70.0, 160.0, 90.0, 140.0, 90.0, 140.0, 70.0}
	for _, kind := range []string{"slot", "no-wires", "static-copper", "poured-copper"} {
		t.Run(kind, func(t *testing.T) {
			in, s := routeSolverFixture()
			switch kind {
			case "slot":
				s.Copper.Fills = []any{map[string]any{"primitiveId": "slot", "layer": 12.0, "source": poly, "geometryAvailable": true, "fillMode": "solid", "lineWidth": 0.0}}
			case "no-wires":
				s.Copper.Regions = []any{map[string]any{"primitiveId": "ban", "layer": 1.0, "ruleType": []any{5.0}, "source": poly}}
			case "static-copper":
				s.Copper.Fills = []any{map[string]any{"primitiveId": "fill", "net": "OTHER", "layer": 1.0, "source": poly, "geometryAvailable": true, "fillMode": "solid", "lineWidth": 10.0}}
			case "poured-copper":
				s.Copper.Poured = []any{map[string]any{"primitiveId": "pour", "net": "OTHER", "layer": 1.0, "fills": []any{map[string]any{"id": "f1", "source": poly, "fill": true, "lineWidth": 10.0}}}}
			}
			rep := newPCBRouteReport(nil, nil)
			if err := solvePCBRoute(context.Background(), in, s, &rep); err != nil || rep.LengthMil <= 200 {
				t.Fatalf("did not detour around %s: %+v %v", kind, rep, err)
			}
			req, clear, _, err := preparePCBRouteRequest(in, s)
			if err != nil {
				t.Fatal(err)
			}
			if clear(req.From, req.To) {
				t.Fatal("straight copper crossed obstacle")
			}
			if kind == "static-copper" || kind == "poured-copper" {
				if clear([2]float64{120, 98}, [2]float64{180, 98}) {
					t.Fatal("polygon stroke width omitted")
				}
			}
			check := newPCBRouteReport(nil, nil)
			if err := checkPCBRoute(context.Background(), in, s, rep, &check); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPCBRouteReportsIncompleteSearchAndProtectsFiles(t *testing.T) {
	in, s := routeSolverFixture()
	in.MaxStates = 1
	s.Copper.Lines = []any{map[string]any{"primitiveId": "wall", "net": "OTHER", "layer": 1.0, "startX": 150.0, "startY": 65.0, "endX": 150.0, "endY": 95.0, "lineWidth": 4.0}}
	dir := t.TempDir()
	board := filepath.Join(dir, "board.json")
	request := filepath.Join(dir, "request.json")
	plan := filepath.Join(dir, "plan.json")
	braw := writeRouteJSON(t, board, s)
	writeRouteJSON(t, request, in)
	var stdout, stderr bytes.Buffer
	args := []string{"pcb", "route", "solve", "--board", board, "--from", request, "--out", plan}
	if code := Run(args, &stdout, &stderr); code == 0 {
		t.Fatal("budget exhaustion exited zero")
	}
	raw, err := os.ReadFile(plan)
	if err != nil {
		t.Fatal("missing diagnostic report", err)
	}
	var rep pcbRouteReport
	_ = json.Unmarshal(raw, &rep)
	if rep.Status != "incomplete" || rep.Search == nil || rep.Search.Reason != "state-budget" || rep.Candidate != nil {
		t.Fatalf("%+v", rep)
	}
	requireRouteSVG(t, filepath.Join(dir, "plan.svg"), "INCOMPLETE", "state-budget", "bounded search envelope")
	alias := filepath.Join(dir, "alias.json")
	if err := os.Link(board, alias); err != nil {
		t.Fatal(err)
	}
	for _, out := range []string{board, request, alias} {
		args[len(args)-1] = out
		if code := Run(args, &stdout, &stderr); code == 0 {
			t.Fatal("overwrote input", out)
		}
	}
	args[len(args)-1] = filepath.Join(dir, "bad.svg")
	if code := Run(args, &stdout, &stderr); code == 0 {
		t.Fatal("accepted SVG as JSON report path")
	}
	if got, _ := os.ReadFile(board); !bytes.Equal(got, braw) {
		t.Fatal("input changed")
	}
	for _, bad := range []string{`{"schemaVersion":1,"units":"mil","maxVia":1}`, `{} {}`} {
		if err := decodePCBRouteJSON([]byte(bad), &pcbRouteRequest{}, true); err == nil {
			t.Fatal("unknown/trailing request accepted")
		}
	}
}

func TestPCBRouteRejectsCurvedOrUnknownAreaSemantics(t *testing.T) {
	poly := []any{140.0, 70.0, "L", 160.0, 70.0, 160.0, 90.0, 140.0, 90.0, 140.0, 70.0}
	for _, mutate := range []func(map[string]any){
		func(m map[string]any) { m["fill"] = false }, func(m map[string]any) { delete(m, "lineWidth") },
		func(m map[string]any) {
			m["source"] = []any{140.0, 70.0, "ARC", 90.0, 160.0, 90.0, "L", 140.0, 90.0, 140.0, 70.0}
		},
	} {
		in, s := routeSolverFixture()
		fill := map[string]any{"id": "f", "source": poly, "fill": true, "lineWidth": 0.0}
		mutate(fill)
		s.Copper.Poured = []any{map[string]any{"primitiveId": "p", "net": "OTHER", "layer": 1.0, "fills": []any{fill}}}
		rep := newPCBRouteReport(nil, nil)
		if err := solvePCBRoute(context.Background(), in, s, &rep); err == nil || rep.Status != "incomplete" {
			t.Fatalf("unknown area accepted %+v %v", rep, err)
		}
	}
}

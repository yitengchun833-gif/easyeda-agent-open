package app

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
)

// This requirement is deliberately independent of layout.json: shrinking the
// input's group must not silently shrink what the scenario claims to verify.
var establishedGroupMembers = []string{"C1", "C2", "D1", "R1", "U1"}

func establishedGroupMembership(refs []string) error {
	got := slices.Clone(refs)
	slices.Sort(got)
	if !slices.Equal(got, establishedGroupMembers) {
		return fmt.Errorf("complete group: got %v want %v", got, establishedGroupMembers)
	}
	return nil
}

func TestPCBEstablishedGroupScenario(t *testing.T) {
	fixture := filepath.Join("..", "..", ".agents", "skills", "easyeda-agent", "references", "examples", "pcb-group-move")
	boardPath, inputPath := filepath.Join(fixture, "board.json"), filepath.Join(fixture, "layout.json")
	boardRaw, err := os.ReadFile(boardPath)
	if err != nil {
		t.Fatal(err)
	}
	inputRaw, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	var snap boardSnapshot
	if err := json.Unmarshal(boardRaw, &snap); err != nil {
		t.Fatal(err)
	}
	input, err := decodePCBLayoutPlanInput(inputRaw)
	if err != nil {
		t.Fatal(err)
	}
	mod := input.Modules[0]
	var refs []string
	for _, member := range mod.Members {
		refs = append(refs, member.Ref)
	}
	if err := establishedGroupMembership(refs); err != nil {
		t.Fatal(err)
	}
	if mod.AnchorRef != "U1" || mod.Strategy != "rigid" || len(mod.Search.OffsetsMil) != 1 {
		t.Fatal("scenario must retain its anchor, strategy and single requested translation")
	}
	before := map[string]boardComp{}
	for _, c := range snap.Components {
		before[c.Designator] = c
	}
	if len(before) != 7 || !before["J1"].Locked || !before["H1"].Locked || snap.RoutedLines == nil || *snap.RoutedLines != 0 {
		t.Fatal("scenario requires five members, two locked objects and a known empty copper baseline")
	}
	origin := before[mod.AnchorRef]
	if cx, cy := origin.center(); cx == origin.X && cy == origin.Y {
		t.Fatal("scenario lost its asymmetric footprint anchor")
	}

	// The public kernel consumes a host-independent model. Keep the fixture's
	// provenance as fixture; it must never masquerade as measured live data.
	model := pcbmodel.Board{SchemaVersion: 1, Units: "mil",
		Outline: pcbmodel.Outline{Source: "polygon", Points: snap.Outline.Points, BBox: pcbmodel.BBox{MinX: snap.Outline.BBox.MinX, MinY: snap.Outline.BBox.MinY, MaxX: snap.Outline.BBox.MaxX, MaxY: snap.Outline.BBox.MaxY}},
		Stackup: pcbmodel.Stackup{Copper: []pcbmodel.Layer{{ID: 1, Role: "signal"}, {ID: 2, Role: "signal"}}},
		Rules:   pcbmodel.Rules{Source: "fixture", Clearance: 5, TrackTrack: 5, CopperToEdge: 5, TrackWidthMinimum: 4, ViaDiameter: 16, ViaDrill: 8},
	}
	for _, c := range snap.Components {
		mc := pcbmodel.Component{ID: c.ID, Ref: c.Designator, Anchor: pcbmodel.Point{c.X, c.Y}, Rotation: c.Rotation, Side: c.Layer, Locked: c.Locked,
			BBox: pcbmodel.BBox{MinX: c.BBox.MinX, MinY: c.BBox.MinY, MaxX: c.BBox.MaxX, MaxY: c.BBox.MaxY}}
		for _, p := range c.Pads {
			mc.Pads = append(mc.Pads, pcbmodel.Pad{ID: p.ID, Number: p.Number, Net: p.Net, Center: pcbmodel.Point{p.X, p.Y}, Layer: p.Layer, Width: p.W, Height: p.H, Rotation: p.Rotation, Shape: "rect"})
		}
		model.Components = append(model.Components, mc)
	}
	modelBefore, _ := json.Marshal(model)
	offset := mod.Search.OffsetsMil[0]
	publicReq := pcblayout.Request{MinGap: input.MinGapMil, MaxStates: 100, MaxCandidates: 3, FixedRefs: []string{"J1", "H1"},
		Groups: []pcblayout.Group{{ID: mod.ID, AnchorRef: mod.AnchorRef, Refs: refs, Offsets: []pcblayout.Offset{{X: offset.XMil, Y: offset.YMil}}, AllowedRotationDeltas: mod.Search.RotationDeltasDeg}}}
	public, err := pcblayout.Generate(model, publicReq)
	if err != nil || len(public.Candidates) != 2 {
		t.Fatalf("public group candidates: %v %+v", err, public)
	}

	dir := t.TempDir()
	cmd := newPcbLayoutPlanCmd(ioDiscard{}, ioDiscard{})
	cmd.SetArgs([]string{"--board", boardPath, "--from", inputPath, "--module", mod.ID, "--candidates", "3", "--out", dir})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var report pcbLayoutPlanReport
	read := func(path string, dst any) {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, dst); err != nil {
			t.Fatal(err)
		}
	}
	read(filepath.Join(dir, "manifest.json"), &report)
	if len(report.Candidates) != 2 || len(report.Rejected) != 0 {
		t.Fatalf("scenario requires both translation and rotation candidates: %+v", report)
	}
	seen := map[float64]bool{}
	for _, entry := range report.Candidates {
		var candidate pcbLayoutCandidate
		read(filepath.Join(dir, entry.ID+".json"), &candidate)
		placed := map[string]pcbLayoutPlacement{}
		var gotRefs []string
		for _, p := range candidate.Placements {
			placed[p.Ref] = p
			gotRefs = append(gotRefs, p.Ref)
		}
		if err := establishedGroupMembership(gotRefs); err != nil {
			t.Fatal(err)
		}
		delta := math.Mod(placed["U1"].RotationDeg-origin.Rotation+360, 360)
		if (delta != 0 && delta != 90) || seen[delta] {
			t.Fatalf("unexpected or repeated rotation %g", delta)
		}
		seen[delta] = true
		// A quarter-turn oracle, independent of either production transform.
		point := func(x, y float64) (float64, float64) {
			x, y = x-origin.X, y-origin.Y
			if delta == 90 {
				x, y = -y, x
			}
			return origin.X + offset.XMil + x, origin.Y + offset.YMil + y
		}
		close := func(label string, got, want float64) {
			t.Helper()
			if math.Abs(got-want) > 1e-7 {
				t.Fatalf("%s rotation=%g got=%g want=%g", label, delta, got, want)
			}
		}
		var projected *pcbmodel.Board
		for i := range public.Candidates {
			u, _ := public.Candidates[i].Board.Component("U1")
			if u.Rotation == placed["U1"].RotationDeg {
				projected = &public.Candidates[i].Board
			}
		}
		if projected == nil {
			t.Fatal("public kernel is missing a CLI candidate orientation")
		}
		for ref, p := range placed {
			old := before[ref]
			mc, _ := projected.Component(ref)
			x, y := point(old.X, old.Y)
			close(ref+" anchor x", p.XMil, x)
			close(ref+" anchor y", p.YMil, y)
			close(ref+" public x", mc.Anchor[0], x)
			close(ref+" public y", mc.Anchor[1], y)
			if p.PrimitiveID != old.ID || p.Layer != old.Layer || mc.Side != old.Layer || len(p.Pads) != len(old.Pads) || len(mc.Pads) != len(old.Pads) {
				t.Fatalf("identity/side/pads changed for %s", ref)
			}
			close(ref+" rotation", p.RotationDeg, math.Mod(old.Rotation+delta, 360))
			close(ref+" public rotation", mc.Rotation, p.RotationDeg)
			x0, y0 := point(old.BBox.MinX, old.BBox.MinY)
			x1, y1 := point(old.BBox.MaxX, old.BBox.MaxY)
			if p.BBox == nil {
				t.Fatal("missing candidate bbox")
			}
			for label, pair := range map[string][2]float64{
				"minX": {p.BBox.MinX, math.Min(x0, x1)}, "maxX": {p.BBox.MaxX, math.Max(x0, x1)},
				"minY": {p.BBox.MinY, math.Min(y0, y1)}, "maxY": {p.BBox.MaxY, math.Max(y0, y1)},
				"public minX": {mc.BBox.MinX, math.Min(x0, x1)}, "public maxX": {mc.BBox.MaxX, math.Max(x0, x1)},
				"public minY": {mc.BBox.MinY, math.Min(y0, y1)}, "public maxY": {mc.BBox.MaxY, math.Max(y0, y1)},
			} {
				close(ref+" bbox "+label, pair[0], pair[1])
			}
			for i, pad := range p.Pads {
				op, mp := old.Pads[i], mc.Pads[i]
				if pad.ID != op.ID || pad.Number != op.Number || pad.Net != op.Net || pad.Layer != op.Layer || mp.ID != op.ID || mp.Number != op.Number || mp.Net != op.Net || mp.Layer != op.Layer {
					t.Fatalf("pad identity/net changed for %s.%s", ref, op.Number)
				}
				px, py := point(op.X, op.Y)
				close(ref+" pad x", pad.X, px)
				close(ref+" pad y", pad.Y, py)
				close(ref+" public pad x", mp.Center[0], px)
				close(ref+" public pad y", mp.Center[1], py)
			}
		}
		for _, ref := range []string{"J1", "H1"} {
			old, _ := model.Component(ref)
			got, _ := projected.Component(ref)
			if !reflect.DeepEqual(old, got) {
				t.Fatalf("fixed object %s changed", ref)
			}
		}
		var pb playbook
		read(filepath.Join(dir, candidate.ID+".apply.json"), &pb)
		if len(pb.Steps) != 6 || pb.Steps[5].Action != "pcb.save" {
			t.Fatal("expected five component modifications and save only")
		}
		var applied []string
		for _, step := range pb.Steps[:5] {
			if step.Action != "pcb.component.modify" {
				t.Fatalf("unexpected action %s", step.Action)
			}
			id, _ := step.Payload["primitiveId"].(string)
			for _, p := range placed {
				if p.PrimitiveID == id {
					applied = append(applied, p.Ref)
					patch, ok := step.Payload["patch"].(map[string]any)
					if !ok || patch["x"] != p.XMil || patch["y"] != p.YMil || patch["rotation"] != p.RotationDeg {
						t.Fatalf("Apply differs from candidate for %s: %v", p.Ref, patch)
					}
				}
			}
		}
		if err := establishedGroupMembership(applied); err != nil {
			t.Fatal(err)
		}
	}
	modelAfter, _ := json.Marshal(model)
	boardAfter, _ := os.ReadFile(boardPath)
	inputAfter, _ := os.ReadFile(inputPath)
	if !slices.Equal(modelBefore, modelAfter) || !slices.Equal(boardRaw, boardAfter) || !slices.Equal(inputRaw, inputAfter) {
		t.Fatal("original model or input files changed")
	}
	t.Log("LG-01/LG-02: 5 members, 2 fixed objects, 0/90 degree candidates, CLI/public geometry and Apply agree; offline only")
}

func TestPCBEstablishedGroupScenarioRejectsIncompleteMembership(t *testing.T) {
	fixture := filepath.Join("..", "..", ".agents", "skills", "easyeda-agent", "references", "examples", "pcb-group-move")
	boardRaw, err := os.ReadFile(filepath.Join(fixture, "board.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snap boardSnapshot
	if err := json.Unmarshal(boardRaw, &snap); err != nil {
		t.Fatal(err)
	}
	inputRaw, err := os.ReadFile(filepath.Join(fixture, "layout.json"))
	if err != nil {
		t.Fatal(err)
	}
	input, err := decodePCBLayoutPlanInput(inputRaw)
	if err != nil {
		t.Fatal(err)
	}
	mod := &input.Modules[0]
	mod.Members = slices.DeleteFunc(mod.Members, func(m pcbLayoutMemberSpec) bool { return m.Ref == "C2" })
	mod.PinAssignments = slices.DeleteFunc(mod.PinAssignments, func(p pcbLayoutPinAssignment) bool { return p.MemberRef == "C2" })
	report, err := planPCBLayoutModule(input, &snap, mod.ID, 3)
	if err != nil || len(report.Candidates) == 0 {
		t.Fatalf("negative example must remain geometrically feasible: %v", err)
	}
	// Geometry cannot infer omitted business ownership. The independent scenario
	// requirement must catch an apparently valid four-member plan.
	for _, c := range report.Candidates {
		var refs []string
		for _, p := range c.Placements {
			refs = append(refs, p.Ref)
		}
		if err := establishedGroupMembership(refs); err == nil {
			t.Fatalf("scenario accepted a real candidate that omitted C2: %v", refs)
		}
	}
}

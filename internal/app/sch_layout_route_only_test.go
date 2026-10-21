package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSchematicRouteOnlyFixedGeometry(t *testing.T) {
	raw, err := os.ReadFile("testdata/route-only-minimal.json")
	if err != nil {
		t.Fatal(err)
	}
	in, err := decodeSchematicRouteInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(in)
	out, err := PlanSchematicRoutes(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Wires) < 3 || len(out.Flags) != 0 || out.LayoutMode != "route-only" {
		t.Fatalf("unexpected result: %+v", out)
	}
	for i, c := range in.Components {
		if !reflect.DeepEqual(c.Measurement, out.Placements[i]) {
			t.Fatal("moved measured geometry", c.ID)
		}
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if !libPinsShareIsland(&p, p.Placements[0].Pins[0], p.Placements[1].Pins[0]) {
		t.Fatal("direct net not joined")
	}
	out.Placements[0].Pins[0].X = 999
	out.PinStates["obstacle"]["1"] = "unconnected"
	after, _ := json.Marshal(in)
	if !bytes.Equal(before, after) {
		t.Fatal("caller evidence mutated")
	}
	in.NetPolicies["SIGNAL"] = "module_port"
	unrouted, err := PlanSchematicRoutes(in)
	if err != nil || len(unrouted.Wires) != 0 {
		t.Fatal("non-direct net routed", err)
	}
}

func TestSchematicRouteOnlyCLI(t *testing.T) {
	dir := t.TempDir()
	output, report := filepath.Join(dir, "routes.json"), filepath.Join(dir, "report.json")
	cmd := newSchLayoutPlanCmd(&bytes.Buffer{})
	cmd.SetArgs([]string{"--route-only", "--from", "testdata/route-only-minimal.json", "--out", output, "--report", report})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var result SchematicLayoutResult
	raw, err := os.ReadFile(output)
	if err != nil || json.Unmarshal(raw, &result) != nil || len(result.Wires) < 3 {
		t.Fatal("CLI output missing", err)
	}
	var diagnostics schLayoutReport
	raw, err = os.ReadFile(report)
	if err != nil || json.Unmarshal(raw, &diagnostics) != nil || diagnostics.Status != "planned" || diagnostics.LayoutMode != "route-only" || len(diagnostics.Diagnostics) == 0 {
		t.Fatal("diagnostics missing", err)
	}
	raw, _ = os.ReadFile("testdata/route-only-minimal.json")
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"x": -10,`), nil, 1),
		bytes.Replace(raw, []byte(`"net": "SIGNAL"`), []byte(`"net": null`), 1),
		bytes.Replace(raw, []byte(`"schemaVersion": 1`), []byte(`"schemaVersion": 1, "schemaVersion": 1`), 1),
	} {
		if _, err := decodeSchematicRouteInput(bad); err == nil {
			t.Fatal("missing evidence accepted")
		}
	}
	limited := bytes.Replace(raw, []byte(`"maxExpandedNodes": 200000`), []byte(`"maxExpandedNodes": 1`), 1)
	inputPath := filepath.Join(dir, "limited.json")
	if err := os.WriteFile(inputPath, limited, 0600); err != nil {
		t.Fatal(err)
	}
	failedOutput := filepath.Join(dir, "failed-routes.json")
	failed := newSchLayoutPlanCmd(&bytes.Buffer{})
	failed.SetErr(&bytes.Buffer{})
	failed.SetArgs([]string{"--route-only", "--from", inputPath, "--out", failedOutput, "--report", report})
	if err := failed.Execute(); err == nil {
		t.Fatal("exhausted routing budget accepted")
	}
	if _, err := os.Stat(failedOutput); !os.IsNotExist(err) {
		t.Fatal("failed run emitted partial geometry")
	}
	raw, err = os.ReadFile(report)
	if err != nil || json.Unmarshal(raw, &diagnostics) != nil || diagnostics.Status != "failed" || len(diagnostics.Diagnostics) == 0 {
		t.Fatal("failure diagnostics missing", err)
	}
}

func TestSchematicRouteOnlyUsesMeasuredText(t *testing.T) {
	in := SchematicLayoutInput{SchemaVersion: 1, NetPolicies: map[string]string{"N": "direct"}, Components: []SchematicLayoutComponent{
		{ID: "a", Measurement: SchematicPlacement{Designator: "J3", X: 0, Y: 0, BBox: SchematicBox{-10, -10, 10, 10}, Pins: []SchematicPin{{Number: "1", Net: "N", X: 15, Y: 0}}}},
		{ID: "b", Measurement: SchematicPlacement{Designator: "J6", X: 40, Y: 0, BBox: SchematicBox{30, -10, 50, 10}, Pins: []SchematicPin{{Number: "1", Net: "N", X: 25, Y: 0}}}},
	}}
	legacy := powerLayoutPlan{Placements: []SchematicPlacement{in.Components[0].Measurement, in.Components[1].Measurement}}
	if err := validateLibGeometry(&legacy); err == nil {
		t.Fatal("fixture must intersect inferred label reservation")
	}
	out, err := PlanSchematicRoutes(in)
	if err != nil || len(out.Wires) != 1 {
		t.Fatal("inferred label blocked fixed route", err)
	}
	in.Components[0].Measurement.TextBBoxes = []SchematicBox{{20, -5, 40, 5}}
	if _, err := PlanSchematicRoutes(in); err == nil {
		t.Fatal("actual text/body collision accepted")
	}
	in.Components[0].Measurement.TextBBoxes = nil
	in.Components[1].Measurement.BBox.MinX = 5
	if _, err := PlanSchematicRoutes(in); err == nil {
		t.Fatal("body collision accepted")
	}
}

func TestSchematicRouteOnlyValidateWires(t *testing.T) {
	raw, _ := os.ReadFile("testdata/route-only-minimal.json")
	in, err := decodeSchematicRouteInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	planned, err := PlanSchematicRoutes(in)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	packet, _ := json.Marshal(map[string]any{"routes": planned.Wires})
	wirePath, output, report := filepath.Join(dir, "wires.json"), filepath.Join(dir, "checked.json"), filepath.Join(dir, "report.json")
	if err := os.WriteFile(wirePath, packet, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := newSchLayoutPlanCmd(&bytes.Buffer{})
	cmd.SetArgs([]string{"--route-only", "--from", "testdata/route-only-minimal.json", "--validate-wires", wirePath, "--out", output, "--report", report})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var checked SchematicLayoutResult
	raw, _ = os.ReadFile(output)
	if err := json.Unmarshal(raw, &checked); err != nil || checked.LayoutMode != "route-validate" || checked.Routing != nil || !reflect.DeepEqual(planned.Wires, checked.Wires) {
		t.Fatal("validation changed/searched wires", err)
	}
	var diag schLayoutReport
	raw, _ = os.ReadFile(report)
	if err := json.Unmarshal(raw, &diag); err != nil || diag.Status != "validated" || diag.WireSourceSHA256 != sha256Hex(packet) {
		t.Fatal("wire evidence missing", err)
	}
	in.Components[2].Measurement.Pins[0].Net = "OTHER"
	in.Components[2].PinStates = nil
	in.NetPolicies["OTHER"] = "module_port"
	bad := append(clonePowerLayoutWires(planned.Wires), SchematicWire{Net: "OTHER", Points: [][2]float64{{400, 400}, {420, 400}}}, SchematicWire{Net: "SIGNAL", Points: [][2]float64{{410, 400}, {410, 420}}})
	if _, err := ValidateSchematicRoutes(in, bad); err == nil {
		t.Fatal("cross-net T contact accepted")
	}
	bad = append(clonePowerLayoutWires(planned.Wires), SchematicWire{Net: "SIGNAL", Points: [][2]float64{{5, 0}, {195, 0}}})
	if _, err := ValidateSchematicRoutes(in, bad); err == nil {
		t.Fatal("wire through body accepted")
	}
	if _, err := ValidateSchematicRoutes(in, nil); err == nil {
		t.Fatal("missing direct wires accepted")
	}
	for _, value := range []string{`{"routes":[{"net":"N","points":[[0],[10,0]]}]}`, `{"routes":[{"net":"N","points":[[null,0],[10,0]]}]}`} {
		if _, err := decodeSchematicRouteWires([]byte(value)); err == nil {
			t.Fatal("incomplete coordinate accepted")
		}
	}
}

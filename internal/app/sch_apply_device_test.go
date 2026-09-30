package app

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func deviceGuardFixture(t *testing.T) (*schematicStateExpectation, map[string]any, map[string]any) {
	t.Helper()
	e, live := stateGuardFixture(t)
	p := e.Parts["U1"]
	p.Device = &schematicDeviceExpectation{LibraryUUID: "library", UUID: strings.Repeat("a", 32)}
	e.Parts["U1"] = p
	u := live["components"].([]any)[3].(map[string]any)
	u["device"] = map[string]any{"libraryUuid": "library", "uuid": strings.Repeat("a", 32), "name": "={Value}"}
	return e, live, u
}

func TestSchematicDeviceGuardRequiresHydratedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"missing", func(c map[string]any) { delete(c, "device") }},
		{"null", func(c map[string]any) { c["device"] = nil }},
		{"placed-instance-id", func(c map[string]any) { c["device"].(map[string]any)["uuid"] = strings.Repeat("a", 16) }},
		{"missing-library", func(c map[string]any) { delete(c["device"].(map[string]any), "libraryUuid") }},
		{"wrong-device-same-symbol", func(c map[string]any) { c["device"].(map[string]any)["uuid"] = strings.Repeat("b", 32) }},
		{"wrong-library-same-device", func(c map[string]any) { c["device"].(map[string]any)["libraryUuid"] = "another-library" }},
		{"resolver-error", func(c map[string]any) { c["deviceIdentityError"] = "multiple exact device candidates" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, live, part := deviceGuardFixture(t)
			tc.edit(part)
			if err := e.check(live, nil); err == nil || !strings.Contains(err.Error(), "device identity") {
				t.Fatalf("unavailable or wrong identity passed unchanged geometry/net gate: %v", err)
			}
		})
	}
	e, live, _ := deviceGuardFixture(t)
	if err := e.check(live, nil); err != nil {
		t.Fatal(err)
	}
	s := playbookStep{Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: e}
	if err := validateSchematicExpectationStep(&s); err == nil {
		t.Fatal("missing hydration request passed preflight")
	}
	s.Payload["includeDeviceIdentity"] = true
	if err := validateSchematicExpectationStep(&s); err != nil {
		t.Fatal(err)
	}
	var invalid schematicPartExpectation
	if err := json.Unmarshal([]byte(`{"device":null,"pins":{}}`), &invalid); err == nil {
		t.Fatal("explicit null device weakened identity verification")
	}
}

func TestSchListRequestsHydratedDeviceIdentity(t *testing.T) {
	cfg, capture, cleanup := newCapturingDaemon(t)
	defer cleanup()
	var out, errOut bytes.Buffer
	c := newSchCmd(cfg, &out, &errOut)
	c.SetArgs([]string{"list", "--include-device-identity", "--include-pins", "--include-bbox", "--include-wires"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.action != "schematic.components.list" || capture.payload["includeDeviceIdentity"] != true {
		t.Fatalf("snapshot CLI did not request hydrated identity: %s %+v", capture.action, capture.payload)
	}
}

func TestSchematicDeviceMismatchIsMandatoryBeforeMutation(t *testing.T) {
	e, live, part := deviceGuardFixture(t)
	part["device"].(map[string]any)["uuid"] = strings.Repeat("b", 32)
	b, _ := json.Marshal(live)
	cfg, daemon, cleanup := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string { return `{"ok":true,"result":` + string(b) + `}` })
	defer cleanup()
	yes := true
	pb := &playbook{Version: 1, Meta: playbookMeta{Name: "device guard"}, Steps: []playbookStep{
		{Action: "schematic.components.list", Payload: map[string]any{"includePins": true, "includeDeviceIdentity": true}, ExpectSchematic: e, stepPolicy: stepPolicy{ContinueOnError: &yes}},
		{Action: "schematic.wire.create", Payload: map[string]any{"points": [][2]float64{{0, 0}, {20, 0}}}},
	}}
	var out bytes.Buffer
	runner := &applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: filepath.Join(t.TempDir(), "device.jsonl"), toIdx: 1}
	if err := runner.execute(); err == nil {
		t.Fatal("device mismatch did not stop Apply")
	}
	if calls := daemon.snapshot(); len(calls) != 1 || calls[0].Action != "schematic.components.list" {
		t.Fatalf("mutation escaped identity guard: %+v", calls)
	}
}

func TestComposeCannotReuseWrongDeviceWithIdenticalGeometry(t *testing.T) {
	for _, unwired := range []bool{false, true} {
		p, env := composeApplyFixture(t, true)
		if unwired {
			p, env = composeUnwiredApplyFixture(t)
		}
		r := env["result"].(map[string]any)
		part := r["components"].([]any)[1].(map[string]any)
		part["device"].(map[string]any)["uuid"] = strings.Repeat("b", 32)
		if _, err := schCompositionPlaybook(p, composeApplyBytes(t, env), false); err == nil {
			t.Fatal("wrong device reused without explicit rebuild")
		}
		pb, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true)
		if err != nil {
			t.Fatal(err)
		}
		composeStep(t, pb, "reset-target-preserving-sheet")
		_, before := composeStep(t, pb, "verify-source-before-reset")
		_, after := composeStep(t, pb, "verify-physical-pins-before-wiring")
		ref := p.Layout.Placements[0].Designator
		if before.ExpectSchematic.Parts[ref].Device.UUID != strings.Repeat("b", 32) || after.ExpectSchematic.Parts[ref].Device.UUID != p.Connectivity.Components[0].Device.UUID {
			t.Fatal("before must pin observed identity; final must pin canonical identity")
		}
		for _, s := range pb.Steps {
			if s.ExpectSchematic != nil {
				for _, part := range s.ExpectSchematic.Parts {
					if part.Device != nil && s.Payload["includeDeviceIdentity"] != true {
						t.Fatal("generated guard omitted identity hydration")
					}
				}
			}
		}
	}
}

func TestComposeRejectsUnresolvedBeforeDeviceEvenWithReplace(t *testing.T) {
	for _, edit := range []func(map[string]any){
		func(c map[string]any) { delete(c, "device") },
		func(c map[string]any) { c["device"].(map[string]any)["uuid"] = strings.Repeat("a", 16) },
		func(c map[string]any) { c["deviceIdentityError"] = "unresolved" },
	} {
		p, env := composeApplyFixture(t, false)
		edit(env["result"].(map[string]any)["components"].([]any)[1].(map[string]any))
		if _, err := schCompositionPlaybook(p, composeApplyBytes(t, env), true); err == nil || !strings.Contains(err.Error(), "before snapshot") {
			t.Fatalf("unknown device produced a destructive queue: %v", err)
		}
	}
}

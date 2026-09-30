package app

import (
	"bytes"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stateGuardExpected = `{
 "exactParts":true,
 "parts":{
  "U1":{"primitiveId":"u1","x":250,"y":250,"rotation":0,"mirror":false,
   "pins":{"1":{"x":205,"y":260,"net":"GND"},"2":{"x":205,"y":250,"net":"+3V3"},"3":{"x":205,"y":240,"net":"+5V"}}},
  "C1":{"pins":{"1":{"net":"+3V3"},"2":{"net":"GND"}}}
 }
}`

const stateGuardResult = `{"components":[
 {"componentType":"part","designator":"C1","primitiveId":"c1","pins":[{"pinNumber":"2","net":"GND"},{"pinNumber":"1","net":"+3V3"}]},
 {"componentType":"sheet","primitiveId":"sheet"},
 {"componentType":"netflag","primitiveId":"gnd-flag"},
 {"componentType":"part","designator":"U1","primitiveId":"u1","x":250,"y":250,"rotation":0,"mirror":false,"pinsAvailable":true,
  "pins":[{"pinNumber":"3","x":205,"y":240,"net":"+5V"},{"pinNumber":"1","x":205,"y":260,"net":"GND"},{"pinNumber":"2","x":205,"y":250,"net":"+3V3"}]}
]}`

func stateGuardFixture(t *testing.T) (*schematicStateExpectation, map[string]any) {
	t.Helper()
	var expected schematicStateExpectation
	var result map[string]any
	if err := json.Unmarshal([]byte(stateGuardExpected), &expected); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stateGuardResult), &result); err != nil {
		t.Fatal(err)
	}
	return &expected, result
}

func TestSchematicStateExpectationOrderIndependent(t *testing.T) {
	expected, result := stateGuardFixture(t)
	if err := expected.check(result, nil); err != nil {
		t.Fatalf("shuffled refs and pins with sheet/flags should pass: %v", err)
	}
	parts := result["components"].([]any)
	parts[0], parts[3] = parts[3], parts[0]
	if err := expected.check(result, nil); err != nil {
		t.Fatalf("reordered components should also pass: %v", err)
	}
}

func TestSchematicStateExpectationRejectsIncompleteOrChangedState(t *testing.T) {
	for _, tt := range []struct {
		name string
		edit func(map[string]any, map[string]any)
		want string
	}{
		{"wrong-net", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["net"] = "GND" }, "U1.3 net"},
		{"unknown-net", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["net"] = nil }, "net is unknown"},
		{"absent-net", func(_ map[string]any, u map[string]any) { delete(u["pins"].([]any)[0].(map[string]any), "net") }, "net is unknown"},
		{"floating-pin", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["net"] = "" }, "U1.3 net"},
		{"ambiguous-net", func(_ map[string]any, u map[string]any) { u["netAmbiguous"] = true }, "net is unknown or ambiguous"},
		{"missing-pin", func(_ map[string]any, u map[string]any) { u["pins"] = u["pins"].([]any)[1:] }, "missing pin 3"},
		{"wrong-pin-number", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["pinNumber"] = "4" }, "unexpected pin 4"},
		{"unknown-pin-number", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["pinNumber"] = nil }, "unknown pin number"},
		{"duplicate-pin", func(_ map[string]any, u map[string]any) { u["pins"] = append(u["pins"].([]any), u["pins"].([]any)[0]) }, "duplicate pin number"},
		{"unavailable-pins", func(_ map[string]any, u map[string]any) { u["pinsAvailable"] = false }, "pins unavailable"},
		{"null-pins", func(_ map[string]any, u map[string]any) { u["pins"] = nil }, "pins must be an available array"},
		{"extra-part", func(r map[string]any, _ map[string]any) {
			r["components"] = append(r["components"].([]any), map[string]any{"componentType": "part", "designator": "R9"})
		}, "unexpected part R9"},
		{"missing-part", func(r map[string]any, _ map[string]any) { r["components"] = r["components"].([]any)[1:] }, "missing part C1"},
		{"duplicate-part", func(r map[string]any, u map[string]any) { r["components"] = append(r["components"].([]any), u) }, "duplicate part designator U1"},
		{"changed-identity", func(_ map[string]any, u map[string]any) { u["primitiveId"] = "replacement" }, "primitiveId"},
		{"moved-part", func(_ map[string]any, u map[string]any) { u["x"] = 255.0 }, "U1.x"},
		{"moved-pin", func(_ map[string]any, u map[string]any) { u["pins"].([]any)[0].(map[string]any)["y"] = 245.0 }, "U1.3.y"},
		{"rotated-part", func(_ map[string]any, u map[string]any) { u["rotation"] = 90.0 }, "U1.rotation"},
		{"mirrored-part", func(_ map[string]any, u map[string]any) { u["mirror"] = true }, "U1 mirror"},
		{"absent-coordinate", func(_ map[string]any, u map[string]any) { delete(u, "x") }, "U1.x"},
		{"invalid-coordinate", func(_ map[string]any, u map[string]any) { u["x"] = math.NaN() }, "U1.x"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			expected, result := stateGuardFixture(t)
			u := result["components"].([]any)[3].(map[string]any)
			tt.edit(result, u)
			if err := expected.check(result, nil); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("got %v, want rejection containing %q", err, tt.want)
			}
		})
	}
}

func TestSchematicStateExpectationGeometryOnlyAndFloatTolerance(t *testing.T) {
	expected, result := stateGuardFixture(t)
	for _, part := range expected.Parts {
		for number, pin := range part.Pins {
			pin.Net = nil
			part.Pins[number] = pin
		}
	}
	u := result["components"].([]any)[3].(map[string]any)
	u["x"] = 250.0 + 0.5e-6
	u["pins"].([]any)[0].(map[string]any)["net"] = nil
	u["pins"].([]any)[0].(map[string]any)["x"] = 205.0 - 0.5e-6
	if err := expected.check(result, nil); err != nil {
		t.Fatalf("geometry-only preflight must tolerate small float drift and omit net validation: %v", err)
	}
	u["x"] = 250.0 + 2e-6
	if err := expected.check(result, nil); err == nil {
		t.Fatal("drift exceeding 1e-6 passed")
	}
}

func TestSchematicStateExpectationFormatAndPreflight(t *testing.T) {
	for _, bad := range []string{`{"net":null}`, `{"net":"GND","unknown":42}`} {
		var pin schematicPinExpectation
		if err := json.Unmarshal([]byte(bad), &pin); err == nil {
			t.Fatalf("invalid pin expectation accepted: %s", bad)
		}
	}
	expected, _ := stateGuardFixture(t)
	step := playbookStep{Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: expected}
	if err := validateSchematicExpectationStep(&step); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"schematic.read", "schematic.component.modify"} {
		step.Action = action
		if err := validateSchematicExpectationStep(&step); err == nil {
			t.Fatalf("guard accepted action %s", action)
		}
	}
	step.Action = "schematic.components.list"
	step.Payload["includePins"] = false
	if err := validateSchematicExpectationStep(&step); err == nil {
		t.Fatal("guard accepted includePins:false")
	}
}

func TestSchematicDesignatorOnlyExpectationNeedsNoSlowFields(t *testing.T) {
	_, result := stateGuardFixture(t)
	expected := &schematicStateExpectation{
		DesignatorsOnly: true,
		Parts: map[string]schematicPartExpectation{
			"U1": {PrimitiveID: "u1"},
			"C1": {PrimitiveID: "c1"},
		},
		AbsentParts: []string{"C9"},
	}
	step := playbookStep{
		Action:          "schematic.components.list",
		Payload:         map[string]any{"allPages": true, "tagPages": true},
		ExpectSchematic: expected,
	}
	if err := validateSchematicExpectationStep(&step); err != nil {
		t.Fatalf("minimal project designator guard rejected: %v", err)
	}
	if err := expected.check(result, nil); err != nil {
		t.Fatalf("minimal project designator guard rejected matching inventory: %v", err)
	}

	components := result["components"].([]any)
	result["components"] = append(components, map[string]any{
		"componentType": "part",
		"designator":    "U1",
		"primitiveId":   "other-page-u1",
	})
	if err := expected.check(result, nil); err == nil || !strings.Contains(err.Error(), "duplicate part designator U1") {
		t.Fatalf("cross-page duplicate passed minimal guard: %v", err)
	}
	result["components"] = components

	bad := *expected
	part := bad.Parts["U1"]
	part.Pins = map[string]schematicPinExpectation{}
	bad.Parts = map[string]schematicPartExpectation{"U1": part}
	if err := bad.validate(); err == nil || !strings.Contains(err.Error(), "may only check primitiveId") {
		t.Fatalf("designator-only guard accepted unrelated state: %v", err)
	}
}

func TestSchematicStateExpectationVariables(t *testing.T) {
	expected, result := stateGuardFixture(t)
	part := expected.Parts["U1"]
	part.PrimitiveID = "${U1_PID}"
	net := "${VIN}"
	pin := part.Pins["3"]
	pin.Net = &net
	part.Pins["3"] = pin
	expected.Parts["U1"] = part
	step := playbookStep{ExpectSchematic: expected}
	if missing := unresolvedVars(&step, map[string]bool{"U1_PID": true}); len(missing) != 1 || missing[0] != "VIN" {
		t.Fatalf("guard variable not preflighted: %v", missing)
	}
	if err := expected.check(result, map[string]string{"U1_PID": "u1", "VIN": "+5V"}); err != nil {
		t.Fatalf("captured IDs and named nets must substitute: %v", err)
	}
}

func TestSchematicStateExpectationCannotRetryVerifyOrContinue(t *testing.T) {
	for _, failedRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "mismatch", true: "read-failure"}[failedRead], func(t *testing.T) {
			cfg, daemon, closeDaemon := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				if call.Action == "schematic.components.list" {
					if failedRead {
						return `{"ok":false,"error":{"message":"netlist unavailable"}}`
					}
					return `{"ok":true,"result":` + strings.Replace(stateGuardResult, `"net":"+5V"`, `"net":"GND"`, 1) + `}`
				}
				return `{"ok":true,"result":{"verified":true}}`
			})
			defer closeDaemon()
			expected, _ := stateGuardFixture(t)
			retry, cont := 3, true
			pb := &playbook{Version: 1, Meta: playbookMeta{Name: "mandatory-state-gate"}, Defaults: stepPolicy{Retry: &retry, ContinueOnError: &cont}, Steps: []playbookStep{
				{ID: "golden", Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: expected,
					OnFail: "continue", Verify: &verifyBlock{Action: "system.notify", Payload: map[string]any{"message": "unsafe bypass"}, Assert: map[string]string{"$.verified": "true"}}},
				{ID: "must-not-run", Action: "schematic.save"},
			}}
			var output bytes.Buffer
			journal := filepath.Join(t.TempDir(), "guard.journal.jsonl")
			r := &applyRunner{cfg: cfg, stdout: &output, stderr: &output, pb: pb, pbPath: "guard.json", vars: map[string]string{}, window: "w1", yes: true, journalPath: journal, toIdx: 1}
			if err := r.execute(); err == nil || !strings.Contains(err.Error(), "expectSchematic") {
				t.Fatalf("guard must terminate playbook, got %v; output=%s", err, output.String())
			}
			calls := daemon.snapshot()
			if len(calls) != 1 || calls[0].Action != "schematic.components.list" {
				t.Fatalf("retry, verify or following step bypassed guard: %+v", calls)
			}
			data, err := os.ReadFile(journal)
			if err != nil || !bytes.Contains(data, []byte(`"status":"fail"`)) || bytes.Contains(data, []byte(`"status":"ok"`)) {
				t.Fatalf("failed guard not journaled correctly: %s (%v)", data, err)
			}
		})
	}
}

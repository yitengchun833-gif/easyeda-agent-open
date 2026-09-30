package app

import (
	"bytes"
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

const stateNCExpected = `{"exactParts":true,"parts":{"U1":{"pins":{"1":{"net":"GND","noConnected":false},"2":{"net":"","noConnected":true}}}}}`
const stateNCResult = `{"components":[{"componentType":"sheet"},{"componentType":"part","designator":"U1","pinsAvailable":true,"pins":[{"pinNumber":"1","net":"GND","noConnected":false},{"pinNumber":"2","net":"","noConnected":true}]}]}`

func stateNCFixture(t *testing.T) (*schematicStateExpectation, map[string]any) {
	t.Helper()
	var e schematicStateExpectation
	var r map[string]any
	if err := json.Unmarshal([]byte(stateNCExpected), &e); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(stateNCResult), &r); err != nil {
		t.Fatal(err)
	}
	return &e, r
}

func TestSchematicNCRequiresExplicitStoredStateAndKnownEmptyNet(t *testing.T) {
	e, r := stateNCFixture(t)
	if err := e.check(r, nil); err != nil {
		t.Fatalf("known NC and connected pin rejected: %v", err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"NC-disappeared", func(p map[string]any) { p["noConnected"] = false }},
		{"NC-unknown", func(p map[string]any) { p["noConnected"] = nil }},
		{"NC-missing", func(p map[string]any) { delete(p, "noConnected") }},
		{"NC-wrong-type", func(p map[string]any) { p["noConnected"] = "true" }},
		{"NC-now-wired", func(p map[string]any) { p["net"] = "GND" }},
		{"NC-net-unknown", func(p map[string]any) { p["net"] = nil }},
		{"NC-net-missing", func(p map[string]any) { delete(p, "net") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := stateNCFixture(t)
			pin := r["components"].([]any)[1].(map[string]any)["pins"].([]any)[1].(map[string]any)
			tc.edit(pin)
			if err := e.check(r, nil); err == nil {
				t.Fatal("NC state loss or missing evidence passed")
			}
		})
	}
	e, r = stateNCFixture(t)
	r["components"].([]any)[1].(map[string]any)["pins"].([]any)[0].(map[string]any)["noConnected"] = true
	if err := e.check(r, nil); err == nil {
		t.Fatal("unexpected NC on a connected pin passed")
	}
}

func TestSchematicNCExpectationRejectsNullAndContradiction(t *testing.T) {
	for _, raw := range []string{`{"noConnected":null}`, `{"net":null,"noConnected":true}`} {
		var p schematicPinExpectation
		if err := json.Unmarshal([]byte(raw), &p); err == nil {
			t.Fatalf("explicit unknown must not disable a gate: %s", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"noConnected":false}`, `{"noConnected":true}`} {
		var p schematicPinExpectation
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Fatalf("omission or boolean NC should decode: %s: %v", raw, err)
		}
	}
	e, _ := stateNCFixture(t)
	p := e.Parts["U1"].Pins["1"]
	yes := true
	p.NC = &yes
	e.Parts["U1"].Pins["1"] = p
	if err := e.validate(); err == nil || !strings.Contains(err.Error(), "both a net") {
		t.Fatalf("wired+NC expectation was accepted: %v", err)
	}
}

func TestSchematicEmptyExactPartsCannotHideNewParts(t *testing.T) {
	e := &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}}
	for _, raw := range []string{`{"components":[]}`, `{"components":[{"componentType":"sheet","primitiveId":"sheet"}]}`} {
		var r map[string]any
		_ = json.Unmarshal([]byte(raw), &r)
		if err := e.check(r, nil); err != nil {
			t.Fatalf("known empty part inventory rejected: %s %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `{"components":null}`, `{"components":[{"componentType":"part","designator":"R9"}]}`, `{"components":[{"primitiveId":"unknown"}]}`} {
		var r map[string]any
		_ = json.Unmarshal([]byte(raw), &r)
		if err := e.check(r, nil); err == nil {
			t.Fatalf("unproven empty inventory accepted: %s", raw)
		}
	}
	e.Parts = nil
	if err := e.validate(); err == nil {
		t.Fatal("nil part inventory must not mean zero parts")
	}
	e.Parts = map[string]schematicPartExpectation{}
	e.ExactParts = false
	if err := e.validate(); err == nil {
		t.Fatal("empty non-exact expectation is a vacuous gate")
	}
}

func stateBBoxFixture(t *testing.T) (*schematicStateExpectation, map[string]any, map[string]any) {
	t.Helper()
	e, r := stateNCFixture(t)
	part := e.Parts["U1"]
	part.BBox = &layoutBBox{MinX: 210, MinY: 230, MaxX: 280, MaxY: 270}
	e.Parts["U1"] = part
	actual := r["components"].([]any)[1].(map[string]any)
	actual["bbox"] = map[string]any{"minX": 210.0, "minY": 230.0, "maxX": 280.0, "maxY": 270.0}
	return e, r, actual
}

func TestSchematicBBoxRejectsChangedSymbolSizeWithoutPinMovement(t *testing.T) {
	e, r, actual := stateBBoxFixture(t)
	if err := e.check(r, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"wider-body", func(p map[string]any) { p["bbox"].(map[string]any)["maxX"] = 300.0 }},
		{"taller-body", func(p map[string]any) { p["bbox"].(map[string]any)["maxY"] = 290.0 }},
		{"missing", func(p map[string]any) { delete(p, "bbox") }},
		{"null", func(p map[string]any) { p["bbox"] = nil }},
		{"partial", func(p map[string]any) { delete(p["bbox"].(map[string]any), "minX") }},
		{"invalid", func(p map[string]any) { p["bbox"].(map[string]any)["maxX"] = math.NaN() }},
		{"infinite", func(p map[string]any) { p["bbox"].(map[string]any)["minY"] = math.Inf(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r, actual := stateBBoxFixture(t)
			tc.edit(actual)
			if err := e.check(r, nil); err == nil || !strings.Contains(err.Error(), "bbox") {
				t.Fatalf("changed or unproven symbol bbox accepted: %v", err)
			}
		})
	}
	actual["bbox"].(map[string]any)["maxX"] = 280.0 + 0.5e-6
	if err := e.check(r, nil); err != nil {
		t.Fatalf("API float roundoff should pass: %v", err)
	}
	actual["bbox"].(map[string]any)["maxX"] = 280.0 + 2e-6
	if err := e.check(r, nil); err == nil {
		t.Fatal("bbox drift beyond 1e-6 passed")
	}
}

func TestSchematicBBoxExpectationRequiresValidGeometryAndReadFlag(t *testing.T) {
	e, _, _ := stateBBoxFixture(t)
	s := playbookStep{Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: e}
	if err := validateSchematicExpectationStep(&s); err == nil {
		t.Fatal("bbox gate without includeBBox passed preflight")
	}
	s.Payload["includeBBox"] = true
	if err := validateSchematicExpectationStep(&s); err != nil {
		t.Fatal(err)
	}
	for _, b := range []layoutBBox{{}, {MinX: 10, MaxX: 0, MinY: 0, MaxY: 20}, {MinX: 0, MaxX: 10, MinY: 0, MaxY: math.Inf(1)}} {
		p := e.Parts["U1"]
		p.BBox = &b
		e.Parts["U1"] = p
		if err := e.validate(); err == nil {
			t.Fatalf("invalid expected bbox passed: %+v", b)
		}
	}
}

func TestSchematicNCAndBBoxFailuresStopBeforeWiring(t *testing.T) {
	for _, failure := range []string{"NC", "bbox"} {
		t.Run(failure, func(t *testing.T) {
			e, result, actual := stateBBoxFixture(t)
			if failure == "NC" {
				actual["pins"].([]any)[1].(map[string]any)["noConnected"] = false
			} else {
				actual["bbox"].(map[string]any)["maxX"] = 310.0
			}
			b, _ := json.Marshal(result)
			cfg, daemon, closeDaemon := newBlockApplyTestDaemon(t, func(call blockApplyTestCall) string {
				return `{"ok":true,"result":` + string(b) + `}`
			})
			defer closeDaemon()
			pb := &playbook{Version: 1, Meta: playbookMeta{Name: "NC and shape gate"}, Steps: []playbookStep{
				{Action: "schematic.components.list", Payload: map[string]any{"includePins": true, "includeBBox": true}, ExpectSchematic: e},
				{Action: "schematic.wire.create", Payload: map[string]any{"points": [][2]float64{{0, 0}, {20, 0}}}},
			}}
			var out bytes.Buffer
			runner := &applyRunner{cfg: cfg, stdout: &out, stderr: &out, pb: pb, vars: map[string]string{}, window: "w1", yes: true, journalPath: filepath.Join(t.TempDir(), "journal.jsonl"), toIdx: 1}
			if err := runner.execute(); err == nil {
				t.Fatal("failed gate did not stop Apply")
			}
			calls := daemon.snapshot()
			if len(calls) != 1 || calls[0].Action != "schematic.components.list" {
				t.Fatalf("wire created after failed %s gate: %+v", failure, calls)
			}
		})
	}
}

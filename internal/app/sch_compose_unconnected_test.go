package app

import (
	"testing"
)

func TestComposePreservesExplicitUnconnectedWithoutNCWrite(t *testing.T) {
	source := composeFixture(2)
	source.Connectivity.Components[0].Pins[2].NoConnected = false
	source.Connectivity.Components[0].Pins[2].ConnectionState = "unconnected"
	plan, err := planSchComposition(source)
	if err != nil {
		t.Fatal(err)
	}
	_, live := composeApplyFixture(t, false)
	pb, err := schCompositionPlaybook(plan, composeApplyBytes(t, live), true)
	if err != nil {
		t.Fatal(err)
	}
	ref := source.Connectivity.Components[0].Ref
	for _, step := range pb.Steps {
		if step.Action == "schematic.pin.set_no_connect" {
			payload := step.Payload
			if payload["designator"] == ref {
				t.Fatal("open pin was changed to NC")
			}
		}
	}
	want := schCompositionExpectation(plan, true).Parts[ref].Pins["3"]
	if want.Net == nil || *want.Net != "" || want.NC == nil || *want.NC {
		t.Fatal("readback must prove empty net AND NC false")
	}
	source.Connectivity.Components[0].Pins[2].ConnectionState = ""
	if _, err := planSchComposition(source); err == nil {
		t.Fatal("unknown pin silently became open")
	}
}

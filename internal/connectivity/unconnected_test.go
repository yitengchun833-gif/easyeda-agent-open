package connectivity

import (
	"encoding/json"
	"testing"
)

func TestSnapshotUnconnectedRequiresExplicitEvidence(t *testing.T) {
	for _, tc := range []struct {
		pin  string
		want string
	}{
		{`{"number":"1","net":"","noConnected":false}`, "unconnected"},
		{`{"number":"1","net":"","noConnected":true}`, ""},
		{`{"number":"1","net":""}`, ""},
		{`{"number":"1","net":null,"noConnected":false}`, ""},
		{`{"number":"1","net":"GND","noConnected":false}`, ""},
	} {
		var raw map[string]any
		if err := json.Unmarshal([]byte(`{"components":[{"componentType":"part","designator":"U1","pins":[`+tc.pin+`]}]}`), &raw); err != nil {
			t.Fatal(err)
		}
		d, err := FromRead(raw)
		if err != nil {
			t.Fatal(err)
		}
		if d.Components[0].Pins[0].ConnectionState != tc.want {
			t.Fatalf("%s: inferred unavailable evidence", tc.pin)
		}
		if tc.want != "" && (len(d.Issues) == 0 || d.Issues[0].Code != "unconnected-pin") {
			t.Fatal("known open pin must retain electrical warning")
		}
	}
}

func TestExplicitUnconnectedDesignRoundTripAndDiff(t *testing.T) {
	d := designFixture()
	d.Components[0].Pins[2].NoConnected = false
	d.Components[0].Pins[2].ConnectionState = "unconnected"
	decoded, err := DecodeDesign(designRaw(t, d))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.Components[0].Pins[2].NoConnected || decoded.Components[0].Pins[2].ConnectionState != "unconnected" {
		t.Fatal("open pin became NC")
	}
	diff, err := CompareDesign(designFixture(), d)
	if err != nil || diff.Status != "different" {
		t.Fatalf("state change not detected: %+v %v", diff, err)
	}
	if len(Compare(designFixture(), d).ChangedPinState) != 1 {
		t.Fatal("connectivity diff lost state change")
	}
	for _, state := range []string{"", "unknown", "connected"} {
		d.Components[0].Pins[2].ConnectionState = state
		if _, err := DecodeDesign(designRaw(t, d)); err == nil {
			t.Fatalf("accepted missing/unsupported state %q", state)
		}
	}
	for _, nc := range []bool{false, true} {
		d = designFixture()
		d.Components[0].Pins[2].ConnectionState = "unconnected"
		d.Components[0].Pins[2].NoConnected = nc
		if !nc {
			d.Connections = append(d.Connections, Connection{ComponentID: "a", PinNumber: "3", NetID: "n-ground"})
		}
		if _, err := DecodeDesign(designRaw(t, d)); err == nil {
			t.Fatal("conflicting net/NC and open state accepted")
		}
	}
}

func TestUnconnectedWarningsRefreshAfterValidationAndConnection(t *testing.T) {
	d := designFixture()
	d.Components[0].Pins[2].NoConnected = false
	d.Components[0].Pins[2].ConnectionState = "unconnected"
	for i := 0; i < 3; i++ {
		if err := d.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	count := func() int {
		n := 0
		for _, issue := range d.Issues {
			if issue.Code == "unconnected-pin" {
				n++
			}
		}
		return n
	}
	if count() != 1 {
		t.Fatalf("duplicated diagnosis: %+v", d.Issues)
	}
	d.Components[0].Pins[2].ConnectionState = ""
	d.Connections = append(d.Connections, Connection{ComponentID: "a", PinNumber: "3", NetID: "n-ground"})
	if err := d.Validate(); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatalf("stale diagnosis: %+v", d.Issues)
	}
}

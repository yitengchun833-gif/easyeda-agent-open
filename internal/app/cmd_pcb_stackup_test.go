package app

import "testing"

func TestCheckPcbStackupResponse(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		ok             bool
	}{
		{"verified", `{"ok":true,"result":{"verified":true,"partial":false}}`, true},
		{"partial", `{"ok":true,"result":{"verified":false,"partial":true}}`, false},
		{"rejected", `{"ok":true,"result":{"verified":false,"partial":false}}`, false},
		{"bad envelope", `{"ok":false}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := checkPcbStackupResponse([]byte(tc.response)); (got == nil) != tc.ok {
				t.Fatalf("got error %v, want success=%v", got, tc.ok)
			}
		})
	}
}

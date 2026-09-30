package protocol

import (
	"strings"
	"testing"
)

func TestPcbViewFilterGetIsReadOnlyAndDocumentsUnsupportedSetter(t *testing.T) {
	var found *ActionSpec
	for _, action := range AllActions() {
		if action.Name == "pcb.view.filter.get" {
			copy := action
			found = &copy
			break
		}
	}
	if found == nil {
		t.Fatal("pcb.view.filter.get action missing")
	}
	if found.Mutates {
		t.Fatal("pcb.view.filter.get must not arm autosave or mutate design/view state")
	}
	if found.Domain != DomainPcb || !found.NeedsWindow {
		t.Fatalf("unexpected action contract: %+v", found)
	}
	text := strings.Join(append([]string{found.Description}, found.Outputs...), " ")
	for _, want := range []string{"getCurrentFilterConfiguration", "no matching setter", "pcb_PrimitiveAttribute.modify", "GUI clicks", "writable=false"} {
		if !strings.Contains(text, want) {
			t.Errorf("view-filter contract missing %q: %s", want, text)
		}
	}
}

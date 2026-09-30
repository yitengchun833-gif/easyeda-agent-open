package app

import (
	"bytes"
	"testing"
)

// Removing Notes must remove its public mutation surface while leaving ordinary
// text inspection available for existing documents and module-title validation.
func TestSchematicNotesCommandRetired(t *testing.T) {
	var out, errOut bytes.Buffer
	cmd := newSchCmd(&appConfig{}, &out, &errOut)
	foundTextList := false
	for _, child := range cmd.Commands() {
		if child.Name() == "note" {
			t.Fatal("standalone sch note must not be exposed in 1.4")
		}
		if child.Name() == "text-list" {
			foundTextList = true
		}
	}
	if !foundTextList {
		t.Fatal("text-list must remain available for existing annotations and titles")
	}
}

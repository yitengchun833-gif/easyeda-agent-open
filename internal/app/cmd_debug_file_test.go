package app

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDebugExecFileValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "empty.js")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"exec", "--file", file, "--code", "return 1"}, "choose --code or --file"},
		{[]string{"exec", "--file", file}, "nonempty --file"},
		{[]string{"exec", "--file", filepath.Join(dir, "missing.js")}, "missing.js"},
	} {
		cmd := newDebugCmd(&appConfig{}, io.Discard, io.Discard)
		cmd.SetArgs(tc.args)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v: got %v", tc.args, err)
		}
	}
}

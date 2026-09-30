package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Read file JSON without passing its quotes through the shell. PowerShell 5.1
// Set-Content -Encoding UTF8 emits a BOM, which encoding/json does not accept.
func readModifyPatchSource(cmd *cobra.Command, inline, path string) (string, error) {
	source := "--patch"
	if cmd.Flags().Changed("patch-file") {
		if cmd.Flags().Changed("patch") {
			return "", fmt.Errorf("--patch and --patch-file are mutually exclusive")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read --patch-file: %w", err)
		}
		inline = string(bytes.TrimPrefix(data, []byte{0xef, 0xbb, 0xbf}))
		source = "--patch-file"
	} else if !cmd.Flags().Changed("patch") {
		return "", nil
	}
	var patch map[string]any
	if err := json.Unmarshal([]byte(inline), &patch); err != nil {
		return "", fmt.Errorf("invalid %s JSON (expected object): %w", source, err)
	}
	if patch == nil {
		return "", fmt.Errorf("invalid %s JSON: expected object, got null", source)
	}
	return inline, nil
}

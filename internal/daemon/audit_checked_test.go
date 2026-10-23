package daemon

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAuditCheckedReportsMirrorFailures(t *testing.T) {
	t.Run("invalid JSON does not append a partial record", func(t *testing.T) {
		writer := newAuditWriter(t.TempDir())
		entry := auditEntry{Timestamp: time.Unix(1, 0), RequestID: "invalid", Result: map[string]any{"value": math.NaN()}}
		if err := writer.AppendChecked(entry); err == nil {
			t.Fatal("invalid audit JSON was reported as persisted")
		}
		if _, err := os.Stat(writer.Path(entry.Timestamp)); !os.IsNotExist(err) {
			t.Fatalf("invalid record touched the audit mirror: %v", err)
		}
	})
	t.Run("filesystem failure is visible to the caller", func(t *testing.T) {
		blocked := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(blocked, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
		writer := newAuditWriter(filepath.Join(blocked, "audit"))
		if err := writer.AppendChecked(auditEntry{Timestamp: time.Unix(1, 0), RequestID: "blocked"}); err == nil {
			t.Fatal("audit mirror failure was swallowed")
		}
		data, err := os.ReadFile(blocked)
		if err != nil || string(data) != "preserve" {
			t.Fatalf("failed mirror append changed its obstruction: %v", err)
		}
	})
}

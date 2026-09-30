package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizePcbSnapshotFitMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", "board", false},
		{" BOARD ", "board", false},
		{"all", "all", false},
		{"none", "none", false},
		{"selected", "", true},
	} {
		got, err := normalizePcbSnapshotFitMode(tc.in)
		if (err != nil) != tc.wantErr {
			t.Fatalf("normalizePcbSnapshotFitMode(%q) error=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
		if got != tc.want {
			t.Fatalf("normalizePcbSnapshotFitMode(%q)=%q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestResolveStageSnapshotSHAFallsBackToPersistedArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.png")
	raw := []byte("same bytes the reviewer sees")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got, source, err := resolveStageSnapshotSHA("", path)
	if err != nil {
		t.Fatal(err)
	}
	if got != sha256Hex(raw) || source != "cli-artifact" {
		t.Fatalf("unexpected fallback hash: got=%q source=%q", got, source)
	}
}

func TestResolveStageSnapshotSHAPrefersConnectorEvidence(t *testing.T) {
	got, source, err := resolveStageSnapshotSHA("  connector-sha  ", "/does/not/exist")
	if err != nil {
		t.Fatal(err)
	}
	if got != "connector-sha" || source != "connector" {
		t.Fatalf("unexpected connector hash: got=%q source=%q", got, source)
	}
}

func TestResolveStageSnapshotSHAReportsUnavailableWithoutArtifact(t *testing.T) {
	got, source, err := resolveStageSnapshotSHA("", "")
	if err != nil || got != "" || source != "unavailable" {
		t.Fatalf("unexpected unavailable result: got=%q source=%q err=%v", got, source, err)
	}
}

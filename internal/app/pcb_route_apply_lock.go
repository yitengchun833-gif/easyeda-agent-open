package app

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// Kernel ownership ends on close/process exit; an inert old file is never a lease.
func acquirePCBRouteApplyLock(project, document string) (func(), error) {
	dir := filepath.Join(os.TempDir(), "easyeda-agent-route-apply-locks")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key := sha256.Sum256([]byte(project + "\x00" + document))
	f, err := os.OpenFile(filepath.Join(dir, fmt.Sprintf("%x.lock", key)), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := tryPCBRouteApplyFileLock(f); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("cannot exclusively apply a route to this project/document (another apply may be active): %w", err)
	}
	return func() { _ = f.Close() }, nil
}

//go:build !windows

package app

import (
	"os"
	"syscall"
)

func tryPCBRouteApplyFileLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

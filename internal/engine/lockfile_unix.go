//go:build !windows

package engine

import (
	"os"
	"syscall"
)

// tryFlock attempts a non-blocking exclusive flock.
func tryFlock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}

// unlockFile releases a flock held on f.
func unlockFile(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
}

// isProcessAlive returns true if a process with the given PID exists and is
// reachable via signal 0 — the standard Unix liveness probe.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

//go:build windows

package engine

import (
	"os"

	"golang.org/x/sys/windows"
)

// tryFlock attempts a non-blocking exclusive lock on f using LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY. The lock covers the
// entire file (offset 0, length 0xFFFFFFFFFFFFFFFF).
func tryFlock(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	const flags = windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY
	return windows.LockFileEx(handle, flags, 0, 0xFFFFFFFF, 0xFFFFFFFF, &ol)
}

// unlockFile releases the lock taken by tryFlock.
func unlockFile(f *os.File) error {
	handle := windows.Handle(f.Fd())
	var ol windows.Overlapped
	return windows.UnlockFileEx(handle, 0, 0xFFFFFFFF, 0xFFFFFFFF, &ol)
}

// isProcessAlive returns true when OpenProcess succeeds for pid. Windows has
// no Unix-style signal 0 probe; instead we open a query handle and close it
// immediately. ERROR_INVALID_PARAMETER (the typical "no such PID" result) and
// ERROR_ACCESS_DENIED (process exists but we lack rights) are both treated
// honestly: the latter still indicates a live process, so we report alive.
func isProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err == nil {
		_ = windows.CloseHandle(h)
		return true
	}
	if err == windows.ERROR_ACCESS_DENIED {
		return true
	}
	return false
}

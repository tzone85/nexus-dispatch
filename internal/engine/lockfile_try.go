package engine

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// ErrLockHeld is returned (wrapped) when the pipeline lock is held by a live
// process. Callers that can proceed read-only should check for it with
// errors.Is and degrade instead of failing.
var ErrLockHeld = errors.New("pipeline lock held by a running process")

// LockHeldError carries the holder's details so callers can print them.
type LockHeldError struct {
	PID       int
	StartedAt time.Time
	Path      string
}

func (e *LockHeldError) Error() string {
	return fmt.Sprintf("%v (pid %d, started %s, lock file %s)",
		ErrLockHeld, e.PID, e.StartedAt.Format(time.RFC3339), e.Path)
}

// Unwrap lets errors.Is(err, ErrLockHeld) match.
func (e *LockHeldError) Unwrap() error { return ErrLockHeld }

// TryAcquireLock is AcquireLock for callers that must not block or fail when
// a pipeline is running: it returns the lock on success, or a *LockHeldError
// (errors.Is(err, ErrLockHeld)) when a live process holds it. Any other
// error is a genuine I/O failure. Stale locks from dead PIDs are reclaimed
// exactly as in AcquireLock.
func TryAcquireLock(stateDir string) (*PipelineLock, error) {
	lockPath := filepath.Join(stateDir, "nxd.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}
	if err := tryFlock(f); err == nil {
		return finaliseLock(f, lockPath)
	}
	f.Close()

	info, readErr := readLockInfo(lockPath)
	if readErr == nil && isProcessAlive(info.PID) {
		return nil, &LockHeldError{PID: info.PID, StartedAt: info.StartedAt, Path: lockPath}
	}
	// Unreadable info or dead holder: fall through to the full acquire path,
	// which clears stale locks and reports unreadable ones.
	return AcquireLock(stateDir)
}

// ForceClearLock removes the lock file only when it is safe: the recorded
// holder is dead, or the lock info cannot be read at all. It refuses to
// remove a lock held by a live process — `resume --force` used to delete the
// file unconditionally, letting two pipelines run against the same state.
func ForceClearLock(stateDir string) error {
	lockPath := filepath.Join(stateDir, "nxd.lock")
	info, err := readLockInfo(lockPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		// Corrupt or empty lock file: nothing identifies a holder; clear it.
		return removeLock(lockPath)
	case isProcessAlive(info.PID) && info.PID != os.Getpid():
		return fmt.Errorf("refusing to force-clear %s: holder pid %d is still alive (started %s); stop it first",
			lockPath, info.PID, info.StartedAt.Format(time.RFC3339))
	default:
		return removeLock(lockPath)
	}
}

func removeLock(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("removing lock file: %w", err)
	}
	return nil
}

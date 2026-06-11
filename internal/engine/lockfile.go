package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// lockInfo is written as JSON into the lock file so that concurrent
// callers (or humans) can identify the holder.
type lockInfo struct {
	PID       int       `json:"pid"`
	Command   string    `json:"command,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// PipelineLock represents an acquired advisory lock backed by a file.
// Call Release when the protected operation completes.
type PipelineLock struct {
	path string
	file *os.File
}

// AcquireLock attempts to obtain an exclusive, non-blocking advisory
// lock at <stateDir>/nxd.lock.  On success it writes the current
// process metadata into the file and returns a PipelineLock whose
// Release method will undo everything.
//
// If the lock is already held:
//   - The existing lock file is read for its lockInfo.
//   - If the recorded PID is no longer alive the lock is considered
//     stale and is force-acquired.
//   - Otherwise an informative error is returned.
func AcquireLock(stateDir string) (*PipelineLock, error) {
	lockPath := filepath.Join(stateDir, "nxd.lock")

	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	if err := tryFlock(f); err == nil {
		return finaliseLock(f, lockPath)
	}

	// Lock is held — inspect the existing holder.
	info, readErr := readLockInfo(lockPath)
	if readErr != nil {
		f.Close()
		return nil, fmt.Errorf("lock is held and lock info unreadable: %w", readErr)
	}

	if isProcessAlive(info.PID) {
		f.Close()
		return nil, fmt.Errorf(
			"pipeline already running (pid %d, started %s).\n"+
				"  Lock file: %s\n"+
				"  If the prior run died, the lock is auto-cleared on the next attempt; "+
				"otherwise remove it manually with `rm %s`.",
			info.PID,
			info.StartedAt.Format(time.RFC3339),
			lockPath,
			lockPath,
		)
	}

	// Stale lock — the holder is dead.  Remove and retry.
	f.Close()
	if err := os.Remove(lockPath); err != nil {
		return nil, fmt.Errorf("removing stale lock file: %w", err)
	}

	f2, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("opening lock file after stale removal: %w", err)
	}

	if err := tryFlock(f2); err != nil {
		f2.Close()
		return nil, fmt.Errorf("flock after stale removal: %w", err)
	}

	return finaliseLock(f2, lockPath)
}

// Release unlocks, closes, and removes the lock file.
func (pl *PipelineLock) Release() error {
	if pl.file == nil {
		return nil
	}

	var errs []string

	if err := unlockFile(pl.file); err != nil {
		errs = append(errs, fmt.Sprintf("unlock: %v", err))
	}
	if err := pl.file.Close(); err != nil {
		errs = append(errs, fmt.Sprintf("close: %v", err))
	}
	if err := os.Remove(pl.path); err != nil && !os.IsNotExist(err) {
		errs = append(errs, fmt.Sprintf("remove: %v", err))
	}

	pl.file = nil

	if len(errs) > 0 {
		return fmt.Errorf("releasing lock: %s", strings.Join(errs, "; "))
	}
	return nil
}

// --------------- internal helpers ---------------

// tryFlock attempts a non-blocking exclusive lock. Implementation lives in
// lockfile_unix.go / lockfile_windows.go.
//
// unlockFile releases the lock taken by tryFlock — also platform-split.
//
// isProcessAlive returns whether a PID maps to a live process — also platform-split.

// finaliseLock writes the current process info into the already-flocked
// file and returns the PipelineLock.
func finaliseLock(f *os.File, path string) (*PipelineLock, error) {
	info := lockInfo{
		PID:       os.Getpid(),
		Command:   strings.Join(os.Args, " "),
		StartedAt: time.Now().UTC(),
	}

	data, err := json.Marshal(info)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("marshalling lock info: %w", err)
	}

	if err := f.Truncate(0); err != nil {
		f.Close()
		return nil, fmt.Errorf("truncating lock file: %w", err)
	}
	if _, err := f.WriteAt(data, 0); err != nil {
		f.Close()
		return nil, fmt.Errorf("writing lock info: %w", err)
	}

	return &PipelineLock{path: path, file: f}, nil
}

// readLockInfo reads and decodes the JSON lockInfo from the given path.
func readLockInfo(path string) (lockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockInfo{}, err
	}
	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return lockInfo{}, err
	}
	return info, nil
}

// isProcessAlive is implemented per-OS (see lockfile_unix.go / lockfile_windows.go).

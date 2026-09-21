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
		// We hold the exclusive advisory lock. The kernel releases a flock when
		// its holder dies (the fd is closed on process exit, and Go opens this
		// file O_CLOEXEC so no child inherits it), so acquiring it here proves no
		// live pipeline is running: whatever pid a crashed prior run may have
		// left in the file is stale by definition, and finaliseLock overwrites
		// it. A lingering lock file therefore needs no explicit removal — this is
		// the path that reclaims it.
		return finaliseLock(f, lockPath)
	}

	// tryFlock failed: another process holds LOCK_EX *right now*. Because a dead
	// holder's flock auto-releases, a failed acquire always means a live holder,
	// so we must NOT reclaim the lock based on the pid recorded in the file.
	// Doing so (the old unlink-and-reopen path) raced a holder that had flocked
	// the file but not yet rewritten its pid: a second acquirer would read the
	// previous (dead) pid, unlink the still-flocked file, and create a fresh
	// inode it could lock — leaving TWO processes each holding "the lock" and
	// running concurrent pipelines on the same requirement. Report the lock as
	// held instead; a genuinely dead holder is cleared by the fast path above on
	// the next attempt.
	f.Close()

	info, readErr := readLockInfo(lockPath)
	if readErr != nil {
		return nil, fmt.Errorf(
			"pipeline already running, but its lock info is unreadable (a run is "+
				"acquiring the lock). If you are certain no run is active, remove it "+
				"with `rm %s`: %w",
			lockPath, readErr,
		)
	}

	holder := fmt.Sprintf("pid %d", info.PID)
	if !isProcessAlive(info.PID) {
		// The recorded pid is dead yet the flock is held: another process has
		// just acquired the lock and not yet rewritten the pid. It is live.
		holder = fmt.Sprintf("pid %d (a run is acquiring the lock)", info.PID)
	}
	return nil, fmt.Errorf(
		"pipeline already running (%s, started %s).\n"+
			"  Lock file: %s\n"+
			"  If the prior run died, the lock is auto-cleared on the next attempt; "+
			"otherwise remove it manually with `rm %s`",
		holder,
		info.StartedAt.Format(time.RFC3339),
		lockPath,
		lockPath,
	)
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

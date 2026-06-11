package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireLock_Success(t *testing.T) {
	dir := t.TempDir()

	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer lock.Release()

	// Lock file should exist with valid JSON content.
	lockPath := filepath.Join(dir, "nxd.lock")
	data, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("reading lock file: %v", err)
	}

	var info lockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshalling lock info: %v", err)
	}

	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.StartedAt.IsZero() {
		t.Error("StartedAt should not be zero")
	}
}

func TestAcquireLock_BlocksConcurrent(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}
	defer first.Release()

	// Second lock attempt should fail while first is held.
	_, err = AcquireLock(dir)
	if err == nil {
		t.Fatal("expected error from second AcquireLock, got nil")
	}
	// Error must point users at the lock-file location and at the recovery
	// command, so new contributors don't have to grep CLAUDE.md to learn
	// that ~/.nxd/nxd.lock is what's blocking them.
	msg := err.Error()
	if !strings.Contains(msg, "nxd.lock") {
		t.Errorf("error should mention the lock file: %q", msg)
	}
	if !strings.Contains(msg, "rm ") {
		t.Errorf("error should suggest a recovery command: %q", msg)
	}
}

func TestAcquireLock_ReleaseThenReacquire(t *testing.T) {
	dir := t.TempDir()

	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("first AcquireLock failed: %v", err)
	}

	if err := first.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}

	// After release, acquiring a new lock should succeed.
	second, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("second AcquireLock failed: %v", err)
	}
	defer second.Release()
}

func TestAcquireLock_StaleLockDetection(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "nxd.lock")

	// Write a lock file referencing a PID that almost certainly does not exist.
	staleInfo := lockInfo{
		PID:     999999999,
		Command: "ghost-process",
	}
	data, err := json.Marshal(staleInfo)
	if err != nil {
		t.Fatalf("marshalling stale info: %v", err)
	}
	if err := os.WriteFile(lockPath, data, 0o644); err != nil {
		t.Fatalf("writing stale lock file: %v", err)
	}

	// AcquireLock should detect the dead PID and force-acquire.
	lock, err := AcquireLock(dir)
	if err != nil {
		t.Fatalf("AcquireLock with stale lock failed: %v", err)
	}
	defer lock.Release()

	// Verify new lock info was written with our PID.
	freshData, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("reading refreshed lock file: %v", err)
	}
	var info lockInfo
	if err := json.Unmarshal(freshData, &info); err != nil {
		t.Fatalf("unmarshalling refreshed lock info: %v", err)
	}
	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
}

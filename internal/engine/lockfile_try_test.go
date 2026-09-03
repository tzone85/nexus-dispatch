package engine

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLockInfo(t *testing.T, dir string, pid int) string {
	t.Helper()
	p := filepath.Join(dir, "nxd.lock")
	if err := os.WriteFile(p, []byte(`{"pid":`+itoa(pid)+`,"started_at":"2026-01-01T00:00:00Z"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestTryAcquireLock_Success(t *testing.T) {
	dir := t.TempDir()
	lock, err := TryAcquireLock(dir)
	if err != nil {
		t.Fatalf("TryAcquireLock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestTryAcquireLock_HeldByLiveProcess(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	lock, err := TryAcquireLock(dir)
	if lock != nil || err == nil {
		t.Fatal("expected ErrLockHeld while the first lock is held")
	}
	if !errors.Is(err, ErrLockHeld) {
		t.Errorf("errors.Is(ErrLockHeld) should be true, got %v", err)
	}
	var held *LockHeldError
	if !errors.As(err, &held) || held.PID != os.Getpid() {
		t.Errorf("LockHeldError should carry the holder pid, got %v", err)
	}
	if !strings.Contains(err.Error(), "nxd.lock") {
		t.Errorf("error should name the lock file: %v", err)
	}
}

func TestTryAcquireLock_ReclaimsStaleLock(t *testing.T) {
	dir := t.TempDir()
	writeLockInfo(t, dir, 999999) // almost certainly dead
	lock, err := TryAcquireLock(dir)
	if err != nil {
		t.Fatalf("stale lock should be reclaimed: %v", err)
	}
	lock.Release()
}

func TestTryAcquireLock_BadDir(t *testing.T) {
	if _, err := TryAcquireLock(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for missing state dir")
	}
}

func TestForceClearLock(t *testing.T) {
	t.Run("no lock file is a no-op", func(t *testing.T) {
		if err := ForceClearLock(t.TempDir()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("dead holder is cleared", func(t *testing.T) {
		dir := t.TempDir()
		p := writeLockInfo(t, dir, 999999)
		if err := ForceClearLock(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Error("lock file should be removed")
		}
	})
	t.Run("corrupt lock file is cleared", func(t *testing.T) {
		dir := t.TempDir()
		p := filepath.Join(dir, "nxd.lock")
		os.WriteFile(p, []byte("not json"), 0o644)
		if err := ForceClearLock(dir); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Error("corrupt lock file should be removed")
		}
	})
	t.Run("live holder is refused", func(t *testing.T) {
		dir := t.TempDir()
		// Use the parent process (the go test runner), which is alive and is not us.
		p := writeLockInfo(t, dir, os.Getppid())
		err := ForceClearLock(dir)
		if err == nil || !strings.Contains(err.Error(), "still alive") {
			t.Fatalf("expected refusal for live holder, got %v", err)
		}
		if _, statErr := os.Stat(p); statErr != nil {
			t.Error("lock file must be left in place")
		}
	})
	t.Run("own pid is cleared", func(t *testing.T) {
		dir := t.TempDir()
		writeLockInfo(t, dir, os.Getpid())
		if err := ForceClearLock(dir); err != nil {
			t.Fatal(err)
		}
	})
}

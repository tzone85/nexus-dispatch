package update

import (
	"path/filepath"
	"testing"
	"time"
)

func TestWriteAndReadCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "update-status.json")

	result := CheckResult{
		CheckedAt: time.Date(2026, 4, 8, 10, 0, 0, 0, time.UTC),
		Models: []ModelStatus{
			{
				Name:            "gemma4:26b",
				Source:          "ollama",
				LocalDigest:     "abc123",
				RemoteDigest:    "def456",
				UpdateAvailable: true,
				UpdateCommand:   "ollama pull gemma4:26b",
			},
		},
	}

	if err := WriteCache(path, result); err != nil {
		t.Fatalf("WriteCache: %v", err)
	}

	got, err := ReadCache(path)
	if err != nil {
		t.Fatalf("ReadCache: %v", err)
	}

	if got.CheckedAt.IsZero() {
		t.Error("CheckedAt is zero")
	}
	if len(got.Models) != 1 {
		t.Fatalf("expected 1 model, got %d", len(got.Models))
	}
	if got.Models[0].Name != "gemma4:26b" {
		t.Errorf("Name = %q", got.Models[0].Name)
	}
	if !got.Models[0].UpdateAvailable {
		t.Error("expected UpdateAvailable=true")
	}
}

func TestReadCache_MissingFile(t *testing.T) {
	got, err := ReadCache("/nonexistent/path/update-status.json")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if !got.CheckedAt.IsZero() {
		t.Error("expected zero CheckedAt for missing cache")
	}
}

func TestIsStale_Fresh(t *testing.T) {
	result := CheckResult{CheckedAt: time.Now()}
	if IsStale(result, 48) {
		t.Error("expected fresh cache to not be stale")
	}
}

func TestIsStale_Old(t *testing.T) {
	result := CheckResult{CheckedAt: time.Now().Add(-49 * time.Hour)}
	if !IsStale(result, 48) {
		t.Error("expected 49-hour-old cache to be stale")
	}
}

func TestIsStale_ZeroTime(t *testing.T) {
	result := CheckResult{}
	if !IsStale(result, 48) {
		t.Error("expected zero-time cache to be stale")
	}
}

func TestUpdatesAvailable(t *testing.T) {
	result := CheckResult{
		Models: []ModelStatus{
			{Name: "a", UpdateAvailable: false},
			{Name: "b", UpdateAvailable: true},
		},
	}
	updates := UpdatesAvailable(result)
	if len(updates) != 1 {
		t.Fatalf("expected 1 update, got %d", len(updates))
	}
	if updates[0].Name != "b" {
		t.Errorf("Name = %q", updates[0].Name)
	}
}

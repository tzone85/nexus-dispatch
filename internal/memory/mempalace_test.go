package memory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewMemPalace_FindsBridge(t *testing.T) {
	// Create a temporary bridge script so detectBridgePath can find it
	// when probing relative to the working directory.
	tmp := t.TempDir()
	scriptsDir := filepath.Join(tmp, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatalf("create scripts dir: %v", err)
	}
	bridgePath := filepath.Join(scriptsDir, "mempalace_bridge.py")
	if err := os.WriteFile(bridgePath, []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write stub bridge: %v", err)
	}

	// Temporarily change to the temp dir so the CWD probe finds the script.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origDir) })

	mp := NewMemPalace()
	// Bridge file exists, so bridgePath should be set.
	if mp.bridgePath == "" {
		t.Error("expected bridgePath to be set, got empty string")
	}
	// Health check will fail (stub script), so available should be false.
	if mp.available {
		t.Error("expected available=false with stub bridge")
	}
}

func TestMemPalace_IsAvailable(t *testing.T) {
	// A zero-value MemPalace must not panic.
	mp := &MemPalace{}
	if mp.IsAvailable() {
		t.Error("expected zero-value MemPalace to report unavailable")
	}
}

func TestMemPalace_SearchReturnsEmpty_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{available: false}
	results, err := mp.Search("test query", "wing", "room", 5)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestMemPalace_MineNoError_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{available: false}
	if err := mp.Mine("wing", "room", "some text"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestMemPalace_MineMetaNoError_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{available: false}
	if err := mp.MineMeta("some meta text"); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
}

func TestParseBridgeOutput_ValidSearch(t *testing.T) {
	raw := `{
		"status": "ok",
		"results": [
			{"text": "hello world", "wing": "code", "room": "main", "similarity": 0.95},
			{"text": "goodbye world", "wing": "docs", "room": "readme", "similarity": 0.80}
		]
	}`

	results := parseSearchOutput(raw)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	if results[0].Text != "hello world" {
		t.Errorf("results[0].Text = %q, want %q", results[0].Text, "hello world")
	}
	if results[0].Wing != "code" {
		t.Errorf("results[0].Wing = %q, want %q", results[0].Wing, "code")
	}
	if results[0].Room != "main" {
		t.Errorf("results[0].Room = %q, want %q", results[0].Room, "main")
	}
	if results[0].Similarity != 0.95 {
		t.Errorf("results[0].Similarity = %f, want %f", results[0].Similarity, 0.95)
	}

	if results[1].Text != "goodbye world" {
		t.Errorf("results[1].Text = %q, want %q", results[1].Text, "goodbye world")
	}
	if results[1].Similarity != 0.80 {
		t.Errorf("results[1].Similarity = %f, want %f", results[1].Similarity, 0.80)
	}
}

func TestParseBridgeOutput_Error(t *testing.T) {
	raw := `{"status": "error", "message": "palace not found"}`

	results := parseSearchOutput(raw)
	if len(results) != 0 {
		t.Errorf("expected empty results for error status, got %d", len(results))
	}
}

func TestParseBridgeOutput_Empty(t *testing.T) {
	results := parseSearchOutput("")
	if results != nil {
		t.Errorf("expected nil for empty input, got %v", results)
	}
}

func TestParseBridgeOutput_InvalidJSON(t *testing.T) {
	results := parseSearchOutput("not json at all")
	if results != nil {
		t.Errorf("expected nil for invalid JSON, got %v", results)
	}
}

func TestParseBridgeOutput_NoResults(t *testing.T) {
	raw := `{"status": "ok", "results": []}`

	results := parseSearchOutput(raw)
	if len(results) != 0 {
		t.Errorf("expected empty results, got %d", len(results))
	}
}

func TestMemPalace_WakeUpReturnsEmpty_WhenUnavailable(t *testing.T) {
	mp := &MemPalace{available: false}
	msg, err := mp.WakeUp("test_wing")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if msg != "" {
		t.Errorf("expected empty string, got %q", msg)
	}
}

func TestBuildCandidatePaths(t *testing.T) {
	paths := buildCandidatePaths()
	if len(paths) == 0 {
		t.Fatal("expected at least one candidate path")
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			t.Errorf("expected absolute path, got %q", p)
		}
	}
}

func TestFileExists(t *testing.T) {
	tmp := t.TempDir()
	existing := filepath.Join(tmp, "exists.txt")
	if err := os.WriteFile(existing, []byte("hi"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if !fileExists(existing) {
		t.Error("expected fileExists to return true for existing file")
	}
	if fileExists(filepath.Join(tmp, "nope.txt")) {
		t.Error("expected fileExists to return false for missing file")
	}
	if fileExists(tmp) {
		t.Error("expected fileExists to return false for directory")
	}
}

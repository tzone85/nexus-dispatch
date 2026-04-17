package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeBridgeScript writes a Python script to tmp that always prints the given
// JSON response, regardless of arguments. It returns the script path.
func writeBridgeScript(t *testing.T, tmp, response string) string {
	t.Helper()
	script := filepath.Join(tmp, "mempalace_bridge.py")
	content := fmt.Sprintf(`import sys
print(%q)
`, response)
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write bridge script: %v", err)
	}
	return script
}

// newAvailableMP creates a MemPalace that considers itself available by using a
// fake bridge script that responds with a health-ok JSON payload.
func newAvailableMP(t *testing.T, searchResponse string) (*MemPalace, string) {
	t.Helper()
	tmp := t.TempDir()
	bridge := writeBridgeScript(t, tmp, searchResponse)
	mp := &MemPalace{
		bridgePath: bridge,
		available:  true,
	}
	return mp, tmp
}

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

// ---------------------------------------------------------------------------
// NewMemPalaceWithPath
// ---------------------------------------------------------------------------

func TestNewMemPalaceWithPath_SetsFields(t *testing.T) {
	tmp := t.TempDir()
	// Use a non-existent bridge so healthCheck returns false — we just want
	// to verify that the constructor sets the fields correctly.
	bridge := filepath.Join(tmp, "bridge.py")
	palace := filepath.Join(tmp, "palace")

	mp := NewMemPalaceWithPath(bridge, palace)

	if mp.bridgePath != bridge {
		t.Errorf("bridgePath = %q, want %q", mp.bridgePath, bridge)
	}
	if mp.palacePath != palace {
		t.Errorf("palacePath = %q, want %q", mp.palacePath, palace)
	}
	// Bridge does not exist → healthCheck must fail → available = false.
	if mp.available {
		t.Error("expected available=false for non-existent bridge")
	}
}

func TestNewMemPalaceWithPath_HealthyBridge(t *testing.T) {
	tmp := t.TempDir()
	bridge := writeBridgeScript(t, tmp, `{"status":"ok"}`)
	palace := filepath.Join(tmp, "palace")

	mp := NewMemPalaceWithPath(bridge, palace)

	if !mp.available {
		t.Error("expected available=true when bridge returns status ok")
	}
}

// ---------------------------------------------------------------------------
// runBridge: empty bridgePath branch
// ---------------------------------------------------------------------------

func TestRunBridge_EmptyBridgePath_ReturnsError(t *testing.T) {
	mp := &MemPalace{bridgePath: "", available: true}
	_, err := mp.runBridge("health")
	if err == nil {
		t.Fatal("expected error when bridgePath is empty")
	}
}

func TestRunBridge_WithPalacePath_PrependsPalaceFlag(t *testing.T) {
	tmp := t.TempDir()
	// Script echoes its own argv to verify --palace is present.
	script := filepath.Join(tmp, "bridge.py")
	content := `import sys, json
args = sys.argv[1:]
if "--palace" in args:
    print('{"status":"ok","message":"palace flag seen"}')
else:
    print('{"status":"error","message":"no palace flag"}')
`
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	mp := &MemPalace{
		bridgePath: script,
		palacePath: "/some/palace",
		available:  true,
	}
	out, err := mp.runBridge("health")
	if err != nil {
		t.Fatalf("runBridge error: %v", err)
	}
	if out != `{"status":"ok","message":"palace flag seen"}` {
		t.Errorf("unexpected output: %q", out)
	}
}

// ---------------------------------------------------------------------------
// healthCheck: invalid JSON branch
// ---------------------------------------------------------------------------

func TestHealthCheck_InvalidJSON_ReturnsFalse(t *testing.T) {
	tmp := t.TempDir()
	// Bridge prints garbage, not JSON.
	bridge := writeBridgeScript(t, tmp, "NOT JSON")
	mp := &MemPalace{bridgePath: bridge}
	if mp.healthCheck() {
		t.Error("expected healthCheck=false when bridge outputs invalid JSON")
	}
}

func TestHealthCheck_NonOkStatus_ReturnsFalse(t *testing.T) {
	tmp := t.TempDir()
	bridge := writeBridgeScript(t, tmp, `{"status":"error","message":"unhealthy"}`)
	mp := &MemPalace{bridgePath: bridge}
	if mp.healthCheck() {
		t.Error("expected healthCheck=false when bridge status != ok")
	}
}

// ---------------------------------------------------------------------------
// Search: available path
// ---------------------------------------------------------------------------

func TestSearch_ReturnsResults_WhenAvailable(t *testing.T) {
	response := `{"status":"ok","results":[{"text":"alpha","wing":"w1","room":"r1","similarity":0.9}]}`
	mp, _ := newAvailableMP(t, response)

	results, err := mp.Search("alpha", "w1", "r1", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Text != "alpha" {
		t.Errorf("Text = %q, want %q", results[0].Text, "alpha")
	}
}

func TestSearch_NoWingRoom_StillWorks(t *testing.T) {
	response := `{"status":"ok","results":[{"text":"bare","wing":"","room":"","similarity":0.5}]}`
	mp, _ := newAvailableMP(t, response)

	results, err := mp.Search("bare", "", "", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
}

func TestSearch_BridgeFails_ReturnsNilNoError(t *testing.T) {
	// Bridge script exits non-zero to simulate failure.
	tmp := t.TempDir()
	script := filepath.Join(tmp, "bridge.py")
	if err := os.WriteFile(script, []byte("import sys; sys.exit(1)\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	mp := &MemPalace{bridgePath: script, available: true}

	results, err := mp.Search("query", "", "", 5)
	if err != nil {
		t.Fatalf("expected nil error on bridge failure, got %v", err)
	}
	if results != nil {
		t.Errorf("expected nil results on bridge failure, got %v", results)
	}
}

// ---------------------------------------------------------------------------
// Mine: available path
// ---------------------------------------------------------------------------

func TestMine_CallsBridge_WhenAvailable(t *testing.T) {
	response := `{"status":"ok"}`
	mp, _ := newAvailableMP(t, response)

	if err := mp.Mine("wing1", "room1", "some text"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMine_BridgeFails_ReturnsNil(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "bridge.py")
	if err := os.WriteFile(script, []byte("import sys; sys.exit(1)\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	mp := &MemPalace{bridgePath: script, available: true}

	if err := mp.Mine("w", "r", "t"); err != nil {
		t.Fatalf("expected nil error on bridge failure, got %v", err)
	}
}

func TestMineMeta_DelegatesToMine(t *testing.T) {
	response := `{"status":"ok"}`
	mp, _ := newAvailableMP(t, response)

	if err := mp.MineMeta("metadata fragment"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// WakeUp: available path
// ---------------------------------------------------------------------------

func TestWakeUp_ReturnsMessage_WhenAvailable(t *testing.T) {
	response := `{"status":"ok","message":"wing summary here"}`
	mp, _ := newAvailableMP(t, response)

	msg, err := mp.WakeUp("test_wing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "wing summary here" {
		t.Errorf("message = %q, want %q", msg, "wing summary here")
	}
}

func TestWakeUp_InvalidJSON_ReturnsEmptyNoError(t *testing.T) {
	mp, _ := newAvailableMP(t, "GARBAGE")

	msg, err := mp.WakeUp("any_wing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
}

func TestWakeUp_NonOkStatus_ReturnsEmptyNoError(t *testing.T) {
	response := `{"status":"error","message":"wing not found"}`
	mp, _ := newAvailableMP(t, response)

	msg, err := mp.WakeUp("missing_wing")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if msg != "" {
		t.Errorf("expected empty message for error status, got %q", msg)
	}
}

func TestWakeUp_BridgeFails_ReturnsEmptyNoError(t *testing.T) {
	tmp := t.TempDir()
	script := filepath.Join(tmp, "bridge.py")
	if err := os.WriteFile(script, []byte("import sys; sys.exit(1)\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	mp := &MemPalace{bridgePath: script, available: true}

	msg, err := mp.WakeUp("any_wing")
	if err != nil {
		t.Fatalf("expected nil error on bridge failure, got %v", err)
	}
	if msg != "" {
		t.Errorf("expected empty message, got %q", msg)
	}
}

package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ─── doctor.go helpers ───────────────────────────────────────────────────────

func TestResolveStateDir_FromConfig(t *testing.T) {
	cfg := config.Config{}
	cfg.Workspace.StateDir = "/tmp/custom-state"
	got := resolveStateDir(cfg)
	if got != "/tmp/custom-state" {
		t.Errorf("resolveStateDir = %q, want /tmp/custom-state", got)
	}
}

func TestResolveStateDir_Default(t *testing.T) {
	cfg := config.Config{} // no StateDir
	got := resolveStateDir(cfg)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".nxd")
	if got != want {
		t.Errorf("resolveStateDir = %q, want %q", got, want)
	}
}

func TestResolveStateDir_TildeExpansion(t *testing.T) {
	cfg := config.Config{}
	cfg.Workspace.StateDir = "~/.nxd"
	got := resolveStateDir(cfg)
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".nxd")
	if got != want {
		t.Errorf("resolveStateDir = %q, want %q", got, want)
	}
}

func TestFileExistsAt_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "test.txt")
	os.WriteFile(f, []byte("hello"), 0o644)
	if !fileExistsAt(f) {
		t.Error("fileExistsAt should return true for existing file")
	}
}

func TestFileExistsAt_MissingFile(t *testing.T) {
	if fileExistsAt("/nonexistent/path/file.txt") {
		t.Error("fileExistsAt should return false for missing file")
	}
}

func TestFileExistsAt_Directory(t *testing.T) {
	dir := t.TempDir()
	// A directory is not a file.
	if fileExistsAt(dir) {
		t.Error("fileExistsAt should return false for a directory")
	}
}

func TestShortPath_HomePrefix(t *testing.T) {
	home, _ := os.UserHomeDir()
	input := filepath.Join(home, ".nxd", "events.jsonl")
	got := shortPath(input)
	if !strings.HasPrefix(got, "~") {
		t.Errorf("shortPath(%q) = %q, expected ~ prefix", input, got)
	}
}

func TestShortPath_NonHomePrefix(t *testing.T) {
	input := "/tmp/something/else"
	got := shortPath(input)
	if got != input {
		t.Errorf("shortPath(%q) = %q, want %q", input, got, input)
	}
}

func TestCheckConfig_ValidConfig(t *testing.T) {
	env := setupTestEnv(t)
	result, cfg := checkConfig(env.Config)
	if result.Status == "fail" {
		t.Errorf("checkConfig returned fail: %s", result.Message)
	}
	if cfg.Workspace.Backend == "" {
		// Config loaded but fields may vary; just verify no panic
	}
}

func TestCheckConfig_MissingFile(t *testing.T) {
	result, _ := checkConfig("/nonexistent/nxd.yaml")
	if result.Status != "warn" && result.Status != "fail" {
		t.Errorf("expected warn or fail for missing config, got %q", result.Status)
	}
}

func TestCheckStateDir_ExistsWithStores(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nxd")
	os.MkdirAll(stateDir, 0o755)
	os.WriteFile(filepath.Join(stateDir, "events.jsonl"), []byte(""), 0o644)
	os.WriteFile(filepath.Join(stateDir, "nxd.db"), []byte(""), 0o644)

	cfg := config.Config{}
	cfg.Workspace.StateDir = stateDir
	result := checkStateDir(cfg)
	if result.Status != "ok" {
		t.Errorf("checkStateDir = %q (%s), want ok", result.Status, result.Message)
	}
}

func TestCheckStateDir_ExistsMissingStores(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, ".nxd")
	os.MkdirAll(stateDir, 0o755)
	// No events.jsonl or nxd.db

	cfg := config.Config{}
	cfg.Workspace.StateDir = stateDir
	result := checkStateDir(cfg)
	if result.Status != "warn" {
		t.Errorf("checkStateDir = %q, want warn (stores missing)", result.Status)
	}
}

func TestCheckStateDir_NotExist(t *testing.T) {
	cfg := config.Config{}
	cfg.Workspace.StateDir = "/tmp/nxd-nonexistent-12345xyz"
	result := checkStateDir(cfg)
	if result.Status != "warn" {
		t.Errorf("checkStateDir = %q, want warn (dir missing)", result.Status)
	}
}

func TestCheckDiskSpace_ExistingWritable(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{}
	cfg.Workspace.StateDir = dir
	result := checkDiskSpace(cfg)
	if result.Status != "ok" {
		t.Errorf("checkDiskSpace = %q (%s), want ok", result.Status, result.Message)
	}
}

func TestCheckDiskSpace_NonExistentDir(t *testing.T) {
	cfg := config.Config{}
	cfg.Workspace.StateDir = "/tmp/nxd-disk-nonexistent-xyz-12345"
	result := checkDiskSpace(cfg)
	if result.Status != "warn" {
		t.Errorf("checkDiskSpace = %q, want warn for nonexistent dir", result.Status)
	}
}

func TestCheckGoogleAI_WithKey(t *testing.T) {
	t.Setenv("GOOGLE_AI_API_KEY", "fake-key-12345")
	result := checkGoogleAI()
	if result.Status != "ok" {
		t.Errorf("checkGoogleAI with key = %q, want ok", result.Status)
	}
}

func TestCheckGoogleAI_WithoutKey(t *testing.T) {
	t.Setenv("GOOGLE_AI_API_KEY", "")
	result := checkGoogleAI()
	if result.Status != "warn" {
		t.Errorf("checkGoogleAI without key = %q, want warn", result.Status)
	}
}

func TestCheckPlugins_NoPluginDir(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{}
	cfg.Workspace.StateDir = dir
	result := checkPlugins(cfg)
	if result.Status != "ok" {
		t.Errorf("checkPlugins (no plugin dir) = %q, want ok", result.Status)
	}
}

func TestCheckPlugins_WithPluginDir(t *testing.T) {
	dir := t.TempDir()
	pluginDir := filepath.Join(dir, "plugins")
	os.MkdirAll(pluginDir, 0o755)

	cfg := config.Config{}
	cfg.Workspace.StateDir = dir
	result := checkPlugins(cfg)
	if result.Status != "ok" {
		t.Errorf("checkPlugins (with dir) = %q, want ok", result.Status)
	}
	if !strings.Contains(result.Message, "plugin directory") {
		t.Errorf("expected 'plugin directory' in message, got %q", result.Message)
	}
}

func TestCheckPlugins_EmptyStateDir(t *testing.T) {
	// Empty state dir means resolveStateDir uses default, but with no plugins dir
	cfg := config.Config{} // no state dir
	result := checkPlugins(cfg)
	// Either ok (no plugin dir found) or ok (defaults to ~/.nxd which may/may not have plugins)
	if result.Name != "Plugins" {
		t.Errorf("expected 'Plugins' check name, got %q", result.Name)
	}
}

// ─── doctor runDoctor with warnings/fails ────────────────────────────────────

func TestRunDoctor_WithWarnings(t *testing.T) {
	// Ensure Google AI key is unset so we get at least one warning
	t.Setenv("GOOGLE_AI_API_KEY", "")
	t.Setenv("NXD_UPDATE_CHECK", "false")

	cmd := newDoctorCmd()
	cmd.Flags().String("config", "", "")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	_ = cmd.Execute()

	output := buf.String()
	if !strings.Contains(output, "Results:") {
		t.Error("expected Results: line in output")
	}
}

// ─── helpers.go ──────────────────────────────────────────────────────────────

func TestLoadStores_Success(t *testing.T) {
	env := setupTestEnv(t)
	s, err := loadStores(env.Config)
	if err != nil {
		t.Fatalf("loadStores: %v", err)
	}
	defer s.Close()

	if s.Config.Workspace.Backend == "" {
		// backend was loaded from the config
	}
	if s.Events == nil {
		t.Error("expected non-nil Events store")
	}
	if s.Proj == nil {
		t.Error("expected non-nil Proj store")
	}
}

func TestLoadStores_MissingConfig(t *testing.T) {
	_, err := loadStores("/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestStoresClose_Nil(t *testing.T) {
	// Verify Close is safe on zero-value stores
	s := stores{}
	s.Close() // must not panic
}

func TestExpandHome_TildeSlash(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandHome("~/foo/bar")
	want := filepath.Join(home, "foo/bar")
	if got != want {
		t.Errorf("expandHome(~/foo/bar) = %q, want %q", got, want)
	}
}

func TestExpandHome_TildeAlone(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := expandHome("~")
	// filepath.Join(home, "") == home
	if got != home {
		t.Errorf("expandHome(~) = %q, want %q", got, home)
	}
}

func TestExpandHome_RelativePath(t *testing.T) {
	got := expandHome("relative/path")
	if got != "relative/path" {
		t.Errorf("expandHome(relative/path) = %q, want relative/path", got)
	}
}

// ─── config_cmd.go ───────────────────────────────────────────────────────────

func TestConfigShow_ValidConfig(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newConfigShowCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty YAML output")
	}
}

func TestConfigShow_MissingConfig(t *testing.T) {
	cmd := newConfigShowCmd()
	_, err := execCmd(t, cmd, "/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

func TestConfigValidate_ValidConfig(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newConfigValidateCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("config validate: %v", err)
	}
	if !strings.Contains(out, "PASSED") {
		t.Errorf("expected PASSED in output, got: %s", out)
	}
}

func TestConfigValidate_MissingConfig(t *testing.T) {
	cmd := newConfigValidateCmd()
	out, err := execCmd(t, cmd, "/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("expected FAILED in output, got: %s", out)
	}
}

// ─── metrics.go ──────────────────────────────────────────────────────────────

func TestMetricsCmd_Empty(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newMetricsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	if !strings.Contains(out, "No metrics") {
		t.Errorf("expected 'No metrics' message, got: %s", out)
	}
}

func TestMetricsCmd_JSONMode_Empty(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newMetricsCmd()
	cmd.Flags().Set("json", "true")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("metrics --json: %v", err)
	}
	// Empty state gives "No metrics" message regardless of --json
	_ = out
}

func TestMetricsCmd_MissingConfig(t *testing.T) {
	cmd := newMetricsCmd()
	_, err := execCmd(t, cmd, "/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

// ─── gc.go ───────────────────────────────────────────────────────────────────

func TestRunGC_WithMergedStory_DryRun(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	// Transition to merged status
	mergeEvt := state.NewEvent(state.EventStoryMerged, "system", "s-001", map[string]any{
		"id": "s-001", "branch": "feature/s-001",
	})
	env.Events.Append(mergeEvt)
	env.Proj.Project(mergeEvt)

	cmd := newGCCmd()
	cmd.Flags().Set("dry-run", "true")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("gc dry-run: %v", err)
	}
	_ = out // output varies based on branch retention days
}

func TestRunGC_MissingConfig(t *testing.T) {
	cmd := newGCCmd()
	_, err := execCmd(t, cmd, "/nonexistent/nxd.yaml")
	if err == nil {
		t.Error("expected error for missing config")
	}
}

// ─── logs.go ─────────────────────────────────────────────────────────────────

func TestRunLogs_NoTrace(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newLogsCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error for missing trace log")
	}
}

func TestRunLogs_WithTrace_Raw(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	artifactDir := filepath.Join(stateDir, "artifacts", "s-001")
	os.MkdirAll(artifactDir, 0o755)

	traceLine := `{"timestamp":"2026-01-01T10:00:00Z","type":"tool_call","tool":"read_file","detail":"reading main.go"}` + "\n"
	os.WriteFile(filepath.Join(artifactDir, "trace_events.jsonl"), []byte(traceLine), 0o644)

	cmd := newLogsCmd()
	cmd.Flags().Set("raw", "true")
	out, err := execCmd(t, cmd, env.Config, "s-001")
	if err != nil {
		t.Fatalf("logs --raw: %v", err)
	}
	if !strings.Contains(out, "tool_call") {
		t.Errorf("expected raw trace line in output, got: %s", out)
	}
}

func TestRunLogs_WithTrace_Formatted(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	artifactDir := filepath.Join(stateDir, "artifacts", "s-002")
	os.MkdirAll(artifactDir, 0o755)

	// Write multiple entries to test tailLog slicing
	var lines []string
	for i := 0; i < 60; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"timestamp":"2026-01-01T10:%02d:00Z","type":"progress","phase":"coding","detail":"step %d","iteration":%d}`,
			i%60, i, i,
		))
	}
	os.WriteFile(filepath.Join(artifactDir, "trace_events.jsonl"), []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	cmd := newLogsCmd()
	cmd.Flags().Set("lines", "10")
	out, err := execCmd(t, cmd, env.Config, "s-002")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out, "Trace log") {
		t.Errorf("expected 'Trace log' header in output, got: %s", out)
	}
}

func TestRunLogs_WithTrace_ToolEntry(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	artifactDir := filepath.Join(stateDir, "artifacts", "s-003")
	os.MkdirAll(artifactDir, 0o755)

	// Entry with tool field
	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"tool_call","phase":"execution","tool":"write_file","detail":"writing output.go","is_error":false}`
	os.WriteFile(filepath.Join(artifactDir, "trace_events.jsonl"), []byte(line+"\n"), 0o644)

	cmd := newLogsCmd()
	out, err := execCmd(t, cmd, env.Config, "s-003")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out, "write_file") {
		t.Errorf("expected tool name in output, got: %s", out)
	}
}

func TestRunLogs_WithTrace_ErrorEntry(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	artifactDir := filepath.Join(stateDir, "artifacts", "s-004")
	os.MkdirAll(artifactDir, 0o755)

	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"error","detail":"something failed","is_error":true}`
	os.WriteFile(filepath.Join(artifactDir, "trace_events.jsonl"), []byte(line+"\n"), 0o644)

	cmd := newLogsCmd()
	out, err := execCmd(t, cmd, env.Config, "s-004")
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected [ERROR] marker in output, got: %s", out)
	}
}

func TestRunLogs_WithTrace_InvalidJSON(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	artifactDir := filepath.Join(stateDir, "artifacts", "s-005")
	os.MkdirAll(artifactDir, 0o755)

	os.WriteFile(filepath.Join(artifactDir, "trace_events.jsonl"), []byte("not valid json\n"), 0o644)

	cmd := newLogsCmd()
	out, err := execCmd(t, cmd, env.Config, "s-005")
	if err != nil {
		t.Fatalf("logs with invalid JSON: %v", err)
	}
	// Invalid JSON falls back to raw line output
	if !strings.Contains(out, "not valid json") {
		t.Errorf("expected raw line in output, got: %s", out)
	}
}

// ─── formatEntry direct tests ─────────────────────────────────────────────────

func TestFormatEntry_WithTool(t *testing.T) {
	var buf bytes.Buffer
	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"tool","phase":"exec","tool":"read_file","detail":"reading"}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "read_file") {
		t.Errorf("expected tool name, got: %s", out)
	}
}

func TestFormatEntry_WithPhase(t *testing.T) {
	var buf bytes.Buffer
	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"progress","phase":"coding","detail":"working","iteration":3}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "coding") {
		t.Errorf("expected phase, got: %s", out)
	}
}

func TestFormatEntry_TypeOnly(t *testing.T) {
	var buf bytes.Buffer
	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"started","detail":"pipeline started"}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "started") {
		t.Errorf("expected type, got: %s", out)
	}
}

func TestFormatEntry_WithError(t *testing.T) {
	var buf bytes.Buffer
	line := `{"timestamp":"2026-01-01T10:00:00Z","type":"error","tool":"run_cmd","phase":"exec","detail":"cmd failed","is_error":true}`
	formatEntry(&buf, line)
	out := buf.String()
	if !strings.Contains(out, "ERROR") {
		t.Errorf("expected [ERROR] marker, got: %s", out)
	}
}

// ─── diff.go ─────────────────────────────────────────────────────────────────

func TestResolveWorktreePath_Conventional(t *testing.T) {
	dir := t.TempDir()
	storyID := "s-abc123"
	worktreeDir := filepath.Join(dir, "worktrees", storyID)
	os.MkdirAll(worktreeDir, 0o755)

	got, err := resolveWorktreePath(dir, storyID)
	if err != nil {
		t.Fatalf("resolveWorktreePath: %v", err)
	}
	if got != worktreeDir {
		t.Errorf("resolveWorktreePath = %q, want %q", got, worktreeDir)
	}
}

func TestResolveWorktreePath_NotFound(t *testing.T) {
	dir := t.TempDir()
	_, err := resolveWorktreePath(dir, "nonexistent-story")
	if err == nil {
		t.Error("expected error when worktree not found")
	}
}

func TestRunDiff_NoWorktree(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newDiffCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error when worktree not found")
	}
}

// ─── status.go ───────────────────────────────────────────────────────────────

func TestCountByStatus_Empty(t *testing.T) {
	counts := countByStatus(nil)
	if len(counts) != 0 {
		t.Errorf("countByStatus(nil) = %v, want empty", counts)
	}
}

func TestCountByStatus_Mixed(t *testing.T) {
	stories := []state.Story{
		{Status: "draft"},
		{Status: "draft"},
		{Status: "in_progress"},
		{Status: "merged"},
	}
	counts := countByStatus(stories)
	if counts["draft"] != 2 {
		t.Errorf("draft count = %d, want 2", counts["draft"])
	}
	if counts["in_progress"] != 1 {
		t.Errorf("in_progress count = %d, want 1", counts["in_progress"])
	}
}

func TestStatusCmd_JSONMode_ReqFilter(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--req", "r-001", "--json")
	if err != nil {
		t.Fatalf("status --req --json: %v", err)
	}
	var result jsonStatusOutput
	if jsonErr := json.Unmarshal([]byte(out), &result); jsonErr != nil {
		t.Fatalf("invalid JSON: %v\n%s", jsonErr, out)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("expected 1 req, got %d", len(result.Requirements))
	}
}

func TestStatusCmd_JSONMode_NonExistentReq(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newStatusCmd()
	_, err := execCmd(t, cmd, env.Config, "--req", "nonexistent", "--json")
	if err == nil {
		t.Error("expected error for nonexistent req in JSON mode")
	}
}

func TestStatusCmd_ShowAll(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00200", "Req ShowAll 1", env.Dir)
	seedTestReq(t, env, "req-00300", "Req ShowAll 2", "/other/repo")

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--all")
	if err != nil {
		t.Fatalf("status --all: %v", err)
	}
	if !strings.Contains(out, "Req ShowAll 1") {
		t.Error("expected Req ShowAll 1 in --all output")
	}
}

func TestStatusCmd_FilterByReq_WithEscalation(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Auth module", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Login", 3)
	seedTestEscalation(t, env, "s-001", "junior-1", "stuck")

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--req", "r-001")
	if err != nil {
		t.Fatalf("status --req: %v", err)
	}
	if !strings.Contains(out, "tier:1") {
		t.Errorf("expected escalation tier in output, got: %s", out)
	}
}

// ─── req.go resolveRequirement ────────────────────────────────────────────────

func TestResolveRequirement_MissingFile(t *testing.T) {
	cmd := newReqCmd()
	cmd.Flags().Set("file", "/nonexistent/file.txt")
	_, err := resolveRequirement(cmd, nil)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ─── estimate.go pure functions ───────────────────────────────────────────────

func TestPrintEstimateJSON_ValidEstimate(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Build auth",
		IsQuick:     false,
		Summary: engine.EstimateSummary{
			StoryCount:    3,
			TotalPoints:   10,
			HoursLow:      8,
			HoursHigh:     16,
			QuoteLow:      800,
			QuoteHigh:     1600,
			Rate:          100,
			MarginPercent: 20,
		},
	}
	err := printEstimateJSON(est)
	if err != nil {
		t.Fatalf("printEstimateJSON: %v", err)
	}
}

func TestPrintLiveTable_NoLLMCost(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Add search",
		Summary: engine.EstimateSummary{
			StoryCount:    2,
			TotalPoints:   8,
			HoursLow:      6,
			HoursHigh:     12,
			QuoteLow:      600,
			QuoteHigh:     1200,
			Rate:          100,
			LLMCost:       0,
			MarginPercent: 25,
		},
		Stories: []engine.StoryEstimate{
			{Title: "Story A", Complexity: 3, HoursLow: 2, HoursHigh: 4, Role: "junior"},
			{Title: "Story B", Complexity: 5, HoursLow: 4, HoursHigh: 8, Role: "senior"},
		},
	}
	err := printLiveTable(est)
	if err != nil {
		t.Fatalf("printLiveTable: %v", err)
	}
}

func TestPrintLiveTable_WithLLMCost(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Refactor auth",
		Summary: engine.EstimateSummary{
			StoryCount:    1,
			TotalPoints:   5,
			HoursLow:      4,
			HoursHigh:     8,
			QuoteLow:      400,
			QuoteHigh:     800,
			Rate:          100,
			LLMCost:       0.05,
			MarginPercent: 20,
		},
		Stories: []engine.StoryEstimate{
			{Title: "Refactor login", Complexity: 5, HoursLow: 4, HoursHigh: 8, Role: "senior"},
		},
	}
	err := printLiveTable(est)
	if err != nil {
		t.Fatalf("printLiveTable with LLMCost: %v", err)
	}
}

func TestPrintEstimateTable_Live_WithStories(t *testing.T) {
	est := engine.Estimate{
		Requirement: "Build search",
		IsQuick:     false,
		Summary: engine.EstimateSummary{
			StoryCount:    3,
			TotalPoints:   12,
			HoursLow:      10,
			HoursHigh:     20,
			QuoteLow:      1000,
			QuoteHigh:     2000,
			Rate:          100,
			LLMCost:       0,
			MarginPercent: 20,
		},
		Stories: []engine.StoryEstimate{
			{Title: "Index documents", Complexity: 5, HoursLow: 4, HoursHigh: 8, Role: "junior"},
			{Title: "Build query parser", Complexity: 4, HoursLow: 3, HoursHigh: 7, Role: "senior"},
			{Title: "Add API endpoint", Complexity: 3, HoursLow: 3, HoursHigh: 5, Role: "junior"},
		},
	}
	err := printEstimateTable(est)
	if err != nil {
		t.Fatalf("printEstimateTable live with stories: %v", err)
	}
}

// ─── models.go ───────────────────────────────────────────────────────────────

func TestCollectConfiguredModels_OllamaOnly(t *testing.T) {
	cfg := config.Config{}
	cfg.Models.Junior.Provider = "ollama"
	cfg.Models.Junior.Model = "gemma4:12b"
	cfg.Models.Senior.Provider = "ollama"
	cfg.Models.Senior.Model = "gemma4:26b"
	cfg.Models.TechLead.Provider = "ollama"
	cfg.Models.TechLead.Model = "gemma4:26b" // duplicate

	ollama, google := collectConfiguredModels(cfg)
	if len(google) != 0 {
		t.Errorf("expected 0 google models, got %d", len(google))
	}
	// gemma4:26b should appear only once despite being in two model configs
	seen := map[string]bool{}
	for _, m := range ollama {
		if seen[m] {
			t.Errorf("duplicate ollama model %q", m)
		}
		seen[m] = true
	}
}

func TestCollectConfiguredModels_GoogleModels(t *testing.T) {
	cfg := config.Config{}
	cfg.Models.Senior.Provider = "google"
	cfg.Models.Senior.GoogleModel = "gemini-2.0-flash"
	cfg.Models.TechLead.Provider = "google"
	cfg.Models.TechLead.GoogleModel = "gemini-2.0-flash" // duplicate

	_, google := collectConfiguredModels(cfg)
	if len(google) != 1 {
		t.Errorf("expected 1 unique google model, got %d: %v", len(google), google)
	}
}

func TestCollectConfiguredModels_Empty(t *testing.T) {
	cfg := config.Config{}
	ollama, google := collectConfiguredModels(cfg)
	if len(ollama) != 0 || len(google) != 0 {
		t.Errorf("expected empty slices for empty config, got ollama=%v google=%v", ollama, google)
	}
}

// ─── init.go ─────────────────────────────────────────────────────────────────

func TestNewInitCmd_Flags(t *testing.T) {
	cmd := newInitCmd()
	if cmd == nil {
		t.Fatal("newInitCmd returned nil")
	}
	if cmd.Use != "init" {
		t.Errorf("Use = %q, want init", cmd.Use)
	}
}

// ─── archive.go ──────────────────────────────────────────────────────────────

func TestCleanupStoryBranch_NoBranch(t *testing.T) {
	story := state.Story{ID: "s-001", Status: "merged", Branch: ""}
	// Should return immediately without panic
	cleanupStoryBranch("/tmp", story)
}

func TestCleanupStoryBranch_WithBranch(t *testing.T) {
	story := state.Story{ID: "s-001", Status: "merged", Branch: "feature/test-branch"}
	// Should attempt cleanup and ignore errors (best-effort)
	cleanupStoryBranch("/tmp", story)
}

// ─── report.go ───────────────────────────────────────────────────────────────

func TestReportCmd_ReqNotFound(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newReportCmd()
	_, err := execCmd(t, cmd, env.Config, "nonexistent-req")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

func TestReportCmd_WithOutput_ReqNotFound(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	outFile := filepath.Join(dir, "report.md")

	cmd := newReportCmd()
	cmd.Flags().Set("output", outFile)
	_, err := execCmd(t, cmd, env.Config, "nonexistent-req")
	if err == nil {
		t.Error("expected error for nonexistent requirement")
	}
}

// ─── learn.go ────────────────────────────────────────────────────────────────

func TestLearnCmd_ValidRepo(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()

	// Create a minimal go project in dir
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644)

	cmd := newLearnCmd()
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn: %v", err)
	}
	if !strings.Contains(out, "Profile saved") {
		t.Errorf("expected 'Profile saved' in output, got: %s", out)
	}
}

func TestLearnCmd_ForceFlag(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("force", "true")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn --force: %v", err)
	}
	if !strings.Contains(out, "Pass 1") {
		t.Errorf("expected Pass 1 output, got: %s", out)
	}
}

func TestLearnCmd_Pass1Only(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("pass", "1")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn --pass=1: %v", err)
	}
	if !strings.Contains(out, "Pass 1") {
		t.Errorf("expected Pass 1 output, got: %s", out)
	}
}

func TestLearnCmd_Pass2Only(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("pass", "2")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn --pass=2: %v", err)
	}
	if !strings.Contains(out, "Pass 2") {
		t.Errorf("expected Pass 2 output, got: %s", out)
	}
}

func TestLearnCmd_Pass3Only(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("pass", "3")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn --pass=3: %v", err)
	}
	if !strings.Contains(out, "Pass 3") {
		t.Errorf("expected Pass 3 (skipped) output, got: %s", out)
	}
}

func TestLearnCmd_JSONOutput(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("json", "true")
	cmd.Flags().Set("pass", "1")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn --json: %v", err)
	}
	// Output contains both human-readable + JSON
	if !strings.Contains(out, "{") {
		t.Errorf("expected JSON in output, got: %s", out)
	}
}

func TestLearnCmd_MissingConfig(t *testing.T) {
	cmd := newLearnCmd()
	cmd.Flags().Set("pass", "1")
	_, err := execCmd(t, cmd, "/nonexistent/nxd.yaml", t.TempDir())
	if err == nil {
		t.Error("expected error for missing config")
	}
}

// ─── root.go checkForModelUpdates ─────────────────────────────────────────────

func TestCheckForModelUpdates_DisabledByEnv(t *testing.T) {
	t.Setenv("NXD_UPDATE_CHECK", "false")
	// Should return immediately — no panic
	cmd := newStatusCmd()
	checkForModelUpdates(cmd)
}

func TestCheckForModelUpdates_UpdateCheckDisabledInConfig(t *testing.T) {
	env := setupTestEnv(t)
	// The test config has UpdateCheck = false (default), so this returns early
	cmd := newStatusCmd()
	if cmd.Flags().Lookup("config") == nil {
		cmd.Flags().String("config", "", "")
	}
	cmd.Flags().Set("config", env.Config)
	checkForModelUpdates(cmd) // must not panic
}

// ─── tailLog directly ─────────────────────────────────────────────────────────

func TestTailLog_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "trace.jsonl")
	os.WriteFile(f, []byte(""), 0o644)

	var buf bytes.Buffer
	err := tailLog(f, 50, false, &buf)
	if err != nil {
		t.Fatalf("tailLog empty: %v", err)
	}
}

func TestTailLog_FewLines(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "trace.jsonl")
	lines := []string{
		`{"timestamp":"2026-01-01T10:00:00Z","type":"start","detail":"begin"}`,
		`{"timestamp":"2026-01-01T10:01:00Z","type":"end","detail":"done"}`,
	}
	os.WriteFile(f, []byte(strings.Join(lines, "\n")+"\n"), 0o644)

	var buf bytes.Buffer
	err := tailLog(f, 50, false, &buf)
	if err != nil {
		t.Fatalf("tailLog: %v", err)
	}
	if buf.Len() == 0 {
		t.Error("expected output from tailLog")
	}
}

func TestTailLog_RawMode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "trace.jsonl")
	os.WriteFile(f, []byte(`{"type":"start","detail":"x"}`+"\n"), 0o644)

	var buf bytes.Buffer
	err := tailLog(f, 50, true, &buf)
	if err != nil {
		t.Fatalf("tailLog raw: %v", err)
	}
	if !strings.Contains(buf.String(), "start") {
		t.Errorf("expected raw line in output, got: %s", buf.String())
	}
}

func TestTailLog_MissingFile(t *testing.T) {
	var buf bytes.Buffer
	err := tailLog("/nonexistent/trace.jsonl", 50, false, &buf)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

// ─── GC with merged story (no branch) ────────────────────────────────────────

func TestRunGC_MergedNoBranch(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Test Req", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Story 1", 3)

	// Merge without a branch name
	mergeEvt := state.NewEvent(state.EventStoryMerged, "system", "s-001", map[string]any{
		"id": "s-001",
	})
	env.Events.Append(mergeEvt)
	env.Proj.Project(mergeEvt)

	cmd := newGCCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("gc with no branch: %v", err)
	}
	_ = out
}

// ─── report.go with valid requirement ────────────────────────────────────────

func TestReportCmd_ValidReq_Markdown(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-001", "Build API", env.Dir)
	seedTestStory(t, env, "s-001", "r-001", "Add endpoint", 3)

	cmd := newReportCmd()
	out, err := execCmd(t, cmd, env.Config, "r-001")
	if err != nil {
		t.Fatalf("report: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty report output")
	}
}

func TestReportCmd_ValidReq_HTML(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-002", "Build API", env.Dir)
	seedTestStory(t, env, "s-002", "r-002", "Add endpoint", 3)

	cmd := newReportCmd()
	cmd.Flags().Set("html", "true")
	out, err := execCmd(t, cmd, env.Config, "r-002")
	if err != nil {
		t.Fatalf("report --html: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty HTML report output")
	}
}

func TestReportCmd_ValidReq_ToFile(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "r-003", "Build API", env.Dir)
	seedTestStory(t, env, "s-003", "r-003", "Add endpoint", 3)

	outFile := filepath.Join(t.TempDir(), "report.md")
	cmd := newReportCmd()
	cmd.Flags().Set("output", outFile)
	_, err := execCmd(t, cmd, env.Config, "r-003")
	if err != nil {
		t.Fatalf("report --output: %v", err)
	}
	if _, statErr := os.Stat(outFile); os.IsNotExist(statErr) {
		t.Error("expected report file to be created")
	}
}

// ─── traceEntry JSON time handling ───────────────────────────────────────────

func TestTraceEntry_JSONMarshal(t *testing.T) {
	entry := traceEntry{
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Type:      "progress",
		Phase:     "coding",
		Tool:      "write_file",
		Detail:    "writing output",
		Iteration: 3,
		Model:     "gemma4:26b",
		Tokens:    1024,
		IsError:   false,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded traceEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.Type != entry.Type {
		t.Errorf("Type = %q, want %q", decoded.Type, entry.Type)
	}
	if decoded.Tokens != entry.Tokens {
		t.Errorf("Tokens = %d, want %d", decoded.Tokens, entry.Tokens)
	}
}

// ─── runMetrics with JSON mode and populated file ─────────────────────────────

func TestMetricsCmd_JSONMode_WithData(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")

	// Write a minimal metrics.jsonl file
	metricsLine := `{"req_id":"r-001","story_id":"s-001","role":"junior","model":"gemma4:26b","provider":"ollama","input_tokens":100,"output_tokens":50,"latency_ms":1200,"timestamp":"2026-01-01T10:00:00Z"}` + "\n"
	os.WriteFile(filepath.Join(stateDir, "metrics.jsonl"), []byte(metricsLine), 0o644)

	cmd := newMetricsCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("metrics with data: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty metrics output")
	}
}

func TestMetricsCmd_JSONMode_WithData_JSON(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")

	metricsLine := `{"req_id":"r-001","story_id":"s-001","role":"junior","model":"gemma4:26b","provider":"ollama","input_tokens":100,"output_tokens":50,"latency_ms":1200,"timestamp":"2026-01-01T10:00:00Z"}` + "\n"
	os.WriteFile(filepath.Join(stateDir, "metrics.jsonl"), []byte(metricsLine), 0o644)

	cmd := newMetricsCmd()
	cmd.Flags().Set("json", "true")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("metrics --json with data: %v", err)
	}
	if !strings.Contains(out, "{") {
		t.Errorf("expected JSON output, got: %s", out)
	}
}

// ─── showRequirementStatus edge cases ────────────────────────────────────────

func TestStatusCmd_FilterByReq_WithPRInfo(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00400", "PR test req", env.Dir)

	// Seed a story with a branch and PR info
	storyEvt := state.NewEvent(state.EventStoryCreated, "system", "s-pr01", map[string]any{
		"id": "s-pr01", "req_id": "req-00400", "title": "Add endpoint",
		"description": "Test", "complexity": 3,
	})
	env.Events.Append(storyEvt)
	env.Proj.Project(storyEvt)

	// Add PR info
	prEvt := state.NewEvent(state.EventStoryMerged, "system", "s-pr01", map[string]any{
		"id": "s-pr01", "branch": "feature/add-endpoint", "pr_url": "https://github.com/org/repo/pull/42", "pr_number": 42,
	})
	env.Events.Append(prEvt)
	env.Proj.Project(prEvt)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--req", "req-00400")
	if err != nil {
		t.Fatalf("status --req with PR: %v", err)
	}
	// Output should contain branch or PR info
	_ = out
}

func TestStatusCmd_FilterByReq_NoStories(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00500", "Empty req", env.Dir)

	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config, "--req", "req-00500")
	if err != nil {
		t.Fatalf("status --req no stories: %v", err)
	}
	if !strings.Contains(out, "No stories") {
		t.Errorf("expected 'No stories' message, got: %s", out)
	}
}

// ─── loadStores failure paths ─────────────────────────────────────────────────

func TestLoadStores_InvalidStateDirPath(t *testing.T) {
	dir := t.TempDir()
	// Write a config pointing to a state_dir that is a FILE not a directory
	// This will cause MkdirAll to fail
	stateFile := filepath.Join(dir, "notadir")
	os.WriteFile(stateFile, []byte("I am a file"), 0o644)

	cfgContent := "version: \"1.0\"\nworkspace:\n  state_dir: " + stateFile + "/nested\n  backend: sqlite\nmerge:\n  base_branch: main\n  mode: local\ncleanup:\n  branch_retention_days: 7\n"
	cfgPath := filepath.Join(dir, "nxd.yaml")
	os.WriteFile(cfgPath, []byte(cfgContent), 0o644)

	_, err := loadStores(cfgPath)
	if err == nil {
		t.Error("expected error when state dir cannot be created")
	}
}

// ─── checkDiskSpace permission-denied simulation ───────────────────────────────

func TestCheckDiskSpace_DefaultDir_WhenConfigEmpty(t *testing.T) {
	// Empty config causes checkDiskSpace to fall back to ~/.nxd
	// This tests the fallback code path
	cfg := config.Config{}
	result := checkDiskSpace(cfg)
	// Result is either ok (if ~/.nxd exists and is writable) or warn (if not found)
	// Either is valid — just ensure it doesn't panic and returns a valid status
	validStatuses := map[string]bool{"ok": true, "warn": true, "fail": true}
	if !validStatuses[result.Status] {
		t.Errorf("checkDiskSpace returned invalid status: %q", result.Status)
	}
	if result.Name == "" {
		t.Error("expected non-empty Name")
	}
}

func TestCheckDiskSpace_ReadOnlyDir(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("running as root, skip permission test")
	}
	dir := t.TempDir()
	// Make the directory read-only so writes fail
	os.Chmod(dir, 0o555)
	defer os.Chmod(dir, 0o755) // restore for cleanup

	cfg := config.Config{}
	cfg.Workspace.StateDir = dir
	result := checkDiskSpace(cfg)
	// Should return fail (permission denied) or warn (write failed)
	if result.Status == "ok" {
		t.Errorf("expected fail or warn for read-only dir, got ok")
	}
}

// ─── init.go integration ─────────────────────────────────────────────────────

func TestRunInit_AlreadyExists(t *testing.T) {
	// Run init twice — the second run should detect nxd.yaml already exists
	// We can't easily test the ~/.nxd path but we can test the "config already exists" branch
	// by manipulating the working directory.
	origDir, _ := os.Getwd()
	tmpDir := t.TempDir()
	defer os.Chdir(origDir)
	os.Chdir(tmpDir)

	// Create the nxd.yaml already
	os.WriteFile(filepath.Join(tmpDir, "nxd.yaml"), []byte("version: \"1.0\"\n"), 0o644)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	_ = cmd.Execute()

	out := buf.String()
	if !strings.Contains(out, "already exists") {
		t.Errorf("expected 'already exists' in output, got: %s", out)
	}
}

// ─── resolveRequirement stdin path (indirect) ────────────────────────────────

func TestResolveRequirement_StdinDash_ConflictWithArg(t *testing.T) {
	// When --file="-" AND a positional arg are given, we get a conflict error
	cmd := newReqCmd()
	cmd.Flags().Set("file", "-")
	_, err := resolveRequirement(cmd, []string{"also text"})
	if err == nil {
		t.Error("expected error when --file=- and positional arg both given")
	}
}

func TestResolveRequirement_StdinDash_WithContent(t *testing.T) {
	// Provide content via a pipe into os.Stdin
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdin = r

	// Write to the write-end and close it
	go func() {
		w.WriteString("Add login endpoint via stdin\n")
		w.Close()
	}()

	cmd := newReqCmd()
	cmd.Flags().Set("file", "-")
	got, err := resolveRequirement(cmd, nil)
	if err != nil {
		t.Fatalf("resolveRequirement stdin: %v", err)
	}
	if got != "Add login endpoint via stdin" {
		t.Errorf("got %q, want 'Add login endpoint via stdin'", got)
	}
}

func TestResolveRequirement_StdinDash_EmptyContent(t *testing.T) {
	origStdin := os.Stdin
	defer func() { os.Stdin = origStdin }()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}
	os.Stdin = r
	w.Close() // immediately EOF with no content

	cmd := newReqCmd()
	cmd.Flags().Set("file", "-")
	_, err = resolveRequirement(cmd, nil)
	if err == nil {
		t.Error("expected error for empty stdin")
	}
}

// ─── archive.go cleanupStoryBranch ────────────────────────────────────────────

func TestArchiveCmd_WithBranchStory(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00600", "Branch test", env.Dir)

	// Seed a story with branch name
	storyEvt := state.NewEvent(state.EventStoryCreated, "system", "s-branch01", map[string]any{
		"id": "s-branch01", "req_id": "req-00600", "title": "Feature",
		"description": "Test", "complexity": 2,
	})
	env.Events.Append(storyEvt)
	env.Proj.Project(storyEvt)

	// Archive the requirement (which calls cleanupStoryBranch)
	cmd := newArchiveCmd()
	_, err := execCmd(t, cmd, env.Config, "req-00600")
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
}

// ─── diff.go with artifact launch config ──────────────────────────────────────

func TestResolveWorktreePath_WithLaunchConfig_FallsBackToConventional(t *testing.T) {
	dir := t.TempDir()
	storyID := "s-lc01"

	// Create a launch config in the artifact store path
	artifactDir := filepath.Join(dir, "artifacts", storyID)
	os.MkdirAll(artifactDir, 0o755)
	launchCfg := `{"prompt":"write some code","worktree_path":"/nonexistent/worktree"}`
	os.WriteFile(filepath.Join(artifactDir, "launch_config.json"), []byte(launchCfg), 0o644)

	// Conventional worktree also doesn't exist, so should error
	_, err := resolveWorktreePath(dir, storyID)
	if err == nil {
		t.Error("expected error when conventional worktree doesn't exist")
	}
}

// ─── runDiff with existing worktree (but git will fail — check we reach git) ──

func TestRunDiff_StatFlag_NoWorktree(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newDiffCmd()
	cmd.Flags().Set("stat", "true")
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error when worktree not found")
	}
}

func TestRunDiff_CachedFlag_NoWorktree(t *testing.T) {
	env := setupTestEnv(t)
	cmd := newDiffCmd()
	cmd.Flags().Set("cached", "true")
	_, err := execCmd(t, cmd, env.Config, "nonexistent-story")
	if err == nil {
		t.Error("expected error when worktree not found")
	}
}

// ─── GC with branch that fails reaper cleanup ────────────────────────────────

func TestRunGC_ReaperNoEligible(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00700", "GC test req", env.Dir)
	seedTestStory(t, env, "s-gc01", "req-00700", "Story A", 3)

	// Merge the story WITH a branch
	mergeEvt := state.NewEvent(state.EventStoryMerged, "system", "s-gc01", map[string]any{
		"id": "s-gc01", "branch": "feature/story-a",
	})
	env.Events.Append(mergeEvt)
	env.Proj.Project(mergeEvt)

	// Run GC without dry-run: reaper should find branch is too new (< retention days)
	cmd := newGCCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("gc: %v", err)
	}
	_ = out
}

// ─── checkForModelUpdates with valid config but update_check disabled ─────────

func TestCheckForModelUpdates_ValidConfig_NoUpdateCheck(t *testing.T) {
	env := setupTestEnv(t)

	// The test config doesn't have update_check: true, so this returns early after config load
	cmd := newStatusCmd()
	if cmd.Flags().Lookup("config") == nil {
		cmd.Flags().String("config", "", "")
	}
	cmd.Flags().Set("config", env.Config)
	checkForModelUpdates(cmd) // must not panic, must not hang
}

// ─── events.go: formatPayload with long payload ───────────────────────────────

func TestFormatPayload_Long(t *testing.T) {
	// Build a payload > 200 chars when marshaled
	long := strings.Repeat("x", 250)
	payload := fmt.Sprintf(`{"key":"%s"}`, long)
	got := formatPayload([]byte(payload))
	if len(got) > 200 {
		t.Errorf("formatPayload should truncate to <=200 chars, got %d", len(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("truncated payload should end with '...', got: %s", got)
	}
}

func TestFormatPayload_Short(t *testing.T) {
	payload := `{"key":"value"}`
	got := formatPayload([]byte(payload))
	if !strings.Contains(got, "key") {
		t.Errorf("expected key in output, got: %s", got)
	}
}

func TestFormatPayload_InvalidJSON(t *testing.T) {
	payload := []byte("not json")
	got := formatPayload(payload)
	if got != "not json" {
		t.Errorf("expected raw string for invalid JSON, got: %s", got)
	}
}

// ─── events.go: runEvents with limit ─────────────────────────────────────────

func TestRunEvents_WithLimit(t *testing.T) {
	env := setupTestEnv(t)
	// Seed more than the limit worth of events
	for i := 0; i < 5; i++ {
		seedTestReq(t, env, fmt.Sprintf("req-%05d", i+1000), fmt.Sprintf("Req %d", i), env.Dir)
	}

	cmd := newEventsCmd()
	cmd.Flags().Set("limit", "3")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("events --limit: %v", err)
	}
	if !strings.Contains(out, "Events (") {
		t.Errorf("expected events header, got: %s", out)
	}
}

func TestRunEvents_FilterByType(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00800", "Filter req", env.Dir)
	seedTestStory(t, env, "s-filter01", "req-00800", "Story", 3)

	cmd := newEventsCmd()
	cmd.Flags().Set("type", "REQ_SUBMITTED")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("events --type: %v", err)
	}
	_ = out
}

func TestRunEvents_FilterByStory(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-00900", "Story filter req", env.Dir)
	seedTestStory(t, env, "s-filter02", "req-00900", "Story", 3)

	cmd := newEventsCmd()
	cmd.Flags().Set("story", "s-filter02")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("events --story: %v", err)
	}
	_ = out
}

// ─── status.go: runStatus with no-filter (current repo) ──────────────────────

func TestStatusCmd_NoArgs_CurrentRepo(t *testing.T) {
	env := setupTestEnv(t)
	// No --all flag, so it filters by current working directory
	// With no matching reqs it shows the "No requirements found" message
	cmd := newStatusCmd()
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("status (no filter): %v", err)
	}
	if !strings.Contains(out, "No requirements found") {
		t.Errorf("expected 'No requirements found', got: %s", out)
	}
}

// ─── GC dry-run with no branches ─────────────────────────────────────────────

func TestRunGC_DryRun_MergedStoryNoBranch(t *testing.T) {
	env := setupTestEnv(t)
	seedTestReq(t, env, "req-01000", "GC req 2", env.Dir)
	seedTestStory(t, env, "s-gc02", "req-01000", "Story B", 3)

	// Merge without a branch
	mergeEvt := state.NewEvent(state.EventStoryMerged, "system", "s-gc02", map[string]any{
		"id": "s-gc02",
	})
	env.Events.Append(mergeEvt)
	env.Proj.Project(mergeEvt)

	cmd := newGCCmd()
	cmd.Flags().Set("dry-run", "true")
	out, err := execCmd(t, cmd, env.Config)
	if err != nil {
		t.Fatalf("gc dry-run no branches: %v", err)
	}
	_ = out // merged story with no branch is excluded from branch list
}

// ─── learn.go: with signals output ───────────────────────────────────────────

func TestLearnCmd_DockerSignal(t *testing.T) {
	env := setupTestEnv(t)
	dir := t.TempDir()

	// Create a repo with Docker signal
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testproject\ngo 1.21\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM golang:1.21\nCOPY . .\nRUN go build\n"), 0o644)

	cmd := newLearnCmd()
	cmd.Flags().Set("pass", "1")
	out, err := execCmd(t, cmd, env.Config, dir)
	if err != nil {
		t.Fatalf("learn with Dockerfile: %v", err)
	}
	if !strings.Contains(out, "Pass 1") {
		t.Errorf("expected Pass 1 in output, got: %s", out)
	}
}

// ─── reverseEvents extra edge cases ──────────────────────────────────────────

// ─── runDiff when worktree exists but git diff fails ─────────────────────────

func TestRunDiff_WorktreeExists_GitFails(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	// Create a conventional worktree dir that's NOT a git repo (so git diff fails)
	worktreeDir := filepath.Join(stateDir, "worktrees", "s-diff01")
	os.MkdirAll(worktreeDir, 0o755)

	cmd := newDiffCmd()
	_, err := execCmd(t, cmd, env.Config, "s-diff01")
	// git diff will fail because the dir is not a git repo
	if err == nil {
		t.Error("expected error when worktree is not a git repo")
	}
}

func TestRunDiff_WorktreeExists_StatFlag(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	worktreeDir := filepath.Join(stateDir, "worktrees", "s-diff02")
	os.MkdirAll(worktreeDir, 0o755)

	cmd := newDiffCmd()
	cmd.Flags().Set("stat", "true")
	_, err := execCmd(t, cmd, env.Config, "s-diff02")
	if err == nil {
		t.Error("expected error when worktree is not a git repo (stat mode)")
	}
}

func TestRunDiff_WorktreeExists_CachedFlag(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	worktreeDir := filepath.Join(stateDir, "worktrees", "s-diff03")
	os.MkdirAll(worktreeDir, 0o755)

	cmd := newDiffCmd()
	cmd.Flags().Set("cached", "true")
	_, err := execCmd(t, cmd, env.Config, "s-diff03")
	if err == nil {
		t.Error("expected error when worktree is not a git repo (cached mode)")
	}
}

// ─── checkGit and checkTmux fail paths ───────────────────────────────────────
// checkGo, checkGit, checkTmux, checkOllamaRunning, checkGemmaModel are tested
// via TestDoctorCmd_IndividualCheckFunctions already for their ok/warn/fail paths.
// The doctor_test tests call them all and verify status is valid.
// Here we add a direct unit test for checkGemmaModel result fields.

func TestCheckGemmaModel_ReturnsValidResult(t *testing.T) {
	result := checkGemmaModel()
	validStatuses := map[string]bool{"ok": true, "warn": true, "fail": true}
	if !validStatuses[result.Status] {
		t.Errorf("checkGemmaModel status = %q, invalid", result.Status)
	}
	if result.Name == "" {
		t.Error("expected non-empty Name")
	}
	if result.Message == "" {
		t.Error("expected non-empty Message")
	}
}

func TestCheckMemPalace_ReturnsValidResult(t *testing.T) {
	result := checkMemPalace()
	validStatuses := map[string]bool{"ok": true, "warn": true, "fail": true}
	if !validStatuses[result.Status] {
		t.Errorf("checkMemPalace status = %q, invalid", result.Status)
	}
}

// ─── shortPath when home dir is prefix ───────────────────────────────────────

func TestShortPath_ExactHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	got := shortPath(home)
	// shortPath(home) = "~" + home[len(home):] = "~"
	if got != "~" {
		t.Errorf("shortPath(home) = %q, want ~", got)
	}
}

// ─── resolveStateDir when UserHomeDir fails can't be tested directly ──────────
// But we can ensure it doesn't break on an unusual StateDir

func TestResolveStateDir_SlashStateDir(t *testing.T) {
	cfg := config.Config{}
	cfg.Workspace.StateDir = "/var/nxd-state"
	got := resolveStateDir(cfg)
	if got != "/var/nxd-state" {
		t.Errorf("resolveStateDir = %q, want /var/nxd-state", got)
	}
}

func TestReverseEvents_EvenCount(t *testing.T) {
	evts := []state.Event{{ID: "1"}, {ID: "2"}, {ID: "3"}, {ID: "4"}}
	got := reverseEvents(evts)
	if got[0].ID != "4" || got[3].ID != "1" {
		t.Errorf("reverseEvents even count = %v, want [4 3 2 1]", got)
	}
}

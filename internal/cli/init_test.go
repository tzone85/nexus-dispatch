package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInit_CreatesConfigWithoutExampleFile(t *testing.T) {
	// Run init from a temp directory where no nxd.config.example.yaml exists.
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	// Override HOME so init creates ~/.nxd inside the temp dir
	t.Setenv("HOME", dir)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify nxd.yaml was created
	cfgPath := filepath.Join(dir, "nxd.yaml")
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("nxd.yaml not created: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("nxd.yaml is empty")
	}

	// Verify output message
	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("Created nxd.yaml")) {
		t.Fatalf("expected 'Created nxd.yaml' in output, got: %s", output)
	}
	// Should NOT contain the old warning about example config
	if bytes.Contains([]byte(output), []byte("Warning: could not read")) {
		t.Fatalf("unexpected warning in output: %s", output)
	}
	// Post-install hint must reference gemma4:e4b (canonical default), NOT deepseek.
	// Triggered only when Ollama isn't running in the test env; check both branches.
	if bytes.Contains([]byte(output), []byte("Warning: Ollama not detected")) {
		if !bytes.Contains([]byte(output), []byte("ollama pull gemma4:e4b")) {
			t.Fatalf("post-install hint should suggest gemma4:e4b, got: %s", output)
		}
		if bytes.Contains([]byte(output), []byte("deepseek-coder-v2")) {
			t.Fatalf("post-install hint must not suggest deepseek-coder-v2, got: %s", output)
		}
	}
}

func TestRunInit_SkipsExistingConfig(t *testing.T) {
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { os.Chdir(orig) })

	t.Setenv("HOME", dir)

	// Pre-create an nxd.yaml with custom content
	customCfg := []byte("version: \"2.0\"\n")
	os.WriteFile("nxd.yaml", customCfg, 0644)

	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("init failed: %v", err)
	}

	// Verify existing config was NOT overwritten
	data, _ := os.ReadFile("nxd.yaml")
	if string(data) != string(customCfg) {
		t.Fatalf("existing nxd.yaml was overwritten: got %s", string(data))
	}

	output := buf.String()
	if !bytes.Contains([]byte(output), []byte("already exists")) {
		t.Fatalf("expected 'already exists' in output, got: %s", output)
	}
}

// chdirTemp switches into a fresh temp dir (restored on cleanup) with HOME
// pointed at it so init never touches the real ~/.nxd.
func chdirTemp(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(orig) })
	t.Setenv("HOME", dir)
	return dir
}

func runInitCmd(t *testing.T, args ...string) (string, error) {
	t.Helper()
	cmd := newInitCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return buf.String(), err
}

// Defect 5(b): --local-state writes a repo-relative state_dir, creates
// ./.nxd and git-ignores it.
func TestRunInit_LocalState(t *testing.T) {
	dir := chdirTemp(t)
	os.WriteFile(".gitignore", []byte("bin/\n"), 0o644)

	out, err := runInitCmd(t, "--local-state")
	if err != nil {
		t.Fatalf("init --local-state: %v\n%s", err, out)
	}
	cfg, _ := os.ReadFile("nxd.yaml")
	if !strings.Contains(string(cfg), "state_dir: .nxd") {
		t.Errorf("nxd.yaml should point state_dir at .nxd:\n%s", cfg)
	}
	if _, err := os.Stat(filepath.Join(dir, ".nxd", "events.jsonl")); err != nil {
		t.Errorf("./.nxd/events.jsonl should exist: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "home-should-not-exist")); err == nil {
		t.Error("unexpected dir")
	}
	gi, _ := os.ReadFile(".gitignore")
	if !strings.Contains(string(gi), "bin/\n") || !strings.Contains(string(gi), ".nxd/\n") {
		t.Errorf(".gitignore should keep existing entries and add .nxd/:\n%s", gi)
	}
	want := "Initialized NXD workspace at " + filepath.Join(dir, ".nxd") + " (state mode: local)"
	if !strings.Contains(out, want) {
		t.Errorf("output should name the local mode:\n%s", out)
	}

	// Re-running keeps the config and does not duplicate the .gitignore entry.
	out, err = runInitCmd(t, "--local-state")
	if err != nil {
		t.Fatal(err)
	}
	gi, _ = os.ReadFile(".gitignore")
	if strings.Count(string(gi), ".nxd/") != 1 {
		t.Errorf(".gitignore entry duplicated:\n%s", gi)
	}
	if !strings.Contains(out, "already exists") || !strings.Contains(out, "(state mode: local)") {
		t.Errorf("second run output:\n%s", out)
	}
}

func TestRunInit_SharedModeReportsHome(t *testing.T) {
	dir := chdirTemp(t)
	out, err := runInitCmd(t)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	want := "Initialized NXD workspace at " + filepath.Join(dir, ".nxd") + " (state mode: "
	if !strings.Contains(out, want) {
		t.Errorf("output:\n%s", out)
	}
	// HOME == cwd here, so ~/.nxd is inside the repo and counts as local; a
	// config pointing elsewhere must report shared.
	if _, err := os.Stat(".gitignore"); err == nil {
		t.Error(".gitignore must not be created without --local-state")
	}
}

func TestRunInit_ExistingConfigStateDirIsHonoured(t *testing.T) {
	dir := chdirTemp(t)
	custom := filepath.Join(dir, "elsewhere", "state")
	os.WriteFile("nxd.yaml", []byte("version: \"1.0\"\nworkspace:\n  state_dir: "+custom+"\n"), 0o644)

	out, err := runInitCmd(t)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(custom, "nxd.db")); err != nil {
		t.Errorf("state should be created where the existing config says: %v", err)
	}
	if !strings.Contains(out, custom+" (state mode: local)") {
		// custom is inside the temp cwd, hence "local".
		t.Errorf("output:\n%s", out)
	}
}

func TestRunInit_StateDirFlagWins(t *testing.T) {
	dir := chdirTemp(t)
	flagDir := filepath.Join(dir, "flag-state")
	t.Cleanup(func() { stateDirOverride = "" })
	stateDirOverride = flagDir

	out, err := runInitCmd(t)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(flagDir, "events.jsonl")); err != nil {
		t.Errorf("--state-dir should win: %v", err)
	}
}

func TestInitStateDir_Modes(t *testing.T) {
	dir := t.TempDir()
	t.Cleanup(func() { stateDirOverride = "" })
	stateDirOverride = ""

	got, mode := initStateDir(filepath.Join(dir, "missing.yaml"), dir, true)
	if got != filepath.Join(dir, ".nxd") || mode != "local" {
		t.Errorf("local: %s %s", got, mode)
	}
	// Unloadable config → default home dir, which is outside dir → shared.
	got, mode = initStateDir(filepath.Join(dir, "missing.yaml"), dir, false)
	if got == "" || mode != "shared" {
		t.Errorf("fallback: %s %s", got, mode)
	}
	// A parent directory is never "local".
	stateDirOverride = filepath.Dir(dir)
	if _, mode = initStateDir("", dir, false); mode != "shared" {
		t.Errorf("parent dir should be shared, got %s", mode)
	}
}

func TestEnsureGitignore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")

	if err := ensureGitignore(path, ".nxd/"); err != nil {
		t.Fatal(err)
	}
	gi, _ := os.ReadFile(path)
	if !strings.HasSuffix(string(gi), ".nxd/\n") {
		t.Errorf("created: %q", gi)
	}

	// Existing equivalent spellings are recognised.
	for _, existing := range []string{".nxd/", ".nxd", "/.nxd/", "/.nxd"} {
		os.WriteFile(path, []byte("x\n"+existing+"\n"), 0o644)
		if err := ensureGitignore(path, ".nxd/"); err != nil {
			t.Fatal(err)
		}
		gi, _ = os.ReadFile(path)
		if string(gi) != "x\n"+existing+"\n" {
			t.Errorf("%q should be recognised, file became %q", existing, gi)
		}
	}

	// Missing trailing newline is handled.
	os.WriteFile(path, []byte("bin"), 0o644)
	if err := ensureGitignore(path, ".nxd/"); err != nil {
		t.Fatal(err)
	}
	gi, _ = os.ReadFile(path)
	if !strings.HasPrefix(string(gi), "bin\n#") {
		t.Errorf("newline not inserted: %q", gi)
	}

	// Unreadable path errors.
	if err := ensureGitignore(filepath.Join(dir, "no", "such", ".gitignore"), ".nxd/"); err == nil {
		t.Error("expected write error")
	}
}

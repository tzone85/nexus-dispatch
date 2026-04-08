package test

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// FixtureConfig describes the initial state of a throwaway test repo.
type FixtureConfig struct {
	ModuleName string
	Files      map[string]string
}

// CreateFixtureRepo creates a temporary git repo from a FixtureConfig.
func CreateFixtureRepo(t *testing.T, cfg FixtureConfig) string {
	t.Helper()
	dir := t.TempDir()
	moduleName := cfg.ModuleName
	if moduleName == "" {
		moduleName = "testproject"
	}

	run(t, dir, "go", "mod", "init", moduleName)

	templatePath := filepath.Join("testdata", "fixture", "main.go")
	templateContent, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatalf("read fixture template: %v", err)
	}
	writeFile(t, dir, "main.go", string(templateContent))

	for path, content := range cfg.Files {
		writeFile(t, dir, path, content)
	}

	run(t, dir, "git", "init")
	run(t, dir, "git", "add", "-A")
	run(t, dir, "git", "commit", "-m", "initial commit")

	return dir
}

// TestStores bundles event and projection stores for testing.
type TestStores struct {
	Events   state.EventStore
	Proj     *state.SQLiteStore
	StateDir string
}

// CreateTestStores creates temporary FileStore + SQLiteStore.
func CreateTestStores(t *testing.T) TestStores {
	t.Helper()
	dir := t.TempDir()
	eventsPath := filepath.Join(dir, "events.jsonl")
	dbPath := filepath.Join(dir, "nxd.db")

	es, err := state.NewFileStore(eventsPath)
	if err != nil {
		t.Fatalf("create event store: %v", err)
	}
	t.Cleanup(func() { es.Close() })

	ps, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatalf("create projection store: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return TestStores{Events: es, Proj: ps, StateDir: dir}
}

type ConfigOption func(*config.Config)

func WithProvider(p string) ConfigOption {
	return func(c *config.Config) {
		for _, mc := range []*config.ModelConfig{
			&c.Models.TechLead, &c.Models.Senior, &c.Models.Intermediate,
			&c.Models.Junior, &c.Models.QA, &c.Models.Supervisor, &c.Models.Manager,
		} {
			mc.Provider = p
		}
	}
}

func WithModel(m string) ConfigOption {
	return func(c *config.Config) {
		for _, mc := range []*config.ModelConfig{
			&c.Models.TechLead, &c.Models.Senior, &c.Models.Intermediate,
			&c.Models.Junior, &c.Models.QA, &c.Models.Supervisor, &c.Models.Manager,
		} {
			mc.Model = m
		}
	}
}

func WithMergeMode(mode string) ConfigOption {
	return func(c *config.Config) { c.Merge.Mode = mode }
}

func NewTestConfig(stateDir string, opts ...ConfigOption) config.Config {
	cfg := config.DefaultConfig()
	cfg.Workspace.StateDir = stateDir
	cfg.Merge.Mode = "local"
	cfg.Merge.AutoMerge = true
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func RequireOllama(t *testing.T) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		t.Skip("Ollama not running, skipping live test")
	}
	resp.Body.Close()
}

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v failed: %v\n%s", name, args, err, out)
	}
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// --- Self-tests for helpers ---

func TestCreateFixtureRepo(t *testing.T) {
	repo := CreateFixtureRepo(t, FixtureConfig{})

	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		t.Fatal("expected .git directory")
	}
	if _, err := os.Stat(filepath.Join(repo, "go.mod")); err != nil {
		t.Fatal("expected go.mod")
	}
	if _, err := os.Stat(filepath.Join(repo, "main.go")); err != nil {
		t.Fatal("expected main.go")
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = repo
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fixture repo doesn't build: %v\n%s", err, out)
	}
}

func TestCreateTestStores(t *testing.T) {
	stores := CreateTestStores(t)
	if stores.Events == nil {
		t.Fatal("expected non-nil event store")
	}
	if stores.Proj == nil {
		t.Fatal("expected non-nil projection store")
	}
	if stores.StateDir == "" {
		t.Fatal("expected non-empty state dir")
	}
}

package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// --- newDevDBProvider ---

func TestNewDevDBProvider_NullProvider(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "null"

	p, err := newDevDBProvider(cfg)
	if err != nil {
		t.Fatalf("expected nil error for null provider, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil null provider")
	}
}

func TestNewDevDBProvider_EmptyProvider(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = ""

	p, err := newDevDBProvider(cfg)
	if err != nil {
		t.Fatalf("expected nil error for empty provider, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil null provider as default")
	}
}

func TestNewDevDBProvider_Docker(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "docker"
	cfg.DevDB.Docker.Image = "postgres:16"
	cfg.DevDB.Docker.ContainerName = "nxd-pg"

	p, err := newDevDBProvider(cfg)
	if err != nil {
		t.Fatalf("expected nil error for docker provider, got: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil docker provider")
	}
}

func TestNewDevDBProvider_Unsupported(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "ghost"

	_, err := newDevDBProvider(cfg)
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Errorf("expected 'not supported' in error, got: %v", err)
	}
}

// --- newDevDBLifecycle ---

func TestNewDevDBLifecycle_NullDisabled(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "null"

	lc := newDevDBLifecycle(cfg, nil, nil)
	if lc != nil {
		t.Error("expected nil lifecycle for null provider")
	}
}

func TestNewDevDBLifecycle_EmptyDisabled(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = ""

	lc := newDevDBLifecycle(cfg, nil, nil)
	if lc != nil {
		t.Error("expected nil lifecycle when devdb section absent")
	}
}

func TestNewDevDBLifecycle_DockerReturnsNonNil(t *testing.T) {
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer es.Close()

	cfg := config.Config{}
	cfg.DevDB.Provider = "docker"
	cfg.DevDB.Docker.Image = "postgres:16"

	lc := newDevDBLifecycle(cfg, es, nil)
	if lc == nil {
		t.Fatal("expected non-nil lifecycle for docker provider")
	}
}

func TestNewDevDBLifecycle_UnsupportedReturnsNil(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "ghost"

	lc := newDevDBLifecycle(cfg, nil, nil)
	if lc != nil {
		t.Error("expected nil lifecycle on unsupported provider (graceful degrade)")
	}
}

// --- runDevDBOrphanRecovery ---

func TestRunDevDBOrphanRecovery_NullProviderSilent(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = "null"

	var buf bytes.Buffer
	runDevDBOrphanRecovery(&buf, cfg, nil)

	if buf.Len() != 0 {
		t.Errorf("expected silent skip for null provider, got: %q", buf.String())
	}
}

func TestRunDevDBOrphanRecovery_EmptyProviderSilent(t *testing.T) {
	cfg := config.Config{}
	cfg.DevDB.Provider = ""

	var buf bytes.Buffer
	runDevDBOrphanRecovery(&buf, cfg, nil)

	if buf.Len() != 0 {
		t.Errorf("expected silent skip when devdb absent, got: %q", buf.String())
	}
}

func TestRunDevDBOrphanRecovery_UnreachableProviderMessages(t *testing.T) {
	// Provider configured but daemon unreachable — should print a "skipping" or
	// "skipped" message rather than panic. DOCKER_HOST overrides the daemon
	// dial target; Docker.Host is the Postgres DSN host and unrelated here.
	t.Setenv("DOCKER_HOST", "unix:///nonexistent-nxd-test.sock")

	cfg := config.Config{}
	cfg.DevDB.Provider = "docker"
	cfg.DevDB.Docker.Image = "postgres:16"

	var buf bytes.Buffer
	runDevDBOrphanRecovery(&buf, cfg, []state.Story{
		{ID: "s1", Status: "in_progress"},
		{ID: "s2", Status: "merged"},
	})

	out := buf.String()
	if !strings.Contains(out, "DevDB recovery") {
		t.Errorf("expected 'DevDB recovery' in output, got: %q", out)
	}
}

// --- dirExists ---

func TestDirExists_TrueForDir(t *testing.T) {
	dir := t.TempDir()
	if !dirExists(dir) {
		t.Error("dirExists returned false for existing dir")
	}
}

func TestDirExists_FalseForFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	if dirExists(path) {
		t.Error("dirExists returned true for a file")
	}
}

func TestDirExists_FalseForMissing(t *testing.T) {
	if dirExists("/does/not/exist/nxd-test-path-xyz") {
		t.Error("dirExists returned true for missing path")
	}
}

// --- ghOpsAdapter (interface conformance, not network) ---

func TestGhOpsAdapter_ImplementsInterface(t *testing.T) {
	var a any = &ghOpsAdapter{}
	if _, ok := a.(interface {
		PushBranch(string, string) error
	}); !ok {
		t.Error("ghOpsAdapter does not implement PushBranch")
	}
	if _, ok := a.(interface {
		MergePR(string, int) error
	}); !ok {
		t.Error("ghOpsAdapter does not implement MergePR")
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
)

// Defect 7: `nxd req --background` forked `nxd resume` while still holding
// the pipeline lock, so the child always saw a live holder and refused. The
// lock must be released before child.Start(), and req must take the lock
// before opening stores (defect 4c). Source-scan wiring test, like the
// resume ones.
func TestReq_ReleasesLockBeforeDaemonizing(t *testing.T) {
	src, err := os.ReadFile("req.go")
	if err != nil {
		t.Fatal(err)
	}
	code := string(src)
	if !strings.Contains(code, "loadStoresLocked(cfgPath)") {
		t.Error("req.go must acquire the pipeline lock via loadStoresLocked before opening stores")
	}
	release := strings.Index(code, "lock.Release(); err != nil")
	start := strings.Index(code, "child.Start()")
	if release < 0 || start < 0 || release > start {
		t.Errorf("req.go must release the pipeline lock before child.Start() (release at %d, start at %d)", release, start)
	}
}

// req fails fast when another pipeline holds the lock.
func TestRunReq_FailsWhenPipelineRunning(t *testing.T) {
	env := setupTestEnv(t)
	holder, err := engine.AcquireLock(filepath.Join(env.Dir, ".nxd"))
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()

	_, err = execCmd(t, newReqCmd(), env.Config, "do a thing")
	if err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected lock error, got %v", err)
	}
}

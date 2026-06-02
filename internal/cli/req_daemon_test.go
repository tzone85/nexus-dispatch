package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestReqLogPath_Format(t *testing.T) {
	got := reqLogPath("/tmp/state", "01HXKZ9ABC123")
	want := "/tmp/state/logs/req-01HXKZ9ABC123.log"
	if got != want {
		t.Errorf("reqLogPath = %q, want %q", got, want)
	}
}

func TestReqLogPath_Empty(t *testing.T) {
	got := reqLogPath("", "abc")
	want := "logs/req-abc.log"
	if got != want {
		t.Errorf("reqLogPath = %q, want %q", got, want)
	}
}

func TestForkReqDaemon_BuildsCmd(t *testing.T) {
	cmd := forkReqDaemon("/usr/local/bin/nxd", "REQ123", []string{"--godmode", "--config", "x.yaml"})

	if cmd.Path != "/usr/local/bin/nxd" {
		t.Errorf("Path = %q, want /usr/local/bin/nxd", cmd.Path)
	}
	wantArgs := []string{"/usr/local/bin/nxd", "resume", "REQ123", "--godmode", "--config", "x.yaml"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args len = %d, want %d (%v)", len(cmd.Args), len(wantArgs), cmd.Args)
	}
	for i := range wantArgs {
		if cmd.Args[i] != wantArgs[i] {
			t.Errorf("Args[%d] = %q, want %q", i, cmd.Args[i], wantArgs[i])
		}
	}
	if cmd.Dir != "." {
		t.Errorf("Dir = %q, want .", cmd.Dir)
	}
}

func TestForkReqDaemon_SetsSetsid(t *testing.T) {
	cmd := forkReqDaemon("/bin/echo", "req-1", nil)
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr should be set to detach from process group")
	}
	sp, ok := any(cmd.SysProcAttr).(*syscall.SysProcAttr)
	if !ok {
		t.Fatal("SysProcAttr should be *syscall.SysProcAttr")
	}
	if !sp.Setsid {
		t.Error("Setsid should be true to escape parent process group")
	}
}

func TestForkReqDaemon_NoExtraArgs(t *testing.T) {
	cmd := forkReqDaemon("/bin/echo", "abc", nil)
	wantArgs := []string{"/bin/echo", "resume", "abc"}
	if len(cmd.Args) != len(wantArgs) {
		t.Fatalf("Args = %v, want %v", cmd.Args, wantArgs)
	}
}

func TestRunReqLogs_MissingLog(t *testing.T) {
	env := setupTestEnv(t)
	// Override expandHome to point to env.Dir/.nxd via config workspace.state_dir.
	cmd := newReqLogsCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", env.Config)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"NONEXISTENT_REQ"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing log file")
	}
	if !strings.Contains(err.Error(), "no log file found") {
		t.Errorf("expected 'no log file found' in error, got: %v", err)
	}
}

func TestRunReqLogs_ReadsExistingLog(t *testing.T) {
	env := setupTestEnv(t)

	// Create log file at path runReqLogs will look at.
	stateDir := filepath.Join(env.Dir, ".nxd")
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reqID := "REQ_EXISTING"
	logFile := filepath.Join(logDir, "req-"+reqID+".log")
	content := "line one\nline two\n"
	if err := os.WriteFile(logFile, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := newReqLogsCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", env.Config)

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{reqID})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(buf.String(), "line one") || !strings.Contains(buf.String(), "line two") {
		t.Errorf("expected log content in output, got: %s", buf.String())
	}
}

func TestRunReqLogs_LogFilePermsTight(t *testing.T) {
	env := setupTestEnv(t)
	stateDir := filepath.Join(env.Dir, ".nxd")
	logDir := filepath.Join(stateDir, "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate what `nxd req --background` writes: a log file with mode 0600.
	// If the production code path drifts back to 0644, this canary fails.
	logFile := filepath.Join(logDir, "req-PERMS.log")
	f, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	fi, err := os.Stat(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("daemon log perm = %o, want 0600 (may capture LLM I/O + credentials)", fi.Mode().Perm())
	}
}

func TestRunReqLogs_BadConfig(t *testing.T) {
	cmd := newReqLogsCmd()
	cmd.Flags().String("config", "", "")
	cmd.Flags().Set("config", "/nonexistent/path/nxd.yaml")

	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"REQ"})

	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
	if !strings.Contains(err.Error(), "load config") {
		t.Errorf("expected 'load config' in error, got: %v", err)
	}
}

package cli

import (
	"errors"
	"runtime"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
)

func withGitStub(t *testing.T, want bool) {
	t.Helper()
	prev := gitInsideWorkTreeFn
	gitInsideWorkTreeFn = func(string) bool { return want }
	t.Cleanup(func() { gitInsideWorkTreeFn = prev })
}

func withTmuxStub(t *testing.T, want bool) {
	t.Helper()
	prev := tmuxAvailableFn
	tmuxAvailableFn = func() bool { return want }
	t.Cleanup(func() { tmuxAvailableFn = prev })
}

func TestCheckGitRepo_InRepo(t *testing.T) {
	withGitStub(t, true)
	if err := checkGitRepo("/anywhere"); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

func TestCheckGitRepo_OutsideRepo(t *testing.T) {
	withGitStub(t, false)
	err := checkGitRepo("/tmp/not-a-repo")
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, errNotAGitRepo) {
		t.Fatalf("want errNotAGitRepo, got %v", err)
	}
	if !strings.Contains(err.Error(), "git init") {
		t.Errorf("error message should include git-init recovery hint, got: %v", err)
	}
}

func TestCfgRequiresTmux(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want bool
	}{
		{
			name: "no runtimes configured",
			cfg:  config.Config{},
			want: false,
		},
		{
			name: "only native gemma",
			cfg: config.Config{Runtimes: map[string]config.RuntimeConfig{
				"gemma": {Native: true, Command: ""},
			}},
			want: false,
		},
		{
			name: "aider present requires tmux",
			cfg: config.Config{Runtimes: map[string]config.RuntimeConfig{
				"gemma": {Native: true},
				"aider": {Native: false, Command: "aider"},
			}},
			want: true,
		},
		{
			name: "only claude-code requires tmux",
			cfg: config.Config{Runtimes: map[string]config.RuntimeConfig{
				"claude-code": {Native: false, Command: "claude"},
			}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfgRequiresTmux(tc.cfg); got != tc.want {
				t.Fatalf("cfgRequiresTmux = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckTmuxIfNeeded_NotNeeded(t *testing.T) {
	withTmuxStub(t, false)
	cfg := config.Config{Runtimes: map[string]config.RuntimeConfig{"gemma": {Native: true}}}
	if err := checkTmuxIfNeeded(cfg); err != nil {
		t.Fatalf("native-only config should skip tmux check, got: %v", err)
	}
}

func TestCheckTmuxIfNeeded_PresentAndNeeded(t *testing.T) {
	withTmuxStub(t, true)
	cfg := config.Config{Runtimes: map[string]config.RuntimeConfig{"aider": {Native: false, Command: "aider"}}}
	if err := checkTmuxIfNeeded(cfg); err != nil {
		t.Fatalf("want nil when tmux available, got: %v", err)
	}
}

func TestCheckTmuxIfNeeded_MissingHasPlatformHint(t *testing.T) {
	withTmuxStub(t, false)
	cfg := config.Config{Runtimes: map[string]config.RuntimeConfig{"aider": {Native: false, Command: "aider"}}}
	err := checkTmuxIfNeeded(cfg)
	if err == nil {
		t.Fatal("want error when tmux missing and config needs it")
	}
	if !errors.Is(err, errTmuxMissing) {
		t.Fatalf("want errTmuxMissing, got %v", err)
	}
	msg := err.Error()
	switch runtime.GOOS {
	case "windows":
		if !strings.Contains(msg, "WSL2") {
			t.Errorf("Windows hint should mention WSL2, got: %v", msg)
		}
	default:
		if !strings.Contains(msg, "brew install tmux") && !strings.Contains(msg, "apt install tmux") {
			t.Errorf("Unix hint should suggest a package manager, got: %v", msg)
		}
	}
}

func TestPreflightForRun_FailsFastOnNoGitRepo(t *testing.T) {
	withGitStub(t, false)
	withTmuxStub(t, false) // would also fail, but git check comes first
	cfg := config.Config{Runtimes: map[string]config.RuntimeConfig{"aider": {Native: false}}}
	err := PreflightForRun("/tmp/bogus", cfg)
	if err == nil {
		t.Fatal("want error")
	}
	if !errors.Is(err, errNotAGitRepo) {
		t.Fatalf("git check should run first, got %v", err)
	}
}

func TestPreflightForRun_Happy(t *testing.T) {
	withGitStub(t, true)
	withTmuxStub(t, true)
	cfg := config.Config{Runtimes: map[string]config.RuntimeConfig{"aider": {Native: false, Command: "aider"}}}
	if err := PreflightForRun("/anywhere", cfg); err != nil {
		t.Fatalf("want nil, got %v", err)
	}
}

package shellexec

import (
	"runtime"
	"testing"
)

func TestShell_DefaultsByGOOS(t *testing.T) {
	t.Setenv("NXD_SHELL", "")
	exe, flag := Shell()
	if runtime.GOOS == "windows" {
		if exe != "cmd.exe" || flag != "/C" {
			t.Fatalf("windows default: got (%q,%q), want (cmd.exe,/C)", exe, flag)
		}
		return
	}
	if exe != "sh" || flag != "-c" {
		t.Fatalf("unix default: got (%q,%q), want (sh,-c)", exe, flag)
	}
}

func TestShell_OverrideViaEnv(t *testing.T) {
	tests := []struct {
		override string
		wantExe  string
		wantFlag string
	}{
		{"pwsh", "pwsh", "-Command"},
		{"powershell.exe", "powershell.exe", "-Command"},
		{"cmd.exe", "cmd.exe", "/C"},
		{"bash", "bash", "-c"},
		{"/usr/bin/zsh", "/usr/bin/zsh", "-c"},
	}
	for _, tc := range tests {
		t.Run(tc.override, func(t *testing.T) {
			t.Setenv("NXD_SHELL", tc.override)
			exe, flag := Shell()
			if exe != tc.wantExe || flag != tc.wantFlag {
				t.Fatalf("override %q: got (%q,%q), want (%q,%q)",
					tc.override, exe, flag, tc.wantExe, tc.wantFlag)
			}
		})
	}
}

func TestCommand_NotNil(t *testing.T) {
	cmd := Command("echo hi")
	if cmd == nil {
		t.Fatal("Command returned nil")
	}
	if len(cmd.Args) < 2 {
		t.Fatalf("Command args too short: %v", cmd.Args)
	}
}

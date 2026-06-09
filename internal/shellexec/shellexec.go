// Package shellexec resolves the host shell + flag tuple used to evaluate a
// user-supplied command string (`nxd.yaml` metric/migration commands, the
// native runtime's run_command tool, etc.). On Unix this is "sh -c"; on
// Windows it is "cmd.exe /C". A user override is possible via the NXD_SHELL
// env var ("NXD_SHELL=pwsh" → "pwsh -Command").
package shellexec

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Shell returns the executable + flag pair used to run a user command line.
func Shell() (exe string, flag string) {
	if override := strings.TrimSpace(os.Getenv("NXD_SHELL")); override != "" {
		return override, shellFlag(override)
	}
	if runtime.GOOS == "windows" {
		return "cmd.exe", "/C"
	}
	return "sh", "-c"
}

// shellFlag returns the standard "run this string" flag for a known shell
// override. Unknown shells fall back to "-c".
func shellFlag(exe string) string {
	base := strings.ToLower(filenameBase(exe))
	switch base {
	case "cmd", "cmd.exe":
		return "/C"
	case "pwsh", "pwsh.exe", "powershell", "powershell.exe":
		return "-Command"
	default:
		return "-c"
	}
}

func filenameBase(p string) string {
	if i := strings.LastIndexAny(p, "/\\"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// Command returns an exec.Cmd that runs command via the host shell.
func Command(command string) *exec.Cmd {
	exe, flag := Shell()
	return exec.Command(exe, flag, command)
}

// CommandContext is the context-aware variant of Command.
func CommandContext(ctx context.Context, command string) *exec.Cmd {
	exe, flag := Shell()
	return exec.CommandContext(ctx, exe, flag, command)
}

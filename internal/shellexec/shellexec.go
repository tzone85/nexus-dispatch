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

// sensitiveEnvMarkers are case-folded substrings of environment-variable NAMES
// that identify a credential. Any variable whose name contains one is withheld
// from subprocesses that run agent-authored or LLM-influenced code.
var sensitiveEnvMarkers = []string{
	"API_KEY",
	"APIKEY",
	"SECRET",
	"TOKEN",
	"PASSWORD",
	"PASSWD",
	"CREDENTIAL",
	"PRIVATE_KEY",
}

// IsSensitiveEnvName reports whether an environment variable name looks like a
// credential (e.g. ANTHROPIC_API_KEY, OPENAI_API_KEY, GITHUB_TOKEN). The match
// is case-insensitive and substring-based so vendor prefixes and suffixes are
// all covered.
func IsSensitiveEnvName(name string) bool {
	up := strings.ToUpper(name)
	for _, marker := range sensitiveEnvMarkers {
		if strings.Contains(up, marker) {
			return true
		}
	}
	return false
}

// scrubEnv returns environ with every credential-bearing variable removed. A
// fresh slice is returned; the input is never mutated.
func scrubEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		if IsSensitiveEnvName(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// ScrubbedEnviron returns the process environment (os.Environ()) with
// credential-bearing variables removed. It is the environment NXD hands to
// subprocesses that execute agent-authored or LLM-influenced code — the native
// runtime's run_command tool and the criteria evaluator's go-test / command /
// migration runners. NXD's own process holds secrets such as ANTHROPIC_API_KEY
// for its internal LLM calls; those must never reach code an agent controls
// (the CLI runtime deliberately withholds the same key from spawned agents).
// Build and test tooling needs no credentials, so nothing a legitimate
// `go build` / `go test` / `make` relies on (PATH, HOME, GOCACHE, GOPATH,
// GOPROXY, HTTPS_PROXY, CGO_*, GOFLAGS, …) is affected.
func ScrubbedEnviron() []string {
	return scrubEnv(os.Environ())
}

package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/memory"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system health and dependencies",
		Long:  "Runs preflight checks on all NXD dependencies and configuration. Use before your first run.",
		RunE:  runDoctor,
	}
	cmd.Flags().Bool("json", false, "machine-readable JSON output")
	cmd.SilenceUsage = true
	return cmd
}

type checkResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"` // "ok", "warn", "fail"
	Message string `json:"message"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	asJSON, _ := cmd.Flags().GetBool("json")
	if !asJSON {
		fmt.Fprintln(out, "NXD Doctor — Preflight Check")
		fmt.Fprintln(out, "============================")
		fmt.Fprintln(out)
	}

	var checks []checkResult

	// 1. Go toolchain (only required for Go repos; warn elsewhere)
	cwd, _ := os.Getwd()
	checks = append(checks, checkGoFor(cwd))

	// 2. Git
	checks = append(checks, checkGit())

	// 3. tmux
	checks = append(checks, checkTmux())

	// 4. Config (loaded first so the Ollama check can use models.ollama_host)
	cfgPath, _ := cmd.Flags().GetString("config")
	cfgCheck, cfg := checkConfig(cfgPath)

	// 5. Ollama
	checks = append(checks, checkOllamaAt(ollamaHost(cfg.Models.OllamaHost)))

	// 6. Gemma 4 model
	checks = append(checks, checkGemmaModel())

	checks = append(checks, cfgCheck)

	// 7. State directory
	checks = append(checks, checkStateDir(cfg))

	// 7b. Projection drift — is the SQLite projection watermark behind the log?
	checks = append(checks, checkProjectionDrift(cfg))

	// 8. MemPalace
	checks = append(checks, checkMemPalace())

	// 9. Google AI API key (optional)
	checks = append(checks, checkGoogleAI())

	// 10. Plugins
	checks = append(checks, checkPlugins(cfg))

	// 11. Disk space
	checks = append(checks, checkDiskSpace(cfg))

	// 12. DevDB provider (only when configured)
	checks = append(checks, checkDevDB(cfg))

	// 13. Sandbox: where agent commands run (docker vs. unsandboxed host)
	checks = append(checks, checkSandbox(cfg, func() bool { return exec.Command("docker", "info").Run() == nil }))

	// Print results
	okCount, warnCount, failCount := 0, 0, 0
	for _, c := range checks {
		icon := "✓"
		switch c.Status {
		case "ok":
			icon = "✓"
			okCount++
		case "warn":
			icon = "⚠"
			warnCount++
		case "fail":
			icon = "✗"
			failCount++
		}
		if !asJSON {
			fmt.Fprintf(out, "  %s %-25s %s\n", icon, c.Name, c.Message)
		}
	}

	if asJSON {
		if err := writeJSON(out, map[string]any{
			"checks": checks, "passed": okCount, "warnings": warnCount, "failed": failCount,
		}); err != nil {
			return err
		}
		if failCount > 0 {
			return fmt.Errorf("%d preflight checks failed", failCount)
		}
		return nil
	}

	fmt.Fprintln(out)
	fmt.Fprintf(out, "Results: %d passed, %d warnings, %d failed\n", okCount, warnCount, failCount)

	if failCount > 0 {
		fmt.Fprintln(out, "\nFix the failed checks before running 'nxd req'.")
		return fmt.Errorf("%d preflight checks failed", failCount)
	}
	if warnCount > 0 {
		fmt.Fprintln(out, "\nWarnings are non-blocking but may affect functionality.")
	} else {
		fmt.Fprintln(out, "\nAll checks passed! NXD is ready to go.")
	}
	return nil
}

func checkGo() checkResult { return checkGoFor("") }

// checkGoFor probes the Go toolchain. Missing Go is a hard failure only when
// dir looks like a Go repository (go.mod present); NXD orchestrates any
// language, so a Swift or Node repo without Go gets a warning, not a block.
func checkGoFor(dir string) checkResult {
	cmd := exec.Command("go", "version")
	out, err := cmd.Output()
	if err != nil {
		if isGoRepo(dir) {
			return checkResult{"Go", "fail", "Go not found but go.mod is present. Install from https://go.dev/dl/"}
		}
		return checkResult{"Go", "warn", "Go not found (only needed for Go repositories). Install from https://go.dev/dl/"}
	}
	version := strings.TrimSpace(string(out))
	return checkResult{"Go", "ok", version}
}

// isGoRepo reports whether dir contains a go.mod. Empty dir means unknown →
// treated as a Go repo to preserve the strict legacy behaviour.
func isGoRepo(dir string) bool {
	if dir == "" {
		return true
	}
	_, err := os.Stat(filepath.Join(dir, "go.mod"))
	return err == nil
}

func checkGit() checkResult {
	cmd := exec.Command("git", "version")
	out, err := cmd.Output()
	if err != nil {
		return checkResult{"Git", "fail", "Git not found. Install git."}
	}
	return checkResult{"Git", "ok", strings.TrimSpace(string(out))}
}

func checkTmux() checkResult {
	cmd := exec.Command("tmux", "-V")
	out, err := cmd.Output()
	if err != nil {
		msg := "tmux not found. Required for agent execution. Install: brew install tmux"
		if runtime.GOOS == "windows" {
			msg = "tmux is not available on native Windows. The agent execution pipeline requires tmux; " +
				"run NXD inside WSL2 (Ubuntu) where you can `sudo apt install tmux`. Read-only commands " +
				"(status, dashboard, metrics, report, projects, config) still work on native Windows."
		}
		return checkResult{"tmux", "warn", msg}
	}
	return checkResult{"tmux", "ok", strings.TrimSpace(string(out))}
}

// defaultOllamaHost is used when neither OLLAMA_HOST nor models.ollama_host
// is set.
const defaultOllamaHost = "http://localhost:11434"

// ollamaHost resolves the Ollama base URL: OLLAMA_HOST env wins, then the
// configured models.ollama_host, then localhost. A bare "host:port" (or
// "host") gets an http:// scheme; a trailing slash is trimmed.
func ollamaHost(configured string) string {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = configured
	}
	if host == "" {
		host = defaultOllamaHost
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return strings.TrimRight(host, "/")
}

func checkOllamaRunning() checkResult { return checkOllamaAt(ollamaHost("")) }

// checkOllamaAt probes the Ollama tags endpoint at baseURL.
func checkOllamaAt(baseURL string) checkResult {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(baseURL + "/api/tags")
	if err != nil {
		hint := "Start with: ollama serve"
		if baseURL != defaultOllamaHost {
			hint = "Check OLLAMA_HOST / models.ollama_host and that the remote server is reachable"
		}
		return checkResult{"Ollama", "fail", fmt.Sprintf("Ollama not reachable at %s. %s", baseURL, hint)}
	}
	resp.Body.Close()
	return checkResult{"Ollama", "ok", "running on " + strings.TrimPrefix(strings.TrimPrefix(baseURL, "http://"), "https://")}
}

func checkGemmaModel() checkResult {
	cmd := exec.Command("ollama", "list")
	out, err := cmd.Output()
	return parseGemmaModelStatus(string(out), err)
}

// parseGemmaModelStatus interprets `ollama list` output. Canonical model is
// gemma4:e4b (matches config.DefaultYAML and the README quick start). The 26B
// MoE variant is accepted but no longer preferred — older copies of the doctor
// suggested it, which mismatched the rest of the project.
func parseGemmaModelStatus(output string, listErr error) checkResult {
	const name = "Gemma 4 model"
	if listErr != nil {
		return checkResult{name, "warn", "Could not list Ollama models"}
	}
	switch {
	case strings.Contains(output, "gemma4:e4b"):
		return checkResult{name, "ok", "gemma4:e4b is pulled"}
	case strings.Contains(output, "gemma4:26b"):
		return checkResult{name, "ok", "gemma4:26b is pulled"}
	case strings.Contains(output, "gemma4"):
		return checkResult{name, "ok", "gemma4 variant found"}
	default:
		return checkResult{name, "warn", "gemma4:e4b not found. Run: ollama pull gemma4:e4b"}
	}
}

func checkConfig(cfgPath string) (checkResult, config.Config) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return checkResult{"Config", "warn", fmt.Sprintf("No config found (%v). Run: nxd init", err)}, config.Config{}
	}
	if err := cfg.Validate(); err != nil {
		return checkResult{"Config", "fail", fmt.Sprintf("Invalid config: %v", err)}, cfg
	}
	return checkResult{"Config", "ok", "nxd.yaml valid"}, cfg
}

func checkStateDir(cfg config.Config) checkResult {
	stateDir := resolveStateDir(cfg)
	if stateDir == "" {
		return checkResult{"State directory", "warn", "Could not determine state directory"}
	}

	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		return checkResult{"State directory", "warn", fmt.Sprintf("%s not found. Run: nxd init", stateDir)}
	}

	eventsOK := fileExistsAt(filepath.Join(stateDir, "events.jsonl"))
	dbOK := fileExistsAt(filepath.Join(stateDir, "nxd.db"))

	if eventsOK && dbOK {
		return checkResult{"State directory", "ok", shortPath(stateDir) + " with event store + projection store"}
	}
	return checkResult{"State directory", "warn", shortPath(stateDir) + " exists but stores missing. Run: nxd init"}
}

func checkMemPalace() checkResult {
	mp := memory.NewMemPalace()
	if mp.IsAvailable() {
		return checkResult{"MemPalace", "ok", "installed and available"}
	}
	return checkResult{"MemPalace", "warn", "Not available. Install: pip install mempalace (optional)"}
}

func checkGoogleAI() checkResult {
	key := os.Getenv("GOOGLE_AI_API_KEY")
	if key != "" {
		return checkResult{"Google AI API", "ok", "GOOGLE_AI_API_KEY set (free tier fallback enabled)"}
	}
	return checkResult{"Google AI API", "warn", "GOOGLE_AI_API_KEY not set. Ollama-only mode (this is fine)."}
}

func checkPlugins(cfg config.Config) checkResult {
	stateDir := resolveStateDir(cfg)
	if stateDir == "" {
		return checkResult{"Plugins", "ok", "no plugins configured"}
	}

	pluginDir := filepath.Join(stateDir, "plugins")
	if _, err := os.Stat(pluginDir); os.IsNotExist(err) {
		return checkResult{"Plugins", "ok", "no plugin directory (none configured)"}
	}
	return checkResult{"Plugins", "ok", fmt.Sprintf("plugin directory exists: %s", pluginDir)}
}

func checkDiskSpace(cfg config.Config) checkResult {
	stateDir := resolveStateDir(cfg)
	if stateDir == "" {
		// Fall back to default
		home, err := os.UserHomeDir()
		if err != nil {
			return checkResult{"Disk/permissions", "warn", "Could not determine home directory"}
		}
		stateDir = filepath.Join(home, ".nxd")
	}

	// Ensure the directory exists before testing write
	if _, err := os.Stat(stateDir); os.IsNotExist(err) {
		return checkResult{"Disk/permissions", "warn", fmt.Sprintf("%s does not exist yet", shortPath(stateDir))}
	}

	tmpFile := filepath.Join(stateDir, ".doctor-check")
	if err := os.WriteFile(tmpFile, []byte("ok"), 0644); err != nil {
		if os.IsPermission(err) {
			return checkResult{"Disk/permissions", "fail", fmt.Sprintf("Cannot write to %s: permission denied", shortPath(stateDir))}
		}
		return checkResult{"Disk/permissions", "warn", fmt.Sprintf("Write check failed: %v", err)}
	}
	_ = os.Remove(tmpFile)
	return checkResult{"Disk/permissions", "ok", shortPath(stateDir) + " is writable"}
}

// resolveStateDir returns the expanded state directory from config, or falls
// back to ~/.nxd if the config has no state dir set.
func resolveStateDir(cfg config.Config) string {
	if cfg.Workspace.StateDir != "" {
		return expandHome(cfg.Workspace.StateDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".nxd")
}

// checkProjectionDrift reports whether the SQLite projection's reconciliation
// watermark has fallen behind the event-log length — the durable desync
// signature that loadStores rebuilds from on the next command. It reads both
// stores without mutating them: it opens nothing that does not already exist
// (so it never materialises empty stores as a side effect) and never triggers
// a rebuild, so the number it reports is the drift a user would hit before any
// command auto-heals it.
func checkProjectionDrift(cfg config.Config) checkResult {
	const name = "Projection drift"

	stateDir := resolveStateDir(cfg)
	if stateDir == "" {
		return checkResult{name, "ok", "no state directory configured (nothing to check)"}
	}

	eventsPath := filepath.Join(stateDir, "events.jsonl")
	dbPath := filepath.Join(stateDir, "nxd.db")
	// Step aside when the stores are not initialised — the State-directory
	// check already owns the "run nxd init" guidance, and opening the stores
	// here would create empty ones as a side effect.
	if !fileExistsAt(eventsPath) || !fileExistsAt(dbPath) {
		return checkResult{name, "ok", "no projection yet (stores not initialised)"}
	}

	es, err := state.NewFileStore(eventsPath)
	if err != nil {
		return checkResult{name, "warn", fmt.Sprintf("could not open event log: %v", err)}
	}
	defer es.Close()

	ps, err := state.NewSQLiteStore(dbPath)
	if err != nil {
		return checkResult{name, "warn", fmt.Sprintf("could not open projection store: %v", err)}
	}
	defer ps.Close()

	logCount, err := es.Count(state.EventFilter{})
	if err != nil {
		return checkResult{name, "warn", fmt.Sprintf("could not count events: %v", err)}
	}
	applied, err := ps.AppliedEventCount()
	if err != nil {
		return checkResult{name, "warn", fmt.Sprintf("could not read projection watermark: %v", err)}
	}

	if applied < logCount {
		return checkResult{name, "warn", fmt.Sprintf(
			"projection is %d event(s) behind the log (applied %d of %d); it rebuilds automatically on the next command",
			logCount-applied, applied, logCount,
		)}
	}
	return checkResult{name, "ok", fmt.Sprintf("in sync with the event log (%d events)", logCount)}
}

// checkDevDB reports the configured devdb provider's reachability.
// Returns "ok" with a "not configured" message when devdb is disabled
// (provider unset or "null") — the absence is intentional, not a failure.
func checkDevDB(cfg config.Config) checkResult {
	provider := cfg.DevDB.Provider
	if provider == "" || provider == "null" {
		return checkResult{"DevDB", "ok", "not configured (devdb.provider is null or unset)"}
	}
	p, err := newDevDBProvider(cfg)
	if err != nil {
		return checkResult{"DevDB", "fail", fmt.Sprintf("provider %q not supported: %v", provider, err)}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Ping(ctx); err != nil {
		return checkResult{"DevDB", "fail", fmt.Sprintf("%s provider unreachable: %v", provider, err)}
	}
	return checkResult{"DevDB", "ok", fmt.Sprintf("%s provider reachable", provider)}
}

// fileExistsAt reports whether a file exists at the given path.
func fileExistsAt(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// shortPath replaces the user's home directory prefix with ~ for display.
func shortPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}
	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

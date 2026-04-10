package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/memory"
)

func newDoctorCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Check system health and dependencies",
		Long:  "Runs preflight checks on all NXD dependencies and configuration. Use before your first run.",
		RunE:  runDoctor,
	}
	cmd.SilenceUsage = true
	return cmd
}

type checkResult struct {
	Name    string
	Status  string // "ok", "warn", "fail"
	Message string
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	fmt.Fprintln(out, "NXD Doctor — Preflight Check")
	fmt.Fprintln(out, "============================")
	fmt.Fprintln(out)

	var checks []checkResult

	// 1. Go version
	checks = append(checks, checkGo())

	// 2. Git
	checks = append(checks, checkGit())

	// 3. tmux
	checks = append(checks, checkTmux())

	// 4. Ollama
	checks = append(checks, checkOllamaRunning())

	// 5. Gemma 4 model
	checks = append(checks, checkGemmaModel())

	// 6. Config
	cfgPath, _ := cmd.Flags().GetString("config")
	cfgCheck, cfg := checkConfig(cfgPath)
	checks = append(checks, cfgCheck)

	// 7. State directory
	checks = append(checks, checkStateDir(cfg))

	// 8. MemPalace
	checks = append(checks, checkMemPalace())

	// 9. Google AI API key (optional)
	checks = append(checks, checkGoogleAI())

	// 10. Plugins
	checks = append(checks, checkPlugins(cfg))

	// 11. Disk space
	checks = append(checks, checkDiskSpace(cfg))

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
		fmt.Fprintf(out, "  %s %-25s %s\n", icon, c.Name, c.Message)
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

func checkGo() checkResult {
	cmd := exec.Command("go", "version")
	out, err := cmd.Output()
	if err != nil {
		return checkResult{"Go", "fail", "Go not found. Install from https://go.dev/dl/"}
	}
	version := strings.TrimSpace(string(out))
	return checkResult{"Go", "ok", version}
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
		return checkResult{"tmux", "warn", "tmux not found. Required for agent execution. Install: brew install tmux"}
	}
	return checkResult{"tmux", "ok", strings.TrimSpace(string(out))}
}

func checkOllamaRunning() checkResult {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:11434/api/tags")
	if err != nil {
		return checkResult{"Ollama", "fail", "Ollama not running. Start with: ollama serve"}
	}
	resp.Body.Close()
	return checkResult{"Ollama", "ok", "running on localhost:11434"}
}

func checkGemmaModel() checkResult {
	cmd := exec.Command("ollama", "list")
	out, err := cmd.Output()
	if err != nil {
		return checkResult{"Gemma 4 model", "warn", "Could not list Ollama models"}
	}
	output := string(out)
	if strings.Contains(output, "gemma4:26b") {
		return checkResult{"Gemma 4 model", "ok", "gemma4:26b is pulled"}
	}
	if strings.Contains(output, "gemma4") {
		return checkResult{"Gemma 4 model", "ok", "gemma4 variant found"}
	}
	return checkResult{"Gemma 4 model", "warn", "gemma4:26b not found. Run: ollama pull gemma4:26b"}
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
	os.Remove(tmpFile)
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

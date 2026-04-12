package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/repolearn"
)

func newLearnCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "learn [repo-path]",
		Short: "Analyse a repository and build a persistent profile",
		Long: `Runs iterative analysis on a repository to build a RepoProfile that agents
consume at dispatch time. The analysis has three passes:

  Pass 1 — Static scan: marker files, configs, directory tree (no git, no LLM)
  Pass 2 — Git history: commit patterns, contributors, churn hotspots
  Pass 3 — Deep analysis: LLM-assisted summary (requires configured model)

By default, all passes that haven't been completed are run. Use --force to
re-run all passes, or --pass to run a specific pass.`,
		Args: cobra.MaximumNArgs(1),
		RunE: runLearn,
	}
	cmd.Flags().Bool("force", false, "Re-run all passes even if previously completed")
	cmd.Flags().Int("pass", 0, "Run a specific pass (1, 2, or 3). 0 means all pending.")
	cmd.Flags().Bool("json", false, "Output the profile as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func runLearn(cmd *cobra.Command, args []string) error {
	force, _ := cmd.Flags().GetBool("force")
	specificPass, _ := cmd.Flags().GetInt("pass")
	jsonOutput, _ := cmd.Flags().GetBool("json")

	// Determine repo path
	repoPath := ""
	if len(args) > 0 {
		repoPath = args[0]
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("get working directory: %w", err)
		}
		repoPath = cwd
	}

	// Make absolute
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return fmt.Errorf("resolve path: %w", err)
	}
	repoPath = absPath

	// Load config to find state dir
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	profileDir := expandHome(cfg.Workspace.StateDir)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return fmt.Errorf("create state dir: %w", err)
	}

	// Load existing profile or start fresh
	profile, err := repolearn.LoadProfile(profileDir)
	if err != nil {
		return fmt.Errorf("load profile: %w", err)
	}
	profile.RepoPath = repoPath

	out := cmd.OutOrStdout()

	// Determine which passes to run
	shouldRun := func(pass int) bool {
		if specificPass > 0 {
			return pass == specificPass
		}
		if force {
			return true
		}
		return !profile.PassCompleted(pass)
	}

	// Pass 1: Static scan
	if shouldRun(1) {
		fmt.Fprintf(out, "Pass 1: Static scan of %s...\n", repoPath)
		scanned, scanErr := repolearn.ScanStatic(repoPath)
		if scanErr != nil {
			return fmt.Errorf("pass 1 failed: %w", scanErr)
		}
		mergeStaticIntoProfile(profile, scanned)
		fmt.Fprintf(out, "  Language: %s (%s)\n", profile.TechStack.PrimaryLanguage, profile.TechStack.PrimaryBuildTool)
		if profile.Build.BuildCommand != "" {
			fmt.Fprintf(out, "  Build: %s\n", profile.Build.BuildCommand)
		}
		if profile.Test.TestCommand != "" {
			fmt.Fprintf(out, "  Test: %s\n", profile.Test.TestCommand)
		}
		if profile.CI.System != "" {
			fmt.Fprintf(out, "  CI: %s\n", profile.CI.System)
		}
		fmt.Fprintf(out, "  Files: %d total, %d source\n", profile.Structure.TotalFiles, profile.Structure.SourceFiles)
	}

	// Pass 2: Git history
	if shouldRun(2) {
		fmt.Fprintf(out, "Pass 2: Git history analysis...\n")
		if histErr := repolearn.ScanHistory(profile); histErr != nil {
			fmt.Fprintf(out, "  ⚠ Skipped (not a git repo or no history)\n")
		} else {
			fmt.Fprintf(out, "  Commits: %d, Contributors: %d\n", profile.Conventions.CommitCount, profile.Conventions.ContributorCount)
			fmt.Fprintf(out, "  Commit style: %s\n", profile.Conventions.CommitFormat)
			if len(profile.Conventions.ChurnHotspots) > 0 {
				fmt.Fprintf(out, "  Top churn: %s (%d changes)\n",
					profile.Conventions.ChurnHotspots[0].Path,
					profile.Conventions.ChurnHotspots[0].Changes)
			}
		}
	}

	// Pass 3: Deep analysis (LLM) — skipped in CLI context
	if shouldRun(3) {
		fmt.Fprintf(out, "Pass 3: Deep analysis (LLM-assisted)...\n")
		fmt.Fprintf(out, "  ⚠ Skipped (run with an LLM client configured via 'nxd req' pipeline)\n")
	}

	// Save profile
	if err := repolearn.SaveProfile(profileDir, profile); err != nil {
		return fmt.Errorf("save profile: %w", err)
	}

	// Print signals
	if len(profile.Signals) > 0 {
		fmt.Fprintf(out, "\nSignals:\n")
		for _, s := range profile.Signals {
			pathSuffix := ""
			if s.Path != "" {
				pathSuffix = fmt.Sprintf(" [%s]", s.Path)
			}
			fmt.Fprintf(out, "  • %s: %s%s\n", s.Kind, s.Message, pathSuffix)
		}
	}

	// Summary
	fmt.Fprintf(out, "\nProfile saved to %s\n", repolearn.ProfilePath(profileDir))
	fmt.Fprintf(out, "Iteration: %d, Passes completed: %s\n", profile.Iteration, formatPasses(profile.CompletedPasses))

	// JSON output
	if jsonOutput {
		data, err := json.MarshalIndent(profile, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal JSON: %w", err)
		}
		fmt.Fprintln(out, string(data))
	}

	return nil
}

// mergeStaticIntoProfile copies Pass 1 results from a fresh scan into an
// existing profile without clobbering Pass 2/3 data.
func mergeStaticIntoProfile(existing, scanned *repolearn.RepoProfile) {
	existing.TechStack = scanned.TechStack
	existing.Build = scanned.Build
	existing.Test = scanned.Test
	existing.Structure = scanned.Structure
	existing.CI = scanned.CI
	existing.Dependencies = scanned.Dependencies

	for _, s := range scanned.Signals {
		existing.AddSignal(s.Kind, s.Message, s.Path)
	}

	if !existing.PassCompleted(1) {
		existing.MarkPass(1)
	}
}

// formatPasses returns a human-readable list of completed passes.
func formatPasses(passes []int) string {
	if len(passes) == 0 {
		return "none"
	}
	parts := make([]string, len(passes))
	for i, p := range passes {
		parts[i] = fmt.Sprintf("%d", p)
	}
	return strings.Join(parts, ", ")
}

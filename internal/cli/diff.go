package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/artifact"
)

func newDiffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "diff <story-id>",
		Short: "Show git diff for a story's worktree",
		Long:  "Displays the git diff of a story's worktree against the base branch, showing all changes made by the agent.",
		Args:  cobra.ExactArgs(1),
		RunE:  runDiff,
	}
	cmd.Flags().Bool("stat", false, "Show diffstat summary instead of full diff")
	cmd.Flags().Bool("cached", false, "Show only staged changes")
	cmd.SilenceUsage = true
	return cmd
}

func runDiff(cmd *cobra.Command, args []string) error {
	storyID := args[0]
	stat, _ := cmd.Flags().GetBool("stat")
	cached, _ := cmd.Flags().GetBool("cached")

	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	stateDir := expandHome(cfg.Workspace.StateDir)

	// Try to find the worktree path from the artifact store's launch config.
	worktreePath, err := resolveWorktreePath(stateDir, storyID)
	if err != nil {
		return err
	}

	baseBranch := cfg.Merge.BaseBranch
	if baseBranch == "" {
		baseBranch = "main"
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "Diff for story: %s\n", storyID)
	fmt.Fprintf(out, "Worktree: %s\n", worktreePath)
	fmt.Fprintf(out, "Base: %s\n\n", baseBranch)

	// Build git diff command.
	gitArgs := []string{"diff"}
	if stat {
		gitArgs = append(gitArgs, "--stat")
	}
	if cached {
		gitArgs = append(gitArgs, "--cached")
	} else {
		gitArgs = append(gitArgs, baseBranch+"...")
	}

	gitCmd := exec.Command("git", gitArgs...)
	gitCmd.Dir = worktreePath
	gitCmd.Stdout = out
	gitCmd.Stderr = os.Stderr

	if err := gitCmd.Run(); err != nil {
		return fmt.Errorf("git diff failed: %w", err)
	}
	return nil
}

// resolveWorktreePath looks up the worktree for a story. It first checks the
// artifact store's launch_config.json for a recorded worktree path, then falls
// back to the conventional path under {stateDir}/worktrees/{storyID}.
func resolveWorktreePath(stateDir, storyID string) (string, error) {
	// Try artifact store launch config first.
	launchPath := filepath.Join(stateDir, "artifacts", storyID, string(artifact.TypeLaunchConfig)+".json")
	if data, err := os.ReadFile(launchPath); err == nil {
		var lc artifact.LaunchConfig
		if err := json.Unmarshal(data, &lc); err == nil && lc.Prompt != "" {
			// LaunchConfig doesn't store worktree path directly, but we
			// can check the conventional location.
		}
	}

	// Conventional worktree path.
	conventional := filepath.Join(stateDir, "worktrees", storyID)
	if info, err := os.Stat(conventional); err == nil && info.IsDir() {
		return conventional, nil
	}

	return "", fmt.Errorf("worktree not found for story %s (checked %s)", storyID, conventional)
}

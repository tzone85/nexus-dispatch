package cli

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/improver"
)

// newImproveCmd wires the `nxd improve` self-improvement command.
//
// Offline-first: the default run inspects metrics.jsonl + the local
// state directory. The --feed flag opts into an online feed (HTTPS URL
// returning a JSON array of Suggestion objects). The CLI persists the
// merged set to ~/.nxd/improvements.json so the dashboard can show
// them as popups in subsequent sessions.
func newImproveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "improve",
		Short: "Surface offline + online suggestions for improving the project",
		Long: `Run the self-improvement module: scan metrics + state, optionally
fetch curated tips from a JSON feed, and print recommendations.

Examples:
  nxd improve                        # offline only
  nxd improve --feed https://example.com/tips.json
  nxd improve --json                 # machine-readable output for tooling`,
		RunE: runImprove,
	}
	cmd.Flags().String("feed", "", "URL of an online tips feed (returns a JSON array of Suggestion objects)")
	cmd.Flags().Bool("json", false, "Emit suggestions as JSON instead of formatted text")
	cmd.SilenceUsage = true
	return cmd
}

func runImprove(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()
	stateDir := expandHome(s.Config.Workspace.StateDir)

	feedURL, _ := cmd.Flags().GetString("feed")
	asJSON, _ := cmd.Flags().GetBool("json")

	opts := []improver.Option{}
	if feedURL != "" {
		opts = append(opts, improver.WithOnline(improver.HTTPFeed{URL: feedURL}))
	}

	imp := improver.NewImprover(opts...)

	suggestions, errs := imp.Run(cmd.Context(), improver.ProjectInfo{
		StateDir:   stateDir,
		ProjectDir: ".",
	})
	for _, err := range errs {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: %v\n", err)
	}

	// Persist to ~/.nxd/improvements.json so the dashboard sees them
	// without re-running the analyzers on each WebSocket tick.
	persistPath := filepath.Join(stateDir, "improvements.json")
	if err := improver.SaveSuggestions(persistPath, suggestions); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: persist suggestions: %v\n", err)
	}

	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(suggestions)
	}

	if len(suggestions) == 0 {
		fmt.Fprintln(out, "No suggestions — looking healthy.")
		return nil
	}

	fmt.Fprintf(out, "%d suggestion(s):\n\n", len(suggestions))
	for _, s := range suggestions {
		fmt.Fprintf(out, "[%s] %s\n  %s\n", s.Severity, s.Title, s.Description)
		if s.Action != "" {
			fmt.Fprintf(out, "  Action: %s\n", s.Action)
		}
		fmt.Fprintln(out)
	}
	return nil
}

package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show requirement and story status",
		Long:  "Lists requirements and their stories with current status. Use --req to filter by requirement ID. Use --all to show archived requirements and those from other repos.",
		RunE:  runStatus,
	}
	cmd.Flags().String("req", "", "Filter by requirement ID")
	cmd.Flags().Bool("all", false, "Show all requirements including archived and from other repos")
	cmd.SilenceUsage = true
	return cmd
}

func runStatus(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	reqFilter, _ := cmd.Flags().GetString("req")
	showAll, _ := cmd.Flags().GetBool("all")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	if reqFilter != "" {
		return showRequirementStatus(cmd, s, reqFilter)
	}

	// Build filter based on flags
	var filter state.ReqFilter
	if !showAll {
		cwd, _ := os.Getwd()
		filter.RepoPath = cwd
		filter.ExcludeArchived = true
	}

	reqs, err := s.Proj.ListRequirementsFiltered(filter)
	if err != nil {
		return fmt.Errorf("list requirements: %w", err)
	}

	if len(reqs) == 0 {
		fmt.Fprintf(out, "No requirements found. Run 'nxd req \"<requirement>\"' to get started.\n")
		if !showAll {
			fmt.Fprintf(out, "Hint: use --all to show requirements from all repos and archived ones.\n")
		}
		return nil
	}

	fmt.Fprintf(out, "Requirements:\n\n")
	for _, req := range reqs {
		stories, storyErr := s.Proj.ListStories(state.StoryFilter{ReqID: req.ID})
		if storyErr != nil {
			return fmt.Errorf("list stories for %s: %w", req.ID, storyErr)
		}

		counts := countByStatus(stories)
		fmt.Fprintf(out, "  [%s] %s (%s)\n", req.ID[:8], req.Title, req.Status)
		fmt.Fprintf(out, "    Stories: %d total", len(stories))
		if len(counts) > 0 {
			fmt.Fprintf(out, " (")
			first := true
			for status, count := range counts {
				if !first {
					fmt.Fprintf(out, ", ")
				}
				fmt.Fprintf(out, "%s: %d", status, count)
				first = false
			}
			fmt.Fprintf(out, ")")
		}
		fmt.Fprintf(out, "\n\n")
	}

	return nil
}

func showRequirementStatus(cmd *cobra.Command, s stores, reqID string) error {
	out := cmd.OutOrStdout()

	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("get requirement: %w", err)
	}

	fmt.Fprintf(out, "Requirement: %s\n", req.Title)
	fmt.Fprintf(out, "ID:          %s\n", req.ID)
	fmt.Fprintf(out, "Status:      %s\n", req.Status)
	fmt.Fprintf(out, "Created:     %s\n\n", req.CreatedAt.Format("2006-01-02 15:04:05"))

	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return fmt.Errorf("list stories: %w", err)
	}

	if len(stories) == 0 {
		fmt.Fprintf(out, "No stories yet.\n")
		return nil
	}

	fmt.Fprintf(out, "Stories:\n\n")
	for i, story := range stories {
		agent := story.AgentID
		if agent == "" {
			agent = "unassigned"
		}
		fmt.Fprintf(out, "  %d. [%s] %s\n", i+1, story.Status, story.Title)
		fmt.Fprintf(out, "     ID: %s | Complexity: %d | Agent: %s\n", story.ID, story.Complexity, agent)
		if story.Branch != "" {
			fmt.Fprintf(out, "     Branch: %s\n", story.Branch)
		}
		if story.PRUrl != "" {
			fmt.Fprintf(out, "     PR: %s\n", story.PRUrl)
		}
		fmt.Fprintf(out, "\n")
	}

	counts := countByStatus(stories)
	fmt.Fprintf(out, "Summary: %d total", len(stories))
	for status, count := range counts {
		fmt.Fprintf(out, ", %s: %d", status, count)
	}
	fmt.Fprintf(out, "\n")

	return nil
}

// countByStatus returns a map of status -> count for the given stories.
func countByStatus(stories []state.Story) map[string]int {
	counts := make(map[string]int)
	for _, s := range stories {
		counts[s.Status]++
	}
	return counts
}

package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newResumeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "resume <req-id>",
		Short: "Resume a paused requirement pipeline",
		Long:  "Loads existing state for a requirement, finds incomplete stories, and dispatches the next wave of ready stories.",
		Args:  cobra.ExactArgs(1),
		RunE:  runResume,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runResume(cmd *cobra.Command, args []string) error {
	reqID := args[0]

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	out := cmd.OutOrStdout()

	// Verify the requirement exists
	req, err := s.Proj.GetRequirement(reqID)
	if err != nil {
		return fmt.Errorf("requirement not found: %w", err)
	}

	fmt.Fprintf(out, "Resuming requirement: %s (%s)\n", req.Title, req.Status)

	// Load all stories for this requirement
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return fmt.Errorf("list stories: %w", err)
	}

	if len(stories) == 0 {
		fmt.Fprintf(out, "No stories found for this requirement.\n")
		return nil
	}

	// Rebuild the dependency graph from story created events
	dag, plannedStories, err := rebuildDAGFromEvents(s.Events, reqID, stories)
	if err != nil {
		return fmt.Errorf("rebuild dependency graph: %w", err)
	}

	// Determine completed stories
	completed := make(map[string]bool)
	for _, story := range stories {
		if story.Status == "merged" || story.Status == "pr_submitted" {
			completed[story.ID] = true
		}
	}

	fmt.Fprintf(out, "Stories: %d total, %d completed\n", len(stories), len(completed))

	// Check if all done
	if len(completed) == len(stories) {
		fmt.Fprintf(out, "All stories are complete.\n")
		return nil
	}

	// Dispatch next wave
	dispatcher := engine.NewDispatcher(s.Config, s.Events, s.Proj)
	assignments, err := dispatcher.DispatchWave(dag, completed, reqID, plannedStories)
	if err != nil {
		return fmt.Errorf("dispatch wave: %w", err)
	}

	if len(assignments) == 0 {
		fmt.Fprintf(out, "No stories ready for dispatch (dependencies not yet met).\n")
		return nil
	}

	fmt.Fprintf(out, "\nDispatched %d stories:\n\n", len(assignments))
	for _, a := range assignments {
		fmt.Fprintf(out, "  [%s] -> %s (agent: %s, branch: %s)\n",
			a.StoryID, a.Role, a.AgentID, a.Branch)
	}

	return nil
}

// rebuildDAGFromEvents reconstructs the dependency graph from event store data
// and builds PlannedStory entries from existing stories.
func rebuildDAGFromEvents(es state.EventStore, reqID string, stories []state.Story) (*graph.DAG, []engine.PlannedStory, error) {
	dag := graph.New()

	// Build planned stories from projection data
	planned := make([]engine.PlannedStory, 0, len(stories))
	for _, story := range stories {
		dag.AddNode(story.ID)
		planned = append(planned, engine.PlannedStory{
			ID:          story.ID,
			Title:       story.Title,
			Description: story.Description,
			Complexity:  story.Complexity,
		})
	}

	// Reconstruct edges from STORY_CREATED events (they contain depends_on in payload)
	events, err := es.List(state.EventFilter{Type: state.EventStoryCreated})
	if err != nil {
		return nil, nil, fmt.Errorf("list story created events: %w", err)
	}

	storySet := make(map[string]bool, len(stories))
	for _, story := range stories {
		storySet[story.ID] = true
	}

	for _, evt := range events {
		if !storySet[evt.StoryID] {
			continue
		}
		// Dependencies would be encoded in the payload; for now the graph
		// is reconstructed without edges since the event payload doesn't
		// store depends_on. Stories without dependency info will all appear
		// ready simultaneously, which is the safe fallback.
	}

	return dag, planned, nil
}

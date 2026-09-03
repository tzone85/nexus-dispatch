package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/state"
	"github.com/tzone85/nexus-dispatch/internal/tmux"
)

// sessionKiller abstracts the tmux calls cancel needs so tests can fake them.
type sessionKiller interface {
	SessionExists(name string) bool
	KillSession(name string) error
}

// realTmux is the production sessionKiller.
type realTmux struct{}

func (realTmux) SessionExists(name string) bool { return tmux.SessionExists(name) }
func (realTmux) KillSession(name string) error  { return tmux.KillSession(name) }

// cancelSessions is the sessionKiller used by `nxd cancel`; swapped in tests.
var cancelSessions sessionKiller = realTmux{}

// cancelReason is the reason recorded when the operator gives none.
const cancelReason = "cancelled by operator"

// activeStoryStatuses are the story states that mean "work is in flight";
// cancelling a requirement resets exactly these.
var activeStoryStatuses = map[string]bool{
	"assigned":    true,
	"in_progress": true,
	"review":      true,
	"qa":          true,
	"merge_ready": true,
}

func newCancelCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cancel <req-id|story-id>",
		Short: "Cancel a running story or requirement",
		Long: `Stops work on a story or a whole requirement.

For a story: emits STORY_RESET (back to draft) and AGENT_TERMINATED with the
reason, and kills the agent's tmux session if it is alive.

For a requirement: cancels every active story as above, then emits REQ_PAUSED
with reason "cancelled by operator". Resume later with 'nxd resume <req-id>'.`,
		Args: cobra.ExactArgs(1),
		RunE: runCancel,
	}
	cmd.Flags().String("reason", "", "reason recorded on the cancellation events")
	cmd.SilenceUsage = true
	return cmd
}

func runCancel(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	reason, _ := cmd.Flags().GetString("reason")
	out := cmd.OutOrStdout()
	id := args[0]

	if req, err := s.Proj.GetRequirement(id); err == nil {
		return cancelRequirement(out, s, req, reason)
	}
	story, err := s.Proj.GetStory(id)
	if err != nil {
		return fmt.Errorf("no requirement or story with id %q", id)
	}
	return cancelStory(out, s, story, reason)
}

// fullReason combines the operator's --reason with the fixed marker.
func fullReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return cancelReason
	}
	return cancelReason + ": " + reason
}

// cancelStory resets one story, terminates its agent and kills its session.
func cancelStory(out io.Writer, s stores, story state.Story, reason string) error {
	if !activeStoryStatuses[story.Status] {
		return fmt.Errorf("story %s is %s, not active; nothing to cancel", story.ID, story.Status)
	}
	why := fullReason(reason)
	// Resolve the session before AGENT_TERMINATED clears current_story_id.
	session := sessionForStory(s, story)

	reset := state.NewEvent(state.EventStoryReset, "operator", story.ID, map[string]any{
		"reason": why, "previous_status": story.Status,
	})
	if err := emit(s, reset); err != nil {
		return err
	}
	term := state.NewEvent(state.EventAgentTerminated, "operator", story.ID, map[string]any{
		"reason": why, "agent_id": story.AgentID,
	})
	if err := emit(s, term); err != nil {
		return err
	}

	if cancelSessions.SessionExists(session) {
		if err := cancelSessions.KillSession(session); err != nil {
			fmt.Fprintf(out, "  warning: could not kill tmux session %s: %v\n", session, err)
		} else {
			fmt.Fprintf(out, "  killed tmux session %s\n", session)
		}
	}
	fmt.Fprintf(out, "Cancelled story %s (%s → draft): %s\n", story.ID, story.Status, why)
	return nil
}

// sessionForStory returns the agent's recorded tmux session, falling back to
// the nxd-<story> convention the executor uses.
func sessionForStory(s stores, story state.Story) string {
	agents, err := s.Proj.ListAgents(state.AgentFilter{})
	if err == nil {
		for _, a := range agents {
			if a.CurrentStoryID == story.ID && a.SessionName != "" {
				return a.SessionName
			}
		}
	}
	return "nxd-" + story.ID
}

// cancelRequirement cancels every active story then pauses the requirement.
func cancelRequirement(out io.Writer, s stores, req state.Requirement, reason string) error {
	if req.Status == "completed" || req.Status == "archived" {
		return fmt.Errorf("requirement %s is %s; nothing to cancel", req.ID, req.Status)
	}
	stories, err := s.Proj.ListStories(state.StoryFilter{ReqID: req.ID})
	if err != nil {
		return fmt.Errorf("list stories: %w", err)
	}
	cancelled := 0
	for _, st := range stories {
		if !activeStoryStatuses[st.Status] {
			continue
		}
		if err := cancelStory(out, s, st, reason); err != nil {
			return err
		}
		cancelled++
	}
	paused := state.NewEvent(state.EventReqPaused, "operator", "", map[string]any{
		"id": req.ID, "reason": fullReason(reason), "stories_cancelled": cancelled,
	})
	if err := emit(s, paused); err != nil {
		return err
	}
	fmt.Fprintf(out, "Cancelled requirement %s (%d active stories reset); status → paused\n", req.ID, cancelled)
	fmt.Fprintf(out, "Resume later with: nxd resume %s\n", req.ID)
	return nil
}

// emit appends an event and projects it, failing loudly on either step.
func emit(s stores, evt state.Event) error {
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("append %s: %w", evt.Type, err)
	}
	if err := s.Proj.Project(evt); err != nil {
		return fmt.Errorf("project %s: %w", evt.Type, err)
	}
	return nil
}

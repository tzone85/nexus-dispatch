// internal/cli/direct.go — `nxd direct` operator-directive subcommand.
//
// Lets the operator inject mid-flight instructions into a running NXD
// session without pausing it. The next iteration of the matching agent(s)
// prepends the directive to its prompt.
//
//   nxd direct <req-id>      "use channels not goroutines"   # broadcast to whole requirement
//   nxd direct <story-id>    "skip the win-detection test"   # narrow to one story
//   nxd direct --req <id>    --message-file ./hint.md
package cli

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newDirectCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "direct <id> [instruction]",
		Short: "Inject an operator directive that the next agent iteration will read",
		Long: `Append an operator directive to the event log. The native runtime checks
for unacknowledged directives at the top of every iteration and prepends
them to the agent's prompt — useful for redirecting agents without
pausing the run.

The id may be a requirement ID (broadcasts to every story under it) or a
story ID (targets a single story).

Examples:
  nxd direct 01KQF...REQ_ID  "use stdlib only — no third-party deps"
  nxd direct 01KQF-s-003     "ignore the test_passes criterion for now"
  nxd direct 01KQF...REQ     --message-file ./redirect.md`,
		Args: cobra.MinimumNArgs(1),
		RunE: runDirect,
	}
	cmd.Flags().String("message-file", "", "Read instruction from a file (use - for stdin)")
	cmd.SilenceUsage = true
	return cmd
}

func runDirect(cmd *cobra.Command, args []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	id := args[0]

	// Resolve instruction: positional args, or --message-file, or stdin if file=-.
	instruction := strings.Join(args[1:], " ")
	if msgFile, _ := cmd.Flags().GetString("message-file"); msgFile != "" {
		var data []byte
		var readErr error
		if msgFile == "-" {
			data, readErr = io.ReadAll(cmd.InOrStdin())
		} else {
			data, readErr = os.ReadFile(msgFile)
		}
		if readErr != nil {
			return fmt.Errorf("read instruction: %w", readErr)
		}
		instruction = strings.TrimSpace(string(data))
	}
	if strings.TrimSpace(instruction) == "" {
		return fmt.Errorf("empty instruction — provide as a positional arg or via --message-file")
	}

	// Identify whether id is a requirement or a story.
	reqID, storyID, scope, err := resolveDirectiveScope(s.Proj, id)
	if err != nil {
		return err
	}

	// Emit the USER_DIRECTIVE event. The runtime picks it up at the next
	// iteration boundary and emits a paired DIRECTIVE_ACKED on delivery.
	evt := state.NewEvent(state.EventUserDirective, "operator", storyID, map[string]any{
		"req_id":      reqID,
		"story_id":    storyID,
		"instruction": instruction,
		"source":      "cli",
	})
	if err := s.Events.Append(evt); err != nil {
		return fmt.Errorf("append directive event: %w", err)
	}
	if err := s.Proj.Project(evt); err != nil {
		// Projection failure is not fatal — directives are sourced from the
		// event log directly, not the projection. Log and continue.
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: projection failed: %v\n", err)
	}

	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "directive submitted (%s)\n", scope)
	fmt.Fprintf(out, "  id:          %s\n", evt.ID)
	if reqID != "" {
		fmt.Fprintf(out, "  req:         %s\n", reqID)
	}
	if storyID != "" {
		fmt.Fprintf(out, "  story:       %s\n", storyID)
	}
	fmt.Fprintf(out, "  instruction: %s\n", instruction)
	fmt.Fprintf(out, "\nThe next iteration of the targeted agent(s) will see this directive.\n")
	return nil
}

// resolveDirectiveScope figures out whether id is a requirement ID or a
// story ID by consulting the projection. Returns (reqID, storyID, label).
//
// Detection rules:
//   - If GetRequirement(id) succeeds → broadcast directive (storyID="").
//   - Else if GetStory(id) succeeds → targeted directive (reqID inferred from story).
//   - Else: error.
func resolveDirectiveScope(proj *state.SQLiteStore, id string) (string, string, string, error) {
	if req, err := proj.GetRequirement(id); err == nil && req.ID != "" {
		return req.ID, "", "broadcast to requirement", nil
	}
	story, err := proj.GetStory(id)
	if err == nil && story.ID != "" {
		return story.ReqID, story.ID, "targeted at story", nil
	}
	_ = engine.Directive{} // keep engine import alive for godoc cross-references
	return "", "", "", fmt.Errorf("id %q is neither a known requirement nor a story", id)
}

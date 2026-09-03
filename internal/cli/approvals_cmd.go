package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/user"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/approvals"
)

// newApprovalsCmd is the human approval queue:
//
//	nxd approvals list [--req <id>] [--all] [--json]
//	nxd approvals approve <item-id> [--note <text>]
//	nxd approvals reject  <item-id> [--note <text>]
//
// `nxd approve <req-id>` (plan approval) is a different command and is
// unchanged.
func newApprovalsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "approvals",
		Short: "List and resolve pending human approvals (conflicts, integration failures, security findings, merges)",
		Long: "The pipeline records decisions that need a human — conflict resolutions, post-merge integration failures, " +
			"security findings, gated merges — as approval items. A pending item pauses the requirement; approve or reject " +
			"it here (or in the dashboard) and `nxd resume <req-id>` to continue.",
	}
	cmd.SilenceUsage = true
	cmd.AddCommand(newApprovalsListCmd(), newApprovalsDecideCmd("approve", approvals.StatusApproved), newApprovalsDecideCmd("reject", approvals.StatusRejected))
	return cmd
}

func newApprovalsListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List pending approvals",
		Args:  cobra.NoArgs,
		RunE:  runApprovalsList,
	}
	cmd.Flags().String("req", "", "only show approvals for this requirement")
	cmd.Flags().Bool("all", false, "include approved/rejected items")
	cmd.Flags().Bool("json", false, "output as JSON")
	cmd.SilenceUsage = true
	return cmd
}

func newApprovalsDecideCmd(verb string, status approvals.Status) *cobra.Command {
	cmd := &cobra.Command{
		Use:   verb + " <item-id>",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a pending approval item",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApprovalsDecide(cmd, args[0], status)
		},
	}
	cmd.Flags().String("note", "", "optional note recorded with the decision")
	cmd.SilenceUsage = true
	return cmd
}

func runApprovalsList(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	reqID, _ := cmd.Flags().GetString("req")
	all, _ := cmd.Flags().GetBool("all")
	asJSON, _ := cmd.Flags().GetBool("json")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	q, err := approvals.Load(s.Events)
	if err != nil {
		return fmt.Errorf("load approvals: %w", err)
	}
	items := q.Pending(reqID)
	if all {
		items = q.All(reqID)
	}
	return renderApprovals(cmd.OutOrStdout(), items, asJSON)
}

// renderApprovals prints items as a table or JSON. Pure; unit-tested directly.
func renderApprovals(out io.Writer, items []approvals.Item, asJSON bool) error {
	if asJSON {
		if items == nil {
			items = []approvals.Item{}
		}
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(items)
	}
	if len(items) == 0 {
		fmt.Fprintln(out, "No approvals found.")
		return nil
	}
	fmt.Fprintf(out, "Approvals (%d):\n\n", len(items))
	fmt.Fprintf(out, "  %-26s %-9s %-20s %-12s %-20s %s\n", "ID", "STATUS", "KIND", "REQ", "STORY", "SUMMARY")
	fmt.Fprintf(out, "  %-26s %-9s %-20s %-12s %-20s %s\n", "--", "------", "----", "---", "-----", "-------")
	for _, it := range items {
		story := it.StoryID
		if story == "" {
			story = "-"
		}
		fmt.Fprintf(out, "  %-26s %-9s %-20s %-12s %-20s %s\n",
			it.ID, it.Status, it.Kind, truncate(it.ReqID, 12), truncate(story, 20), it.Summary)
		if it.Note != "" {
			fmt.Fprintf(out, "  %-26s note: %s (%s)\n", "", it.Note, it.DecidedBy)
		}
	}
	fmt.Fprintln(out, "\nResolve with: nxd approvals approve|reject <id> [--note \"...\"]")
	return nil
}

func runApprovalsDecide(cmd *cobra.Command, itemID string, status approvals.Status) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	note, _ := cmd.Flags().GetString("note")

	s, err := loadStores(cfgPath)
	if err != nil {
		return err
	}
	defer s.Close()

	q, err := approvals.Load(s.Events)
	if err != nil {
		return fmt.Errorf("load approvals: %w", err)
	}
	it, err := q.Resolve(itemID, status, currentUser(), note)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "%s %s (%s) for %s\n", strings.ToUpper(string(status)[:1])+string(status)[1:], it.ID, it.Kind, it.ReqID)
	fmt.Fprintf(out, "Run 'nxd resume %s' to continue the pipeline.\n", it.ReqID)
	return nil
}

// currentUser names the decider: $NXD_USER, then the OS user, then "cli".
func currentUser() string {
	if u := strings.TrimSpace(os.Getenv("NXD_USER")); u != "" {
		return u
	}
	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	return "cli"
}

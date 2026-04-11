package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
)

func newReportCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "report <req-id>",
		Short: "Generate a delivery report for a requirement",
		Long:  "Produces a client-facing delivery report with deliverables, timeline, and effort.\nUse --internal for technical detail. Use --html for styled HTML output.",
		Args:  cobra.ExactArgs(1),
		RunE:  runReport,
	}
	cmd.Flags().Bool("html", false, "Output as self-contained HTML")
	cmd.Flags().Bool("internal", false, "Include internal technical detail")
	cmd.Flags().StringP("output", "o", "", "Write report to file instead of stdout")
	return cmd
}

func runReport(cmd *cobra.Command, args []string) error {
	reqID := args[0]
	htmlOutput, _ := cmd.Flags().GetBool("html")
	internal, _ := cmd.Flags().GetBool("internal")
	outputPath, _ := cmd.Flags().GetString("output")

	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return fmt.Errorf("loading stores: %w", err)
	}
	defer s.Close()

	project := "nxd"

	builder := engine.NewReportBuilder(s.Events, s.Proj, s.Config)
	data, err := builder.Build(reqID)
	if err != nil {
		return err
	}

	var content string
	if htmlOutput {
		content = engine.RenderHTML(data, project, internal)
	} else {
		content = engine.RenderMarkdown(data, project, internal)
	}

	if outputPath != "" {
		if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
			return fmt.Errorf("write file: %w", err)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "Report written to %s\n", outputPath)
		return nil
	}

	fmt.Fprint(cmd.OutOrStdout(), content)
	return nil
}

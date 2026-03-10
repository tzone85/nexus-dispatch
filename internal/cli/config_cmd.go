package cli

import (
	"fmt"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config [show|validate]",
		Short: "Show or validate the current configuration",
		Long:  "Subcommands: 'show' pretty-prints the current config as YAML, 'validate' loads and validates the config.",
	}
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigValidateCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Pretty-print current configuration as YAML",
		RunE:  runConfigShow,
	}
	cmd.SilenceUsage = true
	return cmd
}

func newConfigValidateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate the current configuration",
		RunE:  runConfigValidate,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runConfigShow(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	fmt.Fprintf(out, "%s", string(data))
	return nil
}

func runConfigValidate(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")

	out := cmd.OutOrStdout()

	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintf(out, "Config validation FAILED: %v\n", err)
		return err
	}

	// LoadFromFile already validates, but we can run validate explicitly
	// to provide a clear success message.
	if err := cfg.Validate(); err != nil {
		fmt.Fprintf(out, "Config validation FAILED: %v\n", err)
		return err
	}

	fmt.Fprintf(out, "Config validation PASSED.\n")
	return nil
}

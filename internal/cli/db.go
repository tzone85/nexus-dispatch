package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/devdb"
	devdbdocker "github.com/tzone85/nexus-dispatch/internal/devdb/docker"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newDBCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage ephemeral story databases",
		Long: `Inspect and manage devdb-provisioned databases. The active devdb provider
is determined by the current project's devdb.provider config (docker | null).`,
	}
	cmd.SilenceUsage = true

	cmd.AddCommand(newDBListCmd())
	cmd.AddCommand(newDBConnectCmd())
	cmd.AddCommand(newDBSQLCmd())
	cmd.AddCommand(newDBSchemaCmd())
	cmd.AddCommand(newDBDeleteCmd())
	cmd.AddCommand(newDBGCCmd())
	cmd.AddCommand(newDBPingCmd())
	cmd.AddCommand(newDBTemplateCmd())
	return cmd
}

// dbProviderFor builds a Provider from the project's runtime config.
// Returns an error with a helpful message when devdb is disabled.
func dbProviderFor(cmd *cobra.Command) (devdb.Provider, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if s.Config.DevDB.Provider == "" || s.Config.DevDB.Provider == "null" {
		return nil, fmt.Errorf("devdb is not configured for this project (devdb.provider is null or unset)")
	}
	return newDevDBProvider(s.Config)
}

// findDBByNameOrID looks up a DB from the provider list by name or ID.
func findDBByNameOrID(dbs []devdb.DB, nameOrID string) (*devdb.DB, error) {
	for i := range dbs {
		if dbs[i].Name == nameOrID || dbs[i].ID == nameOrID {
			return &dbs[i], nil
		}
	}
	return nil, fmt.Errorf("db %q not found", nameOrID)
}

// --- list ---

func newDBListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List all DBs known to the devdb provider",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			dbs, err := p.List(ctx)
			if err != nil {
				return err
			}
			if len(dbs) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No databases found.")
				return nil
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tID\tPROVIDER\tCREATED")
			fmt.Fprintln(w, "----\t--\t--------\t-------")
			for _, db := range dbs {
				created := db.CreatedAt.Format(time.RFC3339)
				if db.CreatedAt.IsZero() {
					created = "-"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", db.Name, db.ID, db.Provider, created)
			}
			return w.Flush()
		},
	}
}

// --- connect ---

func newDBConnectCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "connect <db-name>",
		Aliases:      []string{"psql"},
		Short:        "Print the psql command + DSN for a DB",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			dbs, err := p.List(ctx)
			if err != nil {
				return err
			}
			db, err := findDBByNameOrID(dbs, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "DATABASE_URL=%s\n", db.ConnectionString)
			fmt.Fprintf(cmd.OutOrStdout(), "To connect: psql %q\n", db.ConnectionString)
			return nil
		},
	}
}

// --- sql ---

func newDBSQLCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "sql <db-name> <query>",
		Short:        "Run a one-shot SQL query against a DB",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			dbs, err := p.List(ctx)
			if err != nil {
				return err
			}
			target, err := findDBByNameOrID(dbs, args[0])
			if err != nil {
				return err
			}
			conn, err := pgx.Connect(ctx, target.ConnectionString)
			if err != nil {
				return fmt.Errorf("connect: %w", err)
			}
			defer conn.Close(ctx)
			rows, err := conn.Query(ctx, args[1])
			if err != nil {
				return fmt.Errorf("query: %w", err)
			}
			defer rows.Close()
			out := cmd.OutOrStdout()
			fields := rows.FieldDescriptions()
			colNames := make([]string, len(fields))
			for i, f := range fields {
				colNames[i] = string(f.Name)
			}
			fmt.Fprintln(out, strings.Join(colNames, "\t"))
			for rows.Next() {
				vals, scanErr := rows.Values()
				if scanErr != nil {
					return fmt.Errorf("scan: %w", scanErr)
				}
				parts := make([]string, len(vals))
				for i, v := range vals {
					parts[i] = fmt.Sprintf("%v", v)
				}
				fmt.Fprintln(out, strings.Join(parts, "\t"))
			}
			return rows.Err()
		},
	}
}

// --- schema ---

func newDBSchemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "schema <db-name>",
		Short:        "Print agent-friendly schema dump for a DB",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			s, err := p.Schema(ctx, args[0])
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), s)
			return nil
		},
	}
}

// --- delete ---

func newDBDeleteCmd() *cobra.Command {
	var confirm bool
	cmd := &cobra.Command{
		Use:          "delete <db-name>",
		Short:        "Delete a DB permanently",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !confirm {
				return fmt.Errorf("destructive operation — pass --confirm to proceed (deletes %q permanently)", args[0])
			}
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			if err := p.Delete(ctx, args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().BoolVar(&confirm, "confirm", false, "Confirm deletion (destructive)")
	return cmd
}

// --- gc ---

func newDBGCCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "gc",
		Short:        "Run orphan recovery (scan for stale DBs and release old ones)",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfgPath, _ := cmd.Flags().GetString("config")
			s, err := loadStores(cfgPath)
			if err != nil {
				return err
			}
			defer s.Close()
			stories, err := s.Proj.ListStories(state.StoryFilter{})
			if err != nil {
				return fmt.Errorf("list stories: %w", err)
			}
			runDevDBOrphanRecovery(cmd.OutOrStdout(), s.Config, stories)
			return nil
		},
	}
}

// --- ping ---

func newDBPingCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "ping",
		Short:        "Verify the devdb provider is reachable",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dbProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			if err := p.Ping(ctx); err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), "provider OK")
			return nil
		},
	}
}

// --- template ---

// dockerProviderFor returns the docker.Provider for the current project, or
// an error if the configured provider is not "docker". This is needed because
// template ops are docker-specific.
func dockerProviderFor(cmd *cobra.Command) (*devdbdocker.Provider, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	s, err := loadStores(cfgPath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	if s.Config.DevDB.Provider != "docker" {
		return nil, fmt.Errorf("template ops require devdb.provider == \"docker\" (current: %q)", s.Config.DevDB.Provider)
	}
	return devdbdocker.NewProvider(devdbdocker.Config{
		Image:          s.Config.DevDB.Docker.Image,
		ContainerName:  s.Config.DevDB.Docker.ContainerName,
		TemplateVolume: s.Config.DevDB.Docker.TemplateVolume,
		Network:        s.Config.DevDB.Docker.Network,
		HostPortRange:  s.Config.DevDB.Docker.HostPortRange,
		Host:           s.Config.DevDB.Docker.Host,
	}), nil
}

func newDBTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "template",
		Short:        "Manage devdb template databases (docker provider only)",
		SilenceUsage: true,
	}
	cmd.AddCommand(newDBTemplateListCmd())
	cmd.AddCommand(newDBTemplateCreateCmd())
	return cmd
}

func newDBTemplateListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List template databases",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := dockerProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
			defer cancel()
			names, err := p.ListTemplates(ctx)
			if err != nil {
				return err
			}
			if len(names) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No templates registered.")
				return nil
			}
			for _, n := range names {
				fmt.Fprintln(cmd.OutOrStdout(), n)
			}
			return nil
		},
	}
}

func newDBTemplateCreateCmd() *cobra.Command {
	var dumpPath string
	cmd := &cobra.Command{
		Use:          "create <name>",
		Short:        "Create a template DB by importing a SQL dump",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if dumpPath == "" {
				return fmt.Errorf("--from <path-to-sql-file> is required")
			}
			f, err := os.Open(dumpPath)
			if err != nil {
				return fmt.Errorf("open dump: %w", err)
			}
			defer f.Close()
			p, err := dockerProviderFor(cmd)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Minute)
			defer cancel()
			if err := p.CreateTemplate(ctx, args[0], f); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created template %s\n", args[0])
			return nil
		},
	}
	cmd.Flags().StringVar(&dumpPath, "from", "", "Path to SQL dump file (required)")
	return cmd
}

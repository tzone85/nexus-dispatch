package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
	"github.com/tzone85/nexus-dispatch/internal/engine"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// newStateCmd groups the event-log / projection maintenance commands.
func newStateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Inspect and repair the event log and projection",
		Long: `Maintenance for the per-project state directory (workspace.state_dir).

  check    read-only health report of events.jsonl (torn tail, malformed lines)
  repair   move malformed lines / torn tail to events.quarantine.jsonl (keeps a .bak)
  compact  archive STORY_PROGRESS / AGENT_CHECKPOINT events of completed requirements
  rebuild  replay events.jsonl into the SQLite projection under the pipeline lock

repair, compact and rebuild take the pipeline lock and refuse to run while a
pipeline is active. Pass --json for machine-readable output.`,
	}
	cmd.PersistentFlags().Bool("json", false, "machine-readable JSON output")
	cmd.AddCommand(newStateCheckCmd(), newStateRepairCmd(), newStateCompactCmd(), newStateRebuildCmd())
	return cmd
}

// eventsLogPath returns the events.jsonl path for the loaded config.
func eventsLogPath(cmd *cobra.Command) (string, error) {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(cfg.Workspace.StateDir, "events.jsonl"), nil
}

// stateJSON reports whether --json was requested on the state command tree.
func stateJSON(cmd *cobra.Command) bool {
	v, _ := cmd.Flags().GetBool("json")
	return v
}

func writeJSON(out io.Writer, v any) error {
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// withPipelineLock runs fn while holding the pipeline lock for the state
// directory containing eventsPath. A running pipeline yields a clear error.
func withPipelineLock(eventsPath string, fn func() error) error {
	lock, err := engine.TryAcquireLock(filepath.Dir(eventsPath))
	if err != nil {
		return fmt.Errorf("cannot run while a pipeline is active: %w", err)
	}
	defer func() { _ = lock.Release() }()
	return fn()
}

func newStateCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Report event-log health (read-only)",
		Args:  cobra.NoArgs,
		RunE:  runStateCheck,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runStateCheck(cmd *cobra.Command, _ []string) error {
	path, err := eventsLogPath(cmd)
	if err != nil {
		return err
	}
	rep, err := state.Check(path)
	if err != nil {
		return fmt.Errorf("check %s: %w", path, err)
	}
	out := cmd.OutOrStdout()
	if stateJSON(cmd) {
		return writeJSON(out, rep)
	}
	printCheckReport(out, rep)
	if !rep.Healthy() {
		return fmt.Errorf("event log has problems; run `nxd state repair`")
	}
	return nil
}

func printCheckReport(out io.Writer, rep state.Report) {
	fmt.Fprintf(out, "Event log: %s\n", rep.Path)
	fmt.Fprintf(out, "  Size:        %d bytes\n", rep.SizeBytes)
	fmt.Fprintf(out, "  Lines:       %d (%d valid)\n", rep.Lines, rep.Valid)
	if !rep.LastEventTime.IsZero() {
		fmt.Fprintf(out, "  Last event:  %s\n", rep.LastEventTime.Format(time.RFC3339))
	}
	if rep.Torn {
		fmt.Fprintf(out, "  Torn tail:   yes (crash mid-write; skipped by readers)\n")
	}
	if len(rep.Malformed) > 0 {
		fmt.Fprintf(out, "  Malformed:   %d line(s)\n", len(rep.Malformed))
		for _, m := range rep.Malformed {
			fmt.Fprintf(out, "    line %d: %s — %s\n", m.Line, m.Error, m.Snippet)
		}
	}
	if rep.Healthy() {
		fmt.Fprintln(out, "  Status:      healthy")
	} else {
		fmt.Fprintln(out, "  Status:      NEEDS REPAIR (nxd state repair)")
	}
}

func newStateRepairCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "repair",
		Short: "Quarantine malformed lines and rewrite the event log",
		Args:  cobra.NoArgs,
		RunE:  runStateRepair,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runStateRepair(cmd *cobra.Command, _ []string) error {
	path, err := eventsLogPath(cmd)
	if err != nil {
		return err
	}
	var moved int
	err = withPipelineLock(path, func() error {
		var rerr error
		moved, rerr = state.Repair(path)
		return rerr
	})
	if err != nil {
		return fmt.Errorf("repair %s: %w", path, err)
	}
	out := cmd.OutOrStdout()
	if stateJSON(cmd) {
		return writeJSON(out, map[string]any{
			"path": path, "moved": moved, "quarantine": state.QuarantineName(path), "backup": path + ".bak",
		})
	}
	if moved == 0 {
		fmt.Fprintf(out, "Event log %s is healthy; nothing to repair.\n", path)
		return nil
	}
	fmt.Fprintf(out, "Repaired %s: moved %d line(s) to %s\n", path, moved, state.QuarantineName(path))
	fmt.Fprintf(out, "Original kept at %s.bak\n", path)
	return nil
}

func newStateCompactCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compact",
		Short: "Archive informational events of completed requirements",
		Long: "Removes STORY_PROGRESS and AGENT_CHECKPOINT events whose requirement has reached REQ_COMPLETED,\n" +
			"writing them to events.archive-<timestamp>.jsonl. Nothing else is ever removed.",
		Args: cobra.NoArgs,
		RunE: runStateCompact,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runStateCompact(cmd *cobra.Command, _ []string) error {
	path, err := eventsLogPath(cmd)
	if err != nil {
		return err
	}
	var res state.CompactResult
	err = withPipelineLock(path, func() error {
		var cerr error
		res, cerr = state.Compact(path, time.Now())
		return cerr
	})
	if err != nil {
		return fmt.Errorf("compact %s: %w", path, err)
	}
	out := cmd.OutOrStdout()
	if stateJSON(cmd) {
		return writeJSON(out, res)
	}
	if res.Removed == 0 {
		fmt.Fprintf(out, "Nothing to compact: %d events kept.\n", res.Kept)
		return nil
	}
	fmt.Fprintf(out, "Compacted %s: removed %d events, kept %d\n", path, res.Removed, res.Kept)
	fmt.Fprintf(out, "Archived to %s (original at %s.bak)\n", res.ArchivePath, path)
	return nil
}

func newStateRebuildCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Replay the event log into the SQLite projection",
		Args:  cobra.NoArgs,
		RunE:  runStateRebuild,
	}
	cmd.SilenceUsage = true
	return cmd
}

func runStateRebuild(cmd *cobra.Command, _ []string) error {
	cfgPath, _ := cmd.Flags().GetString("config")
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return err
	}
	s, err := openStores(cfg)
	if err != nil {
		return err
	}
	defer s.Close()

	acquire := func() (func(), error) { return tryPipelineLock(cfg.Workspace.StateDir) }
	if err := state.Rebuild(context.Background(), s.Events, s.Proj, acquire); err != nil {
		return fmt.Errorf("rebuild projection: %w", err)
	}
	applied, _ := s.Proj.AppliedEventCount()
	out := cmd.OutOrStdout()
	if stateJSON(cmd) {
		return writeJSON(out, map[string]any{"applied_events": applied, "db": filepath.Join(cfg.Workspace.StateDir, "nxd.db")})
	}
	fmt.Fprintf(out, "Rebuilt projection from %d events (%s)\n", applied, filepath.Join(cfg.Workspace.StateDir, "nxd.db"))
	return nil
}

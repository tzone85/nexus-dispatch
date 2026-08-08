package cli

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/nlog"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// noticesPrinted dedupes config.Notices() output across multiple
// loadConfig calls in the same process. Without this guard the same
// "same-model review" warning printed up to 3 times per command:
// PersistentPreRun → loadConfig → Validate, then the command's own
// loadConfig → Validate, then any explicit cfg.Validate() call. We log
// each unique notice at most once per process — operators read it on
// startup, no point repeating it.
var (
	noticesMu      sync.Mutex
	noticesPrinted = map[string]struct{}{}
)

// logNoticesOnce prints any new notices the loaded config carries,
// suppressing repeats within the same process.
func logNoticesOnce(cfg config.Config) {
	noticesMu.Lock()
	defer noticesMu.Unlock()
	for _, n := range cfg.Notices() {
		if _, seen := noticesPrinted[n]; seen {
			continue
		}
		noticesPrinted[n] = struct{}{}
		log.Printf("[config] WARNING: %s", n)
	}
}

// stores bundles the event store and projection store opened from a config.
// Both must be closed by the caller.
type stores struct {
	Config config.Config
	Events state.EventStore
	Proj   *state.SQLiteStore
}

// loadStores loads configuration and opens both event and projection stores.
// The caller is responsible for closing both stores.
func loadStores(cfgPath string) (stores, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return stores{}, err
	}

	// Apply log-level / log-format from workspace config. main.go installs an
	// early env-based logger before config is available; after YAML load we
	// must reconfigure so workspace.log_level / log_format take effect.
	nlog.Reconfigure(cfg.Workspace.LogLevel, cfg.Workspace.LogFormat)

	stateDir := expandHome(cfg.Workspace.StateDir)

	// Ensure state directory exists (first run creates it).
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return stores{}, fmt.Errorf("create state directory %s: %w", stateDir, err)
	}

	es, err := state.NewFileStore(filepath.Join(stateDir, "events.jsonl"))
	if err != nil {
		return stores{}, fmt.Errorf("open event store: %w", err)
	}

	ps, err := state.NewSQLiteStore(filepath.Join(stateDir, "nxd.db"))
	if err != nil {
		es.Close()
		return stores{}, fmt.Errorf("open projection store: %w", err)
	}

	// Recover from projection desync. emitEventOrLog documents that a Project
	// failure after a successful Append is "loud-not-fatal" because "replaying
	// the event log will recover the projection on next startup" — this is that
	// recovery. When the projection has applied fewer events than the log holds
	// (a crash or a Project error mid-run left it behind), rebuild it from the
	// durable log so resume never re-dispatches an already-merged story.
	if err := rebuildProjectionIfBehind(es, ps); err != nil {
		es.Close()
		ps.Close()
		return stores{}, fmt.Errorf("rebuild projection: %w", err)
	}

	// Backfill acceptance_criteria for stories created before the column existed.
	allEvents, _ := es.List(state.EventFilter{Type: state.EventStoryCreated})
	ps.BackfillAcceptanceCriteria(allEvents)

	return stores{
		Config: cfg,
		Events: es,
		Proj:   ps,
	}, nil
}

// rebuildProjectionIfBehind rebuilds the projection from the event log when its
// reconciliation watermark has fallen behind the log length — the durable
// desync signature. A matched watermark means the projection is already a
// faithful function of the log, so the common path does no work.
func rebuildProjectionIfBehind(es state.EventStore, ps *state.SQLiteStore) error {
	logCount, err := es.Count(state.EventFilter{})
	if err != nil {
		return fmt.Errorf("count events: %w", err)
	}
	applied, err := ps.AppliedEventCount()
	if err != nil {
		return err
	}
	if applied >= logCount {
		return nil
	}
	log.Printf("[projection] watermark %d < log %d; rebuilding projection from event log", applied, logCount)
	return ps.RebuildFrom(context.Background(), es)
}

// Close releases both stores.
func (s stores) Close() {
	if s.Events != nil {
		s.Events.Close()
	}
	if s.Proj != nil {
		s.Proj.Close()
	}
}

// loadConfig loads configuration from the given path or falls back to defaults
// if the file is not found. H3: behavior depends on whether the caller passed
// an explicit path:
//   - empty path  → try ./nxd.yaml then ~/.nxd/config.yaml
//   - explicit    → fail loudly if the file doesn't exist or can't parse,
//     do NOT silently fall back to home directory
//
// This prevents `nxd --config /etc/nxd/prod.yaml ...` from quietly loading
// the wrong config when the prod file is missing.
func loadConfig(cfgPath string) (config.Config, error) {
	explicit := cfgPath != ""
	if !explicit {
		cfgPath = "nxd.yaml"
	}

	cfg, err := config.LoadFromFile(cfgPath)
	if err == nil {
		logNoticesOnce(cfg)
		return cfg, nil
	}

	if explicit {
		// Loud failure: caller passed --config and it doesn't work.
		return config.Config{}, fmt.Errorf("load config from %s: %w", cfgPath, err)
	}

	// Implicit path: try home-directory fallback before giving up.
	home, homeErr := os.UserHomeDir()
	if homeErr != nil {
		return config.Config{}, fmt.Errorf("load config from %s (no home dir for fallback): %w", cfgPath, err)
	}
	altPath := filepath.Join(home, ".nxd", "config.yaml")
	cfg, altErr := config.LoadFromFile(altPath)
	if altErr != nil {
		return config.Config{}, fmt.Errorf("no config: tried %s (%v) and %s (%v)", cfgPath, err, altPath, altErr)
	}
	logNoticesOnce(cfg)
	return cfg, nil
}

// expandHome replaces a leading ~ with the user's home directory.
func expandHome(path string) string {
	if len(path) == 0 {
		return path
	}
	if path[0] == '~' {
		home, err := os.UserHomeDir()
		if err != nil {
			return path
		}
		return filepath.Join(home, path[1:])
	}
	return path
}

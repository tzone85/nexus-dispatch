package cli

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
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

// stateDirOverride is set from the global --state-dir flag (root.go). When
// non-empty it replaces workspace.state_dir from every loaded config.
var stateDirOverride string

// loadStores loads configuration and opens both event and projection stores.
// The caller is responsible for closing both stores.
//
// When the projection watermark is behind the event log the projection is
// rebuilt — but only if the pipeline lock can be taken. A running pipeline
// (lock held by a live process) is appending events right now; rebuilding
// underneath it would race its own Project calls, so we log and proceed
// read-only instead. Commands that own the pipeline use loadStoresLocked.
func loadStores(cfgPath string) (stores, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return stores{}, err
	}
	s, err := openStores(cfg)
	if err != nil {
		return stores{}, err
	}
	if err := rebuildProjectionIfBehind(s.Events, s.Proj, cfg.Workspace.StateDir, tryPipelineLock); err != nil {
		s.Close()
		return stores{}, fmt.Errorf("rebuild projection: %w", err)
	}
	s.backfill()
	return s, nil
}

// loadStoresLocked acquires the pipeline lock FIRST, then opens the stores and
// rebuilds the projection if it is behind. Used by req/resume, which go on to
// write events: taking the lock before the rebuild check closes the window in
// which a concurrent read-only command could rebuild under a starting
// pipeline. The caller must Release the returned lock.
func loadStoresLocked(cfgPath string) (stores, *engine.PipelineLock, error) {
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		return stores{}, nil, err
	}
	if err := os.MkdirAll(cfg.Workspace.StateDir, 0o755); err != nil {
		return stores{}, nil, fmt.Errorf("create state directory %s: %w", cfg.Workspace.StateDir, err)
	}
	lock, err := engine.AcquireLock(cfg.Workspace.StateDir)
	if err != nil {
		return stores{}, nil, err
	}
	s, err := openStores(cfg)
	if err != nil {
		_ = lock.Release()
		return stores{}, nil, err
	}
	// We hold the lock, so the rebuild can never race a pipeline.
	held := func(string) (func(), error) { return func() {}, nil }
	if err := rebuildProjectionIfBehind(s.Events, s.Proj, cfg.Workspace.StateDir, held); err != nil {
		s.Close()
		_ = lock.Release()
		return stores{}, nil, fmt.Errorf("rebuild projection: %w", err)
	}
	s.backfill()
	return s, lock, nil
}

// openStores opens the event log and projection database under the config's
// (already normalised) state directory. No rebuild is attempted.
func openStores(cfg config.Config) (stores, error) {
	// Apply log-level / log-format from workspace config. main.go installs an
	// early env-based logger before config is available; after YAML load we
	// must reconfigure so workspace.log_level / log_format take effect.
	nlog.Reconfigure(cfg.Workspace.LogLevel, cfg.Workspace.LogFormat)

	stateDir := cfg.Workspace.StateDir

	// Ensure state directory exists (first run creates it).
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return stores{}, fmt.Errorf("create state directory %s: %w", stateDir, err)
	}

	es, err := state.NewFileStore(filepath.Join(stateDir, "events.jsonl"),
		state.WithMaxEventBytes(cfg.Workspace.MaxEventBytes),
		state.WithFsync(cfg.Workspace.FsyncEvents),
	)
	if err != nil {
		return stores{}, fmt.Errorf("open event store: %w", err)
	}

	ps, err := state.NewSQLiteStore(filepath.Join(stateDir, "nxd.db"))
	if err != nil {
		es.Close()
		return stores{}, fmt.Errorf("open projection store: %w", err)
	}
	return stores{Config: cfg, Events: es, Proj: ps}, nil
}

// backfill fills acceptance_criteria for stories created before the column
// existed.
func (s stores) backfill() {
	allEvents, _ := s.Events.List(state.EventFilter{Type: state.EventStoryCreated})
	s.Proj.BackfillAcceptanceCriteria(allEvents)
}

// lockAcquirer tries to take the pipeline lock for stateDir and returns a
// release func. It must return an error wrapping engine.ErrLockHeld when a
// live pipeline holds the lock. Injected so tests can fake a holder.
type lockAcquirer func(stateDir string) (release func(), err error)

// tryPipelineLock is the production lockAcquirer.
func tryPipelineLock(stateDir string) (func(), error) {
	lock, err := engine.TryAcquireLock(stateDir)
	if err != nil {
		return nil, err
	}
	return func() { _ = lock.Release() }, nil
}

// rebuildProjectionIfBehind rebuilds the projection from the event log when its
// reconciliation watermark has fallen behind the log length — the durable
// desync signature. A matched watermark means the projection is already a
// faithful function of the log, so the common path does no work.
//
// The rebuild runs under the pipeline lock obtained via acquire. If a live
// pipeline holds it, the rebuild is skipped with a log line and the caller
// proceeds against the (slightly stale) projection.
func rebuildProjectionIfBehind(es state.EventStore, ps *state.SQLiteStore, stateDir string, acquire lockAcquirer) error {
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
	release, err := acquire(stateDir)
	if err != nil {
		if errors.Is(err, engine.ErrLockHeld) {
			log.Printf("[projection] projection %d events behind; pipeline running, skipping rebuild (run `nxd state rebuild` later)",
				logCount-applied)
			return nil
		}
		return fmt.Errorf("acquire pipeline lock for rebuild: %w", err)
	}
	defer release()
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
		return applyStateDirOverride(cfg), nil
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
	return applyStateDirOverride(cfg), nil
}

// applyStateDirOverride replaces workspace.state_dir with the global
// --state-dir flag value (resolved against the working directory) when set.
func applyStateDirOverride(cfg config.Config) config.Config {
	if stateDirOverride != "" {
		cfg.Workspace.StateDir = config.NormalizeStateDir(stateDirOverride, "")
	}
	return cfg
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

package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

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

	// Backfill acceptance_criteria for stories created before the column existed.
	allEvents, _ := es.List(state.EventFilter{Type: state.EventStoryCreated})
	ps.BackfillAcceptanceCriteria(allEvents)

	return stores{
		Config: cfg,
		Events: es,
		Proj:   ps,
	}, nil
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

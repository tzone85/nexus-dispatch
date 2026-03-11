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
// if the file is not found.
func loadConfig(cfgPath string) (config.Config, error) {
	if cfgPath == "" {
		cfgPath = "nxd.yaml"
	}

	cfg, err := config.LoadFromFile(cfgPath)
	if err != nil {
		// Try home directory fallback
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return config.Config{}, fmt.Errorf("load config: %w", err)
		}
		altPath := filepath.Join(home, ".nxd", "config.yaml")
		cfg, err = config.LoadFromFile(altPath)
		if err != nil {
			return config.Config{}, fmt.Errorf("load config from %s or %s: %w", cfgPath, altPath, err)
		}
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

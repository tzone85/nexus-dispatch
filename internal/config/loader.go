package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// DefaultConfig returns a Config populated with sensible defaults.
// The returned value passes Validate() without modification.
func DefaultConfig() Config {
	return Config{
		Version: "1.0",
		Workspace: WorkspaceConfig{
			StateDir:         "~/.nxd",
			Backend:          "sqlite",
			LogLevel:         "info",
			LogRetentionDays: 30,
		},
		Routing: RoutingConfig{
			JuniorMaxComplexity:           3,
			IntermediateMaxComplexity:     5,
			MaxRetriesBeforeEscalation:    2,
			MaxQAFailuresBeforeEscalation: 3,
		},
		Monitor: MonitorConfig{
			PollIntervalMs:         10000,
			StuckThresholdS:        120,
			ContextFreshnessTokens: 150000,
		},
		Cleanup: CleanupConfig{
			WorktreePrune:       "immediate",
			BranchRetentionDays: 7,
			LogArchive:          "file",
		},
		Merge: MergeConfig{
			AutoMerge:  true,
			BaseBranch: "main",
			Mode:       "local",
		},
	}
}

// LoadFromFile reads a YAML configuration file, overlays it on top of
// DefaultConfig (so unset fields keep their defaults), validates, and
// returns the result.
func LoadFromFile(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("reading config file: %w", err)
	}

	cfg := DefaultConfig()

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config YAML: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

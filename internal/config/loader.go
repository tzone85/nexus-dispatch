package config

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// gemma4Default returns a ModelConfig preset for the Gemma 4 e4b model
// using the local Ollama provider. Fully offline — no API key required.
func gemma4Default(maxTokens int) ModelConfig {
	return ModelConfig{
		Provider:  "ollama",
		Model:     "gemma4:e4b",
		MaxTokens: maxTokens,
	}
}

// DefaultConfig returns a Config populated with sensible defaults.
// The returned value passes Validate() without modification.
func DefaultConfig() Config {
	return Config{
		Version: "1.0",
		Workspace: WorkspaceConfig{
			StateDir:            "~/.nxd",
			Backend:             "sqlite",
			LogLevel:            "info",
			LogRetentionDays:    30,
			UpdateCheck:         true,
			UpdateIntervalHours: 48,
		},
		Models: ModelsConfig{
			TechLead:     gemma4Default(16000),
			Senior:       gemma4Default(8000),
			Intermediate: gemma4Default(4000),
			Junior:       gemma4Default(4000),
			QA:           gemma4Default(8000),
			Supervisor:   gemma4Default(4000),
			Manager:      gemma4Default(8000),
			Investigator: gemma4Default(16000),
		},
		Routing: RoutingConfig{
			JuniorMaxComplexity:           3,
			IntermediateMaxComplexity:     5,
			MaxRetriesBeforeEscalation:    2,
			MaxQAFailuresBeforeEscalation: 3,
			MaxSeniorRetries:              2,
			MaxManagerAttempts:            2,
		},
		Monitor: MonitorConfig{
			PollIntervalMs:         10000,
			StuckThresholdS:        120,
			ContextFreshnessTokens: 150000,
			PipelineTimeoutS:       900, // 15 min — room for slow local LLM calls + conflict resolution
		},
		Cleanup: CleanupConfig{
			WorktreePrune:          "immediate",
			BranchRetentionDays:    7,
			LogArchive:             "file",
			DeleteDanglingBranches: true,
		},
		Merge: MergeConfig{
			AutoMerge:         true,
			ReviewBeforeMerge: false,
			BaseBranch:        "main",
			Mode:              "local",
			PRTemplate:        "## Story: {story_id}\n{description}\n### Acceptance Criteria\n{acceptance_criteria}\n",
		},
		Planning: PlanningConfig{
			SequentialFilePatterns: []string{"package.json", "*.config.*", "src/core/*"},
			MaxStoryComplexity:     5,
		},
		Billing: BillingConfig{
			DefaultRate: 150.0,
			Currency:    "USD",
			HoursPerPoint: map[int][2]float64{
				1:  {0.5, 1.0},
				2:  {1.0, 2.0},
				3:  {2.0, 3.0},
				5:  {3.0, 5.0},
				8:  {5.0, 8.0},
				13: {8.0, 13.0},
			},
			LLMCosts: LLMCostConfig{
				Mode: "subscription",
			},
		},
		Memory: MemoryConfig{
			Enabled: true,
		},
		Investigation: InvestigationConfig{
			CommandAllowlist: []string{
				"ls", "find", "wc", "grep", "cat", "head", "tail",
				"git log", "git status", "git diff", "git ls-files", "git blame", "git branch",
				"go build", "go test", "go mod", "go vet",
				"npm test", "npm run", "npm ls",
				"python -m pytest", "python -m py_compile",
				"make",
				"docker ps", "docker-compose config",
			},
		},
		QA: QAConfig{
			SuccessCriteria: []SuccessCriterion{
				{Kind: "command_succeeds", Value: "go build ./..."},
				{Kind: "command_succeeds", Value: "go vet ./..."},
				{Kind: "test_passes", Value: "go test ./..."},
			},
		},
		Security: SecurityConfig{
			// The pipeline gate pauses a build only on CRITICAL findings (leaked
			// secrets, LLM-confirmed injection/hardcoded credentials) so it is
			// high-signal and does not stall builds on context-dependent SAST
			// noise. The standalone `nxd security scan` reports high/medium too
			// (default --min high). Tighten the gate via security.gate_severity.
			GateSeverity: "critical",
			AutoLearn:    true,
		},
		// DDD + TDD on by default. Per-requirement opt-out via the
		// `methodology: relaxed` directive in the requirement text or a
		// `.spec/methodology.md` file. Operators can disable globally by
		// setting `methodology.tdd: false` in nxd.yaml.
		Methodology: MethodologyConfig{
			DDD:            true,
			TDD:            true,
			MinCoveragePct: 80,
			AllowOverride:  true,
		},
		Plugins: PluginConfig{},
		Runtimes: map[string]RuntimeConfig{
			"aider": {
				Command: "aider",
				Args:    []string{"--model", "ollama_chat/deepseek-coder-v2:latest", "--no-auto-commits"},
				Models:  []string{"deepseek-coder-v2", "qwen2.5-coder"},
				Detection: RuntimeDetection{
					IdlePattern:       "^>",
					PermissionPattern: `\[Y/n\]`,
				},
			},
			"claude-code": {
				Command: "claude",
				Args:    []string{"--dangerously-skip-permissions"},
				Models:  []string{"opus-4", "sonnet-4", "haiku-4"},
				Detection: RuntimeDetection{
					IdlePattern:       `^\$\s*$`,
					PermissionPattern: `\[Y/n\]`,
					PlanModePattern:   "Plan mode",
				},
			},
			"codex": {
				Command: "codex",
				Args:    []string{"--full-auto"},
				Models:  []string{"o3", "o4-mini"},
				Detection: RuntimeDetection{
					IdlePattern:       "Codex>",
					PermissionPattern: "approve|deny",
				},
			},
			"gemma": {
				Native:           true,
				MaxIterations:    20,
				Models:           []string{"gemma4"},
				CommandAllowlist: []string{"go build ./...", "go test ./...", "npm test", "npm run build", "make", "make test"},
			},
		},
	}
}

// DefaultYAML marshals DefaultConfig to YAML bytes suitable for writing
// as an nxd.yaml configuration file.
func DefaultYAML() ([]byte, error) {
	cfg := DefaultConfig()
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshalling default config: %w", err)
	}

	header := []byte("# NXD configuration — generated by 'nxd init'\n" +
		"# See https://github.com/tzone85/nexus-dispatch for documentation.\n\n")
	return append(header, data...), nil
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

	if err := CheckSchemaVersion(cfg.Version, path); err != nil {
		return Config{}, err
	}

	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config validation: %w", err)
	}

	return cfg, nil
}

// CheckSchemaVersion compares the loaded YAML's `version` field against
// CurrentSchemaVersion. Behavior:
//   - empty            → log hint, succeed
//   - equal            → succeed silently
//   - older minor/patch → log advisory, succeed (this build still
//     understands the older shape because schema bumps are minor by default)
//   - older major       → succeed with a migration warning
//   - newer major       → fail (the YAML expects features this binary lacks)
//
// The path argument is included in messages so operators with multiple
// config files can identify the offender. Visible-for-testing.
func CheckSchemaVersion(have, path string) error {
	if have == "" {
		log.Printf("[config] %s has no `version:` field — pin it to %q to silence this hint", path, CurrentSchemaVersion)
		return nil
	}
	if have == CurrentSchemaVersion {
		return nil
	}
	haveMajor, haveOK := majorOf(have)
	curMajor, _ := majorOf(CurrentSchemaVersion)
	if !haveOK {
		log.Printf("[config] %s has unparseable version %q — proceeding with current schema", path, have)
		return nil
	}
	switch {
	case haveMajor > curMajor:
		return fmt.Errorf("config schema version %s in %s is newer than this binary supports (v%d). Upgrade nxd or pin schema to %s",
			have, path, curMajor, CurrentSchemaVersion)
	case haveMajor < curMajor:
		log.Printf("[config] %s schema is v%d but build expects v%d — running in compat mode; consider upgrading config to %s",
			path, haveMajor, curMajor, CurrentSchemaVersion)
	default:
		log.Printf("[config] %s schema %s differs from build's %s — proceeding (minor/patch drift)", path, have, CurrentSchemaVersion)
	}
	return nil
}

// majorOf parses the leading integer of a semver-ish string ("1.0", "2",
// "v1.2.3"). Returns (n, true) on success.
func majorOf(v string) (int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return 0, false
	}
	end := len(v)
	for i, c := range v {
		if c == '.' || c == '-' || c == ' ' {
			end = i
			break
		}
	}
	n, err := strconv.Atoi(v[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

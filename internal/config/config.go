// Package config provides configuration types, loading, defaults, and
// validation for NXD (Nexus Dispatch).
package config

import (
	"fmt"
	"regexp"
	"strings"
)

// MemoryConfig controls the MemPalace context-memory subsystem.
type MemoryConfig struct {
	Enabled    bool   `yaml:"enabled"`
	PalacePath string `yaml:"palace_path,omitempty"`
}

// Config is the top-level NXD configuration.
type Config struct {
	Version   string                   `yaml:"version"`
	Workspace WorkspaceConfig          `yaml:"workspace"`
	Models    ModelsConfig             `yaml:"models"`
	Routing   RoutingConfig            `yaml:"routing"`
	Monitor   MonitorConfig            `yaml:"monitor"`
	Cleanup   CleanupConfig            `yaml:"cleanup"`
	Merge     MergeConfig              `yaml:"merge"`
	Planning  PlanningConfig           `yaml:"planning"`
	Billing    BillingConfig            `yaml:"billing"`
	Controller ControllerConfig         `yaml:"controller"`
	Memory        MemoryConfig             `yaml:"memory"`
	Investigation InvestigationConfig      `yaml:"investigation"`
	QA            QAConfig                 `yaml:"qa"`
	Runtimes      map[string]RuntimeConfig `yaml:"runtimes"`
	Plugins       PluginConfig             `yaml:"plugins"`
}

// PlanningConfig controls how the planner decomposes requirements into stories.
type PlanningConfig struct {
	SequentialFilePatterns []string `yaml:"sequential_file_patterns"`
	MaxStoryComplexity     int      `yaml:"max_story_complexity"`
	Godmode                bool     `yaml:"godmode"`
}

// WorkspaceConfig holds workspace-level settings.
type WorkspaceConfig struct {
	StateDir            string `yaml:"state_dir"`
	Backend             string `yaml:"backend"`
	LogLevel            string `yaml:"log_level"`
	LogRetentionDays    int    `yaml:"log_retention_days"`
	UpdateCheck         bool   `yaml:"update_check"`
	UpdateIntervalHours int    `yaml:"update_interval_hours"`
}

// ModelConfig describes a single LLM model binding.
type ModelConfig struct {
	Provider          string `yaml:"provider"`
	Model             string `yaml:"model"`
	MaxTokens         int    `yaml:"max_tokens"`
	GoogleModel       string `yaml:"google_model,omitempty"`
	FallbackCooldownS int    `yaml:"fallback_cooldown_s,omitempty"`
}

// ModelsConfig maps agent roles to their model bindings.
type ModelsConfig struct {
	TechLead     ModelConfig `yaml:"tech_lead"`
	Senior       ModelConfig `yaml:"senior"`
	Intermediate ModelConfig `yaml:"intermediate"`
	Junior       ModelConfig `yaml:"junior"`
	QA           ModelConfig `yaml:"qa"`
	Supervisor   ModelConfig `yaml:"supervisor"`
	Manager      ModelConfig `yaml:"manager"`
	Investigator ModelConfig `yaml:"investigator"`
}

// All returns every role→ModelConfig pair for iteration.
func (m ModelsConfig) All() map[string]ModelConfig {
	return map[string]ModelConfig{
		"tech_lead": m.TechLead, "senior": m.Senior,
		"intermediate": m.Intermediate, "junior": m.Junior,
		"qa": m.QA, "supervisor": m.Supervisor, "manager": m.Manager,
		"investigator": m.Investigator,
	}
}

// RoutingConfig controls how tasks are assigned to agent tiers.
type RoutingConfig struct {
	JuniorMaxComplexity           int `yaml:"junior_max_complexity"`
	IntermediateMaxComplexity     int `yaml:"intermediate_max_complexity"`
	MaxRetriesBeforeEscalation    int `yaml:"max_retries_before_escalation"`
	MaxQAFailuresBeforeEscalation int `yaml:"max_qa_failures_before_escalation"`
	MaxSeniorRetries              int `yaml:"max_senior_retries"`
	MaxManagerAttempts            int `yaml:"max_manager_attempts"`
}

// MonitorConfig controls the supervisor monitoring loop.
type MonitorConfig struct {
	PollIntervalMs         int `yaml:"poll_interval_ms"`
	StuckThresholdS        int `yaml:"stuck_threshold_s"`
	ContextFreshnessTokens int `yaml:"context_freshness_tokens"`
}

// ControllerConfig configures the periodic active controller.
type ControllerConfig struct {
	Enabled           bool `yaml:"enabled"`
	IntervalS         int  `yaml:"interval_s"`
	MaxStuckDurationS int  `yaml:"max_stuck_duration_s"`
	AutoCancel        bool `yaml:"auto_cancel"`
	AutoRestart       bool `yaml:"auto_restart"`
	AutoReprioritize  bool `yaml:"auto_reprioritize"`
	MaxActionsPerTick int  `yaml:"max_actions_per_tick"`
	CooldownS         int  `yaml:"cooldown_s"`
}

// CleanupConfig controls post-task cleanup behaviour.
type CleanupConfig struct {
	WorktreePrune       string `yaml:"worktree_prune"`
	BranchRetentionDays int    `yaml:"branch_retention_days"`
	LogArchive          string `yaml:"log_archive"`
}

// MergeConfig controls how completed work is merged.
type MergeConfig struct {
	AutoMerge         bool   `yaml:"auto_merge"`
	ReviewBeforeMerge bool   `yaml:"review_before_merge"`
	BaseBranch        string `yaml:"base_branch"`
	Mode              string `yaml:"mode"` // "local" or "github"
	PRTemplate        string `yaml:"pr_template"`
}

// BillingConfig controls cost estimation and client quoting.
type BillingConfig struct {
	DefaultRate   float64            `yaml:"default_rate"`
	Currency      string             `yaml:"currency"`
	HoursPerPoint map[int][2]float64 `yaml:"hours_per_point"`
	LLMCosts      LLMCostConfig      `yaml:"llm_costs"`
}

// LLMCostConfig tracks LLM API costs.
type LLMCostConfig struct {
	Mode  string              `yaml:"mode"`
	Rates map[string]TokenRate `yaml:"rates,omitempty"`
}

// TokenRate defines per-token pricing for a model.
type TokenRate struct {
	InputPer1K  float64 `yaml:"input_per_1k"`
	OutputPer1K float64 `yaml:"output_per_1k"`
}

// QAConfig holds quality-assurance settings including declarative success criteria.
type QAConfig struct {
	SuccessCriteria []SuccessCriterion `yaml:"success_criteria"`
}

// SuccessCriterion defines a single declarative success check in config YAML.
type SuccessCriterion struct {
	Kind    string `yaml:"kind"`
	Value   string `yaml:"value,omitempty"`
	Path    string `yaml:"path,omitempty"`
	Message string `yaml:"message,omitempty"`
}

// validCriteriaKinds is the set of allowed QA criteria kinds.
var validCriteriaKinds = map[string]bool{
	"output_contains":     true,
	"output_not_contains": true,
	"file_exists":         true,
	"file_contains":       true,
	"file_not_empty":      true,
	"exit_code_zero":      true,
}

// InvestigationConfig controls how the investigation agent operates.
type InvestigationConfig struct {
	CommandAllowlist []string `yaml:"command_allowlist"`
}

// RuntimeDetection holds patterns used to detect runtime states.
type RuntimeDetection struct {
	IdlePattern       string `yaml:"idle_pattern"`
	PermissionPattern string `yaml:"permission_pattern"`
	PlanModePattern   string `yaml:"plan_mode_pattern,omitempty"`
}

// RuntimeConfig describes an external AI coding runtime.
type RuntimeConfig struct {
	Command          string           `yaml:"command"`
	Args             []string         `yaml:"args"`
	Models           []string         `yaml:"models"`
	Detection        RuntimeDetection `yaml:"detection"`
	Native           bool             `yaml:"native,omitempty"`
	MaxIterations    int              `yaml:"max_iterations,omitempty"`
	CommandAllowlist []string         `yaml:"command_allowlist,omitempty"`
	Concurrency      int              `yaml:"concurrency,omitempty"`
}

// validBackends is the set of allowed workspace backends.
var validBackends = map[string]bool{
	"dolt":   true,
	"sqlite": true,
}

// validLogLevels is the set of allowed log levels.
var validLogLevels = map[string]bool{
	"debug": true,
	"info":  true,
	"warn":  true,
	"error": true,
}

// validWorktreePrune is the set of allowed worktree prune modes.
var validWorktreePrune = map[string]bool{
	"immediate": true,
	"deferred":  true,
}

// validLogArchive is the set of allowed log archive modes.
var validLogArchive = map[string]bool{
	"dolt": true,
	"file": true,
	"none": true,
}

// validMergeModes is the set of allowed merge modes.
var validMergeModes = map[string]bool{
	"local":  true,
	"github": true,
}

// validProviders is the set of allowed model providers.
var validProviders = map[string]bool{
	"ollama": true, "anthropic": true, "openai": true,
	"google": true, "google+ollama": true,
}

// validLLMModes is the set of allowed LLM cost tracking modes.
var validLLMModes = map[string]bool{
	"subscription": true,
	"per_token":    true,
}

// Validate checks that all configuration values are within allowed ranges.
// It returns an error describing the first invalid value found.
func (c Config) Validate() error {
	if !validBackends[c.Workspace.Backend] {
		return fmt.Errorf("workspace.backend must be \"dolt\" or \"sqlite\", got %q", c.Workspace.Backend)
	}

	if !validLogLevels[c.Workspace.LogLevel] {
		return fmt.Errorf("workspace.log_level must be one of debug, info, warn, error; got %q", c.Workspace.LogLevel)
	}

	if !validWorktreePrune[c.Cleanup.WorktreePrune] {
		return fmt.Errorf("cleanup.worktree_prune must be \"immediate\" or \"deferred\", got %q", c.Cleanup.WorktreePrune)
	}

	if !validLogArchive[c.Cleanup.LogArchive] {
		return fmt.Errorf("cleanup.log_archive must be \"dolt\", \"file\", or \"none\"; got %q", c.Cleanup.LogArchive)
	}

	if !validMergeModes[c.Merge.Mode] {
		return fmt.Errorf("merge.mode must be \"local\" or \"github\", got %q", c.Merge.Mode)
	}

	if c.Routing.JuniorMaxComplexity < 1 || c.Routing.JuniorMaxComplexity > 13 {
		return fmt.Errorf("routing.junior_max_complexity must be 1-13, got %d", c.Routing.JuniorMaxComplexity)
	}

	if c.Routing.IntermediateMaxComplexity < c.Routing.JuniorMaxComplexity {
		return fmt.Errorf(
			"routing.intermediate_max_complexity (%d) must be >= junior_max_complexity (%d)",
			c.Routing.IntermediateMaxComplexity, c.Routing.JuniorMaxComplexity,
		)
	}

	if c.Routing.IntermediateMaxComplexity > 13 {
		return fmt.Errorf("routing.intermediate_max_complexity must be <= 13, got %d", c.Routing.IntermediateMaxComplexity)
	}

	// Validate model providers and google_model requirement.
	for role, mc := range c.Models.All() {
		if mc.Provider != "" && !validProviders[mc.Provider] {
			return fmt.Errorf("models.%s.provider %q is not a valid provider", role, mc.Provider)
		}
		if strings.Contains(mc.Provider, "google") && mc.GoogleModel == "" {
			return fmt.Errorf("models.%s.google_model is required when provider contains \"google\"", role)
		}
	}

	if c.Workspace.UpdateIntervalHours < 0 {
		return fmt.Errorf("workspace.update_interval_hours must be >= 0, got %d", c.Workspace.UpdateIntervalHours)
	}

	// Validate billing configuration.
	if c.Billing.DefaultRate < 0 {
		return fmt.Errorf("billing.default_rate must be >= 0, got %f", c.Billing.DefaultRate)
	}
	if c.Billing.Currency == "" {
		return fmt.Errorf("billing.currency must not be empty")
	}
	if !validLLMModes[c.Billing.LLMCosts.Mode] {
		return fmt.Errorf("billing.llm_costs.mode must be \"subscription\" or \"per_token\", got %q", c.Billing.LLMCosts.Mode)
	}
	for pts, hrs := range c.Billing.HoursPerPoint {
		if hrs[0] > hrs[1] {
			return fmt.Errorf("billing.hours_per_point[%d]: low (%.1f) must be <= high (%.1f)", pts, hrs[0], hrs[1])
		}
	}

	// Validate QA success criteria kinds.
	for i, sc := range c.QA.SuccessCriteria {
		if !validCriteriaKinds[sc.Kind] {
			return fmt.Errorf("qa.success_criteria[%d].kind %q is not a valid criterion kind", i, sc.Kind)
		}
	}

	// Validate native runtime constraints.
	for name, rt := range c.Runtimes {
		if rt.Native {
			if rt.MaxIterations <= 0 {
				return fmt.Errorf("runtimes.%s.max_iterations must be > 0 for native runtimes", name)
			}
			if len(rt.CommandAllowlist) == 0 {
				return fmt.Errorf("runtimes.%s.command_allowlist must be non-empty for native runtimes", name)
			}
		}

		// Validate detection regex patterns compile without error (prevents
		// ReDoS and catches invalid patterns early at config load time).
		if p := rt.Detection.IdlePattern; p != "" {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("runtimes.%s.detection.idle_pattern is invalid regex: %w", name, err)
			}
		}
		if p := rt.Detection.PermissionPattern; p != "" {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("runtimes.%s.detection.permission_pattern is invalid regex: %w", name, err)
			}
		}
		if p := rt.Detection.PlanModePattern; p != "" {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("runtimes.%s.detection.plan_mode_pattern is invalid regex: %w", name, err)
			}
		}
	}

	return nil
}

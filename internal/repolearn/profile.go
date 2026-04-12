// Package repolearn provides iterative deep analysis of tracked repositories.
// It builds a persistent RepoProfile that agents consume at dispatch time,
// eliminating the need for early-iteration codebase archaeology.
//
// The analysis pipeline has three passes:
//
//	Pass 1 — Static scan: marker files, configs, directory tree (no git, no LLM)
//	Pass 2 — Git history: commit patterns, contributors, churn hotspots
//	Pass 3 — Deep analysis: LLM-assisted summary and architectural notes
//
// Each pass is idempotent and increments the profile's Iteration counter.
package repolearn

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProfileFilename is the name of the persisted profile JSON file.
const ProfileFilename = "repo-profile.json"

// RepoProfile is the persisted knowledge about a tracked repository.
// It is incrementally populated across analysis passes and consumed by
// the executor and planner to enrich agent prompts.
type RepoProfile struct {
	RepoPath   string    `json:"repo_path"`
	AnalyzedAt time.Time `json:"analyzed_at"`
	Iteration  int       `json:"iteration"` // monotonically increasing per pass

	// Pass 1 — static scan
	TechStack   TechStackDetail `json:"tech_stack"`
	Build       BuildConfig     `json:"build"`
	Test        TestConfig      `json:"test"`
	Structure   RepoStructure   `json:"structure"`
	CI          CIConfig        `json:"ci"`

	// Pass 2 — git history
	Conventions  Conventions  `json:"conventions"`
	Dependencies []Dependency `json:"dependencies"`

	// Pass 1/2/3 — accumulated signals
	Signals []Signal `json:"signals"`

	// Tracks which passes have been completed.
	CompletedPasses []int `json:"completed_passes"`
}

// TechStackDetail extends the shallow git.TechStack with richer metadata.
type TechStackDetail struct {
	PrimaryLanguage    string   `json:"primary_language"`
	PrimaryFramework   string   `json:"primary_framework,omitempty"`
	PrimaryBuildTool   string   `json:"primary_build_tool"`
	LanguageVersion    string   `json:"language_version,omitempty"`
	SecondaryLanguages []string `json:"secondary_languages,omitempty"`
}

// BuildConfig holds detected build and lint commands.
type BuildConfig struct {
	BuildCommand string   `json:"build_command,omitempty"`
	LintCommand  string   `json:"lint_command,omitempty"`
	FormatCommand string  `json:"format_command,omitempty"`
	MakeTargets  []string `json:"make_targets,omitempty"` // available Makefile targets
}

// TestConfig holds detected test commands and conventions.
type TestConfig struct {
	TestCommand    string   `json:"test_command,omitempty"`
	TestFramework  string   `json:"test_framework,omitempty"`
	CoverageTool   string   `json:"coverage_tool,omitempty"`
	TestFilePattern string  `json:"test_file_pattern,omitempty"` // e.g. "*_test.go", "test_*.py"
	TestDirs       []string `json:"test_dirs,omitempty"`         // e.g. ["test/", "tests/", "spec/"]
}

// CIConfig holds detected CI/CD system information.
type CIConfig struct {
	System    string   `json:"system,omitempty"`    // "github_actions", "gitlab_ci", "circleci", etc.
	Files     []string `json:"files,omitempty"`     // paths to CI config files
}

// RepoStructure describes the top-level directory layout.
type RepoStructure struct {
	EntryPoints []EntryPoint `json:"entry_points,omitempty"` // main files, cmd/ dirs
	TopDirs     []DirInfo    `json:"top_dirs,omitempty"`
	TotalFiles  int          `json:"total_files"`
	SourceFiles int          `json:"source_files"`
}

// EntryPoint is a main package or executable entry point.
type EntryPoint struct {
	Path string `json:"path"`
	Kind string `json:"kind"` // "main", "cmd", "script", "handler"
}

// DirInfo annotates a top-level directory with its purpose.
type DirInfo struct {
	Name    string `json:"name"`
	Purpose string `json:"purpose"` // inferred purpose: "source", "test", "config", "docs", "vendor", "build", "scripts", "generated"
	Files   int    `json:"files"`
}

// Conventions captures coding and workflow conventions detected from git history.
type Conventions struct {
	CommitFormat     string `json:"commit_format,omitempty"`      // "conventional", "freeform", "ticket-prefix"
	BranchPattern    string `json:"branch_pattern,omitempty"`     // e.g. "feature/*, fix/*"
	ContributorCount int    `json:"contributor_count"`
	CommitCount      int    `json:"commit_count"`
	ActiveDays       int    `json:"active_days"`                  // days with at least 1 commit
	ChurnHotspots    []ChurnHotspot `json:"churn_hotspots,omitempty"` // most-changed files
}

// ChurnHotspot identifies a frequently-changed file.
type ChurnHotspot struct {
	Path    string `json:"path"`
	Changes int    `json:"changes"` // number of commits touching this file
}

// Dependency represents a project dependency.
type Dependency struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Kind    string `json:"kind"` // "direct", "dev", "indirect"
}

// Signal represents a noteworthy pattern or observation about the repo.
type Signal struct {
	Kind    string `json:"kind"`    // "monorepo", "generated_code", "vendored", "no_tests", "large_file", "stale", "llm_summary", etc.
	Message string `json:"message"` // human-readable description
	Path    string `json:"path,omitempty"`
}

// PassCompleted reports whether the given pass number (1, 2, or 3) has been run.
func (p *RepoProfile) PassCompleted(pass int) bool {
	for _, completed := range p.CompletedPasses {
		if completed == pass {
			return true
		}
	}
	return false
}

// MarkPass records that a pass has been completed and bumps the iteration.
func (p *RepoProfile) MarkPass(pass int) {
	if !p.PassCompleted(pass) {
		p.CompletedPasses = append(p.CompletedPasses, pass)
	}
	p.Iteration++
	p.AnalyzedAt = time.Now().UTC()
}

// AddSignal appends a signal, deduplicating by kind+path.
func (p *RepoProfile) AddSignal(kind, message, path string) {
	for _, s := range p.Signals {
		if s.Kind == kind && s.Path == path {
			return // already recorded
		}
	}
	p.Signals = append(p.Signals, Signal{Kind: kind, Message: message, Path: path})
}

// Summary returns a concise multi-line summary suitable for injection into
// agent prompts. It covers tech stack, build/test commands, structure, and
// key signals.
func (p *RepoProfile) Summary() string {
	if p.TechStack.PrimaryLanguage == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString("## Repository Profile\n")

	// Tech stack
	b.WriteString(fmt.Sprintf("Language: %s", p.TechStack.PrimaryLanguage))
	if p.TechStack.LanguageVersion != "" {
		b.WriteString(fmt.Sprintf(" %s", p.TechStack.LanguageVersion))
	}
	if p.TechStack.PrimaryBuildTool != "" {
		b.WriteString(fmt.Sprintf(" (build: %s)", p.TechStack.PrimaryBuildTool))
	}
	b.WriteString("\n")
	if p.TechStack.PrimaryFramework != "" {
		b.WriteString(fmt.Sprintf("Framework: %s\n", p.TechStack.PrimaryFramework))
	}
	if len(p.TechStack.SecondaryLanguages) > 0 {
		b.WriteString(fmt.Sprintf("Also uses: %s\n", strings.Join(p.TechStack.SecondaryLanguages, ", ")))
	}

	// Build commands
	if p.Build.BuildCommand != "" {
		b.WriteString(fmt.Sprintf("Build: %s\n", p.Build.BuildCommand))
	}
	if p.Build.LintCommand != "" {
		b.WriteString(fmt.Sprintf("Lint: %s\n", p.Build.LintCommand))
	}
	if p.Test.TestCommand != "" {
		b.WriteString(fmt.Sprintf("Test: %s\n", p.Test.TestCommand))
	}
	if p.Test.TestFramework != "" {
		b.WriteString(fmt.Sprintf("Test framework: %s\n", p.Test.TestFramework))
	}

	// CI
	if p.CI.System != "" {
		b.WriteString(fmt.Sprintf("CI: %s\n", p.CI.System))
	}

	// Structure
	b.WriteString(fmt.Sprintf("Files: %d total, %d source\n", p.Structure.TotalFiles, p.Structure.SourceFiles))
	if len(p.Structure.EntryPoints) > 0 {
		paths := make([]string, 0, len(p.Structure.EntryPoints))
		for _, ep := range p.Structure.EntryPoints {
			paths = append(paths, ep.Path)
		}
		b.WriteString(fmt.Sprintf("Entry points: %s\n", strings.Join(paths, ", ")))
	}

	// Conventions (from Pass 2)
	if p.Conventions.ContributorCount > 0 {
		b.WriteString(fmt.Sprintf("Contributors: %d, Commits: %d\n", p.Conventions.ContributorCount, p.Conventions.CommitCount))
	}
	if p.Conventions.CommitFormat != "" {
		b.WriteString(fmt.Sprintf("Commit style: %s\n", p.Conventions.CommitFormat))
	}

	// Key signals
	for _, s := range p.Signals {
		if s.Kind == "llm_summary" {
			b.WriteString(fmt.Sprintf("\n%s\n", s.Message))
		}
	}

	return b.String()
}

// SaveProfile writes a RepoProfile to the given directory as repo-profile.json.
func SaveProfile(dir string, profile *RepoProfile) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}

	data, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal profile: %w", err)
	}

	path := filepath.Join(dir, ProfileFilename)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write profile: %w", err)
	}
	return nil
}

// LoadProfile reads a RepoProfile from the given directory.
// Returns a zero-value profile and a nil error if the file does not exist.
func LoadProfile(dir string) (*RepoProfile, error) {
	path := filepath.Join(dir, ProfileFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &RepoProfile{}, nil
		}
		return nil, fmt.Errorf("read profile: %w", err)
	}

	var profile RepoProfile
	if err := json.Unmarshal(data, &profile); err != nil {
		return nil, fmt.Errorf("parse profile: %w", err)
	}
	return &profile, nil
}

// ProfilePath returns the full path to the profile JSON file in the given
// project state directory.
func ProfilePath(projectDir string) string {
	return filepath.Join(projectDir, ProfileFilename)
}

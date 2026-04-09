package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	nxdgit "github.com/tzone85/nexus-dispatch/internal/git"
	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// RepoProfile holds the heuristic classification of a repository, produced
// entirely from local file-system and git inspection (no LLM call).
type RepoProfile struct {
	IsExisting      bool
	Language        string
	BuildTool       string
	SourceFileCount int
	TestFileCount   int
	CommitCount     int
	TopDirs         []string
	HasCI           bool
	HasDocker       bool
	BuildHealthy    bool
	TestsExist      bool
}

// RequirementClassification captures the LLM's assessment of a requirement's
// intent. Valid Type values: "feature", "bugfix", "refactor", "infrastructure".
type RequirementClassification struct {
	Type       string   `json:"type"`
	Confidence float64  `json:"confidence"`
	Signals    []string `json:"signals"`
}

// RequirementContext combines the heuristic repo profile with the LLM
// classification and derives convenience booleans used by downstream stages.
type RequirementContext struct {
	Repo           RepoProfile
	Classification RequirementClassification
	Report         any // *InvestigationReport — defined in Task 4

	// Derived convenience booleans.
	IsExisting bool
	IsBugFix   bool
	IsRefactor bool
	IsInfra    bool
}

// NewRequirementContext builds a RequirementContext from a RepoProfile and
// RequirementClassification, setting the derived boolean fields.
func NewRequirementContext(repo RepoProfile, class RequirementClassification) RequirementContext {
	return RequirementContext{
		Repo:           repo,
		Classification: class,
		IsExisting:     repo.IsExisting,
		IsBugFix:       class.Type == "bugfix",
		IsRefactor:     class.Type == "refactor",
		IsInfra:        class.Type == "infrastructure",
	}
}

// sourceExtensions maps language file extensions to whether they are source
// code. Used by the file walker to count source vs test files.
var sourceExtensions = map[string]bool{
	".go":    true,
	".js":    true,
	".ts":    true,
	".tsx":   true,
	".jsx":   true,
	".py":    true,
	".java":  true,
	".rs":    true,
	".rb":    true,
	".c":     true,
	".cpp":   true,
	".h":     true,
	".cs":    true,
	".swift": true,
	".kt":    true,
	".scala": true,
	".php":   true,
}

// skipDirs contains directory names that should be skipped during the file
// walk. These are typically dependency caches or VCS internals.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	"__pycache__":  true,
	".tox":         true,
	"target":       true,
	"build":        true,
	"dist":         true,
}

// ClassifyRepo performs a pure-heuristic classification of the repository at
// repoPath. It inspects files, git history, CI config, and Docker config. No
// LLM call is made.
func ClassifyRepo(repoPath string) RepoProfile {
	stack := nxdgit.ScanRepo(repoPath)

	profile := RepoProfile{
		Language:  stack.Language,
		BuildTool: stack.BuildTool,
	}

	// Walk files to count source and test files.
	profile.SourceFileCount, profile.TestFileCount = countFiles(repoPath)
	profile.TestsExist = profile.TestFileCount > 0

	// Count git commits.
	profile.CommitCount = countCommits(repoPath)

	// Determine if this is an existing project.
	profile.IsExisting = profile.SourceFileCount > 5 && profile.CommitCount > 10

	// Collect top-level directories (excluding hidden dirs and skipDirs).
	profile.TopDirs = listTopDirs(repoPath)

	// CI detection.
	profile.HasCI = detectCI(repoPath)

	// Docker detection.
	profile.HasDocker = detectDocker(repoPath)

	// Build health check.
	profile.BuildHealthy = checkBuildHealth(repoPath, stack)

	return profile
}

// countFiles walks the repo and counts source and test files.
func countFiles(repoPath string) (source, test int) {
	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if d.IsDir() {
			if skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}

		ext := filepath.Ext(d.Name())
		if !sourceExtensions[ext] {
			return nil
		}

		if isTestFile(d.Name()) {
			test++
		} else {
			source++
		}
		return nil
	})
	return source, test
}

// isTestFile returns true if the filename looks like a test file across
// common language conventions.
func isTestFile(name string) bool {
	lower := strings.ToLower(name)

	// Go: *_test.go
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	// Python: test_*.py or *_test.py
	if strings.HasSuffix(lower, ".py") {
		base := strings.TrimSuffix(lower, ".py")
		if strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test") {
			return true
		}
	}
	// JS/TS: *.test.js, *.spec.js, *.test.ts, *.spec.ts (and tsx/jsx)
	for _, suffix := range []string{".test.js", ".spec.js", ".test.ts", ".spec.ts", ".test.tsx", ".spec.tsx", ".test.jsx", ".spec.jsx"} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	// Java: *Test.java
	if strings.HasSuffix(name, "Test.java") || strings.HasSuffix(name, "Tests.java") {
		return true
	}
	// Rust: files in a tests/ directory are caught by the walker; inline
	// #[cfg(test)] can't be detected from filename alone.
	return false
}

// countCommits returns the number of commits on HEAD, or 0 if not a git repo.
func countCommits(repoPath string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", "rev-list", "--count", "HEAD")
	cmd.Dir = repoPath
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return count
}

// listTopDirs returns the names of top-level directories, excluding hidden
// directories and known skip directories.
func listTopDirs(repoPath string) []string {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil
	}

	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if skipDirs[name] {
			continue
		}
		dirs = append(dirs, name)
	}
	return dirs
}

// detectCI checks for common CI configuration files.
func detectCI(repoPath string) bool {
	ciPaths := []string{
		filepath.Join(".github", "workflows"),
		".gitlab-ci.yml",
		".circleci",
		"Jenkinsfile",
		".travis.yml",
	}
	for _, p := range ciPaths {
		full := filepath.Join(repoPath, p)
		if info, err := os.Stat(full); err == nil {
			// For directories (like .github/workflows), check if non-empty.
			if info.IsDir() {
				entries, dirErr := os.ReadDir(full)
				if dirErr == nil && len(entries) > 0 {
					return true
				}
				continue
			}
			return true
		}
	}
	return false
}

// detectDocker checks for Dockerfile or docker-compose files.
func detectDocker(repoPath string) bool {
	dockerFiles := []string{
		"Dockerfile",
		"docker-compose.yml",
		"docker-compose.yaml",
		"compose.yml",
		"compose.yaml",
	}
	for _, f := range dockerFiles {
		if _, err := os.Stat(filepath.Join(repoPath, f)); err == nil {
			return true
		}
	}
	return false
}

// buildCommands maps build tool names to their build commands.
var buildCommands = map[string][]string{
	"go":     {"go", "build", "./..."},
	"cargo":  {"cargo", "check"},
	"npm":    {"npm", "run", "build"},
	"maven":  {"mvn", "compile", "-q"},
	"gradle": {"gradle", "compileJava", "-q"},
	"pip":    {"python", "-m", "py_compile"},
	"poetry": {"poetry", "check"},
}

// checkBuildHealth runs the build command for the detected build tool with a
// 30-second timeout and returns true if it exits successfully.
func checkBuildHealth(repoPath string, stack nxdgit.TechStack) bool {
	args, ok := buildCommands[stack.BuildTool]
	if !ok || len(args) == 0 {
		return false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = repoPath
	cmd.Stdout = nil
	cmd.Stderr = nil

	return cmd.Run() == nil
}

// validTypes is the set of accepted requirement classification types.
var validTypes = map[string]bool{
	"feature":        true,
	"bugfix":         true,
	"refactor":       true,
	"infrastructure": true,
}

// ClassifyRequirement uses a single LLM call to classify a requirement's
// intent. On any error (LLM failure, bad JSON, invalid type), it returns a
// safe default of Type:"feature", Confidence:0.5.
func ClassifyRequirement(
	ctx context.Context,
	client llm.Client,
	requirement string,
	profile RepoProfile,
) (RequirementClassification, error) {
	fallback := RequirementClassification{
		Type:       "feature",
		Confidence: 0.5,
		Signals:    []string{"default-fallback"},
	}

	prompt := buildClassificationPrompt(requirement, profile)

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		MaxTokens: 512,
		System:    "You are a software engineering assistant. Classify the given requirement.",
		Messages:  []llm.Message{{Role: llm.RoleUser, Content: prompt}},
	})
	if err != nil {
		return fallback, nil //nolint:nilerr // fallback by design
	}

	cleaned := extractJSON(resp.Content)

	var result RequirementClassification
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return fallback, nil //nolint:nilerr // fallback by design
	}

	// Validate the type field.
	if !validTypes[result.Type] {
		return fallback, nil
	}

	return result, nil
}

// buildClassificationPrompt constructs the user message for the classification
// LLM call, incorporating repo profile context.
func buildClassificationPrompt(requirement string, profile RepoProfile) string {
	var b strings.Builder

	b.WriteString("Classify the following requirement into one of: feature, bugfix, refactor, infrastructure.\n\n")
	b.WriteString(fmt.Sprintf("Requirement: %s\n\n", requirement))
	b.WriteString("Repository context:\n")
	b.WriteString(fmt.Sprintf("- Language: %s\n", profile.Language))
	b.WriteString(fmt.Sprintf("- Build tool: %s\n", profile.BuildTool))
	b.WriteString(fmt.Sprintf("- Source files: %d\n", profile.SourceFileCount))
	b.WriteString(fmt.Sprintf("- Test files: %d\n", profile.TestFileCount))
	b.WriteString(fmt.Sprintf("- Commits: %d\n", profile.CommitCount))
	b.WriteString(fmt.Sprintf("- Has CI: %v\n", profile.HasCI))
	b.WriteString(fmt.Sprintf("- Has Docker: %v\n", profile.HasDocker))
	b.WriteString(fmt.Sprintf("- Is existing project: %v\n", profile.IsExisting))
	b.WriteString("\nRespond ONLY with a JSON object containing:\n")
	b.WriteString("- type: one of \"feature\", \"bugfix\", \"refactor\", \"infrastructure\"\n")
	b.WriteString("- confidence: float between 0.0 and 1.0\n")
	b.WriteString("- signals: array of strings explaining the classification\n")
	b.WriteString("\nNo markdown, no explanation — just the JSON object.")

	return b.String()
}

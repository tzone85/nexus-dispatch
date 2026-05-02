package criteria

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Evaluate runs a single criterion check against the given working directory.
func Evaluate(ctx context.Context, workDir string, c Criterion) Result {
	switch c.Type {
	case TypeFileExists:
		return evalFileExists(workDir, c)
	case TypeFileContains:
		return evalFileContains(workDir, c)
	case TypeTestPasses:
		return evalTestPasses(ctx, workDir, c)
	case TypeCoverageAbove:
		return evalCoverageAbove(ctx, workDir, c)
	case TypeCommandSucceeds:
		return evalCommandSucceeds(ctx, workDir, c)
	default:
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("unknown criterion type: %s", c.Type)}
	}
}

// EvaluateAll runs all criteria and returns results. Stops early on context
// cancellation but not on individual failures.
func EvaluateAll(ctx context.Context, workDir string, criteria []Criterion) []Result {
	results := make([]Result, 0, len(criteria))
	for _, c := range criteria {
		if ctx.Err() != nil {
			results = append(results, Result{Criterion: c, Passed: false, Message: "cancelled"})
			continue
		}
		results = append(results, Evaluate(ctx, workDir, c))
	}
	return results
}

// AllPassed returns true if every result passed.
func AllPassed(results []Result) bool {
	for _, r := range results {
		if !r.Passed {
			return false
		}
	}
	return true
}

// FailureSummary returns a human-readable summary of failed criteria.
func FailureSummary(results []Result) string {
	var sb strings.Builder
	for _, r := range results {
		if !r.Passed {
			sb.WriteString(fmt.Sprintf("- [%s] %s: %s\n", r.Criterion.Type, r.Criterion.Target, r.Message))
		}
	}
	return sb.String()
}

func evalFileExists(workDir string, c Criterion) Result {
	path := filepath.Join(workDir, c.Target)
	_, err := os.Stat(path)
	if err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("file not found: %s", c.Target)}
	}
	return Result{Criterion: c, Passed: true, Actual: "exists", Message: "file exists"}
}

func evalFileContains(workDir string, c Criterion) Result {
	path := filepath.Join(workDir, c.Target)
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("cannot read file: %v", err)}
	}

	content := string(data)

	// Try regex first, fall back to substring.
	re, reErr := regexp.Compile(c.Expected)
	if reErr == nil {
		if re.MatchString(content) {
			return Result{Criterion: c, Passed: true, Actual: "matched", Message: "pattern found"}
		}
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("pattern %q not found in %s", c.Expected, c.Target)}
	}

	if strings.Contains(content, c.Expected) {
		return Result{Criterion: c, Passed: true, Actual: "found", Message: "substring found"}
	}
	return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("%q not found in %s", c.Expected, c.Target)}
}

func evalTestPasses(ctx context.Context, workDir string, c Criterion) Result {
	args := normalizeGoTestArgs(c.Target)
	cmd := exec.CommandContext(ctx, "go", append([]string{"test"}, args...)...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{
			Criterion: c, Passed: false,
			Actual:  strings.TrimSpace(string(out)),
			Message: fmt.Sprintf("tests failed: %v", err),
		}
	}
	return Result{Criterion: c, Passed: true, Actual: "passed", Message: "all tests pass"}
}

func normalizeGoTestArgs(target string) []string {
	fields := strings.Fields(target)
	if len(fields) >= 2 && fields[0] == "go" && fields[1] == "test" {
		fields = fields[2:]
	}
	if len(fields) == 0 {
		return []string{"./..."}
	}
	return fields
}

func evalCoverageAbove(ctx context.Context, workDir string, c Criterion) Result {
	target := c.Target
	if target == "" {
		target = "./..."
	}
	threshold, err := strconv.ParseFloat(c.Expected, 64)
	if err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("invalid threshold: %s", c.Expected)}
	}

	cmd := exec.CommandContext(ctx, "go", "test", "-cover", target)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		return Result{Criterion: c, Passed: false, Actual: output, Message: fmt.Sprintf("test+cover failed: %v", err)}
	}

	coverage := parseCoverage(output)
	if coverage < 0 {
		return Result{Criterion: c, Passed: false, Actual: output, Message: "could not parse coverage from output"}
	}
	if coverage < threshold {
		return Result{
			Criterion: c, Passed: false,
			Actual:  fmt.Sprintf("%.1f%%", coverage),
			Message: fmt.Sprintf("coverage %.1f%% below threshold %.1f%%", coverage, threshold),
		}
	}
	return Result{Criterion: c, Passed: true, Actual: fmt.Sprintf("%.1f%%", coverage), Message: "coverage meets threshold"}
}

// allowedCommandPrefixes restricts what can be run via command_succeeds
// criteria. The criteria configuration originates from nxd.yaml (operator
// trust) but is also augmented by LLM split actions, where untrusted text
// could land. The allowlist below covers all legitimate validation tools
// NXD ships criteria for, and rejects anything outside this list.
var allowedCommandPrefixes = []string{
	"go build", "go test", "go vet", "go run", "go fmt", "go mod tidy",
	"npm run", "npm test", "npm install", "npm ci", "npx tsc",
	"pnpm run", "pnpm test", "pnpm install",
	"yarn build", "yarn test", "yarn install",
	"python -m", "python3 -m", "pytest",
	"make ",
	"cargo build", "cargo test",
	"./scripts/", "scripts/",
	"git diff", "git status", "git log",
}

// IsCommandAllowed reports whether the given command is permitted under the
// criteria allowlist. Exposed for testing and for callers that need to
// pre-validate criteria at config-load time.
func IsCommandAllowed(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false
	}
	// Reject shell metacharacters that can chain commands or redirect.
	if strings.ContainsAny(trimmed, ";&|`$<>") {
		return false
	}
	for _, prefix := range allowedCommandPrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

func evalCommandSucceeds(ctx context.Context, workDir string, c Criterion) Result {
	if !IsCommandAllowed(c.Target) {
		return Result{
			Criterion: c, Passed: false,
			Actual:  c.Target,
			Message: fmt.Sprintf("command rejected by allowlist: %q (see internal/criteria/evaluator.go)", c.Target),
		}
	}
	// Tokenize and exec without a shell so injection via metachars is
	// impossible by construction (in addition to the allowlist check).
	parts := strings.Fields(c.Target)
	if len(parts) == 0 {
		return Result{Criterion: c, Passed: false, Message: "empty command"}
	}
	if isGoBuildCommand(parts) {
		cleanup := cleanupGoBuildArtifacts(workDir)
		defer cleanup()
	}
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return Result{
			Criterion: c, Passed: false,
			Actual:  strings.TrimSpace(string(out)),
			Message: fmt.Sprintf("command failed: %v", err),
		}
	}
	return Result{Criterion: c, Passed: true, Actual: "exit 0", Message: "command succeeded"}
}

func isGoBuildCommand(parts []string) bool {
	return len(parts) >= 2 && parts[0] == "go" && parts[1] == "build"
}

func cleanupGoBuildArtifacts(workDir string) func() {
	candidates := []string{
		filepath.Join(workDir, filepath.Base(workDir)),
	}
	if moduleBinary := moduleBinaryPath(workDir); moduleBinary != "" {
		candidates = append(candidates, moduleBinary)
	}
	existed := make(map[string]bool, len(candidates))
	for _, path := range candidates {
		_, statErr := os.Stat(path)
		existed[path] = statErr == nil
	}
	before := untrackedFiles(workDir)
	return func() {
		for _, path := range candidates {
			if !existed[path] {
				_ = os.Remove(path)
			}
		}
		after := untrackedFiles(workDir)
		for path := range after {
			if _, ok := before[path]; ok {
				continue
			}
			abs := filepath.Join(workDir, path)
			if info, err := os.Stat(abs); err == nil && !info.IsDir() {
				_ = os.Remove(abs)
			}
		}
	}
}

func moduleBinaryPath(workDir string) string {
	data, err := os.ReadFile(filepath.Join(workDir, "go.mod"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "module" {
			return filepath.Join(workDir, filepath.Base(fields[1]))
		}
	}
	return ""
}

func untrackedFiles(workDir string) map[string]struct{} {
	cmd := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	files := make(map[string]struct{})
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "?? ") {
			files[strings.TrimSpace(strings.TrimPrefix(line, "?? "))] = struct{}{}
		}
	}
	return files
}

// parseCoverage extracts the coverage percentage from go test -cover output.
// Returns -1 if not found.
func parseCoverage(output string) float64 {
	re := regexp.MustCompile(`coverage:\s+([\d.]+)%`)
	match := re.FindStringSubmatch(output)
	if len(match) < 2 {
		return -1
	}
	v, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return -1
	}
	return v
}

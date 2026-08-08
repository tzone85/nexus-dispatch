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

	"github.com/tzone85/nexus-dispatch/internal/sanitize"
)

// resolveWorkDirPath joins workDir and rel, refusing any rel that is
// absolute or escapes workDir via "..". Centralised so file_exists,
// file_contains, and schema_changed share the same containment policy.
//
// Criteria are configured from nxd.yaml — operator-trusted — but the LLM
// "split" action can also emit criteria, and merge-conflict resolution can
// pull criteria text from upstream branches. A malicious value like
// "../../etc/passwd" would otherwise let a file_contains check probe the
// host filesystem.
func resolveWorkDirPath(workDir, rel string) (string, error) {
	return sanitize.SafeJoin(workDir, rel)
}

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
	case TypeMigrationSucceeds:
		return evaluateMigrationSucceeds(ctx, workDir, c)
	case TypeSchemaChanged:
		return evaluateSchemaChanged(ctx, workDir, c)
	case TypeSQLQueryReturns:
		return evaluateSQLQueryReturns(ctx, workDir, c)
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

// maxActualInSummary bounds how much captured command output FailureSummary
// echoes back. The actionable diagnostic (compiler error, failed assertion) is
// almost always at the END of the output, so we keep the tail.
const maxActualInSummary = 2000

// FailureSummary returns a human-readable summary of failed criteria. For
// command_succeeds / test_passes it appends the captured command output (the
// Result.Actual field) — without it a self-correcting agent is told only
// "command failed: exit status 1" and retries blind, unable to see WHAT broke
// (e.g. a Swift manifest error "type 'String?' has no member
// 'relativeToThisFile'"). Surfacing the real error is what lets the agent fix
// the root cause instead of guessing.
func FailureSummary(results []Result) string {
	var sb strings.Builder
	for _, r := range results {
		if r.Passed {
			continue
		}
		fmt.Fprintf(&sb, "- [%s] %s: %s\n", r.Criterion.Type, r.Criterion.Target, r.Message)
		// Echo the command's own output, unless it is empty or merely repeats
		// the command target (the allowlist-reject case, where Actual == Target).
		if out := strings.TrimSpace(r.Actual); out != "" && out != strings.TrimSpace(r.Criterion.Target) {
			fmt.Fprintf(&sb, "  output:\n%s\n", indentSalient(out, maxActualInSummary))
		}
	}
	return sb.String()
}

// errorLineRe matches the lines that carry the actionable diagnostic. A blind
// tail is wrong for tools like `swift build`, whose manifest errors print the
// real "error:" line first and then dump a multi-KB swiftc invocation — the
// tail would keep the noise and drop the error.
var errorLineRe = regexp.MustCompile(`(?i)\b(error|fatal|fail(ed|ure)?|cannot|undefined|expected|no member|not found|panic|exception|assert)\b`)

// indentSalient extracts the most useful slice of command output within n
// bytes: the error-bearing lines when any exist (that is where the fix lives),
// otherwise the tail. Every line is indented so the block reads as sub-content.
func indentSalient(s string, n int) string {
	lines := strings.Split(s, "\n")
	var picked []string
	for _, ln := range lines {
		if errorLineRe.MatchString(ln) {
			picked = append(picked, ln)
		}
	}
	var body string
	switch {
	case len(picked) > 0:
		body = strings.Join(picked, "\n")
		if len(body) > n {
			body = body[:n] + "\n…(truncated)"
		}
	case len(s) > n:
		body = "…(truncated)\n" + s[len(s)-n:] // no error lines: keep the tail
	default:
		body = s
	}
	out := strings.Split(body, "\n")
	for i, ln := range out {
		out[i] = "    " + ln
	}
	return strings.Join(out, "\n")
}

func evalFileExists(workDir string, c Criterion) Result {
	path, err := resolveWorkDirPath(workDir, c.Target)
	if err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("rejected target %q: %v", c.Target, err)}
	}
	if _, err := os.Stat(path); err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("file not found: %s", c.Target)}
	}
	return Result{Criterion: c, Passed: true, Actual: "exists", Message: "file exists"}
}

func evalFileContains(workDir string, c Criterion) Result {
	path, pathErr := resolveWorkDirPath(workDir, c.Target)
	if pathErr != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("rejected target %q: %v", c.Target, pathErr)}
	}
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

	// Write a merged coverage profile so we can measure the AGGREGATE coverage
	// of the whole target. `go test -cover ./...` prints one "coverage: X%"
	// line PER package; reading just the first (as a bare regex does) lets the
	// gate pass on the alphabetically-first package while every other package
	// is far below threshold — a false green on a gating check. `go tool cover
	// -func` collapses the profile into one statement-weighted "total:" line.
	profile, err := os.CreateTemp("", "nxd-cover-*.out")
	if err != nil {
		return Result{Criterion: c, Passed: false, Message: fmt.Sprintf("create coverage profile: %v", err)}
	}
	profilePath := profile.Name()
	_ = profile.Close()
	defer func() { _ = os.Remove(profilePath) }()

	cmd := exec.CommandContext(ctx, "go", "test", "-coverprofile", profilePath, target)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	output := string(out)
	if err != nil {
		return Result{Criterion: c, Passed: false, Actual: output, Message: fmt.Sprintf("test+cover failed: %v", err)}
	}

	// Prefer the statement-weighted aggregate from the profile; fall back to the
	// per-run summary line only if the profile can't be reduced (e.g. a target
	// with no statements at all).
	coverage := coverageTotal(ctx, workDir, profilePath)
	if coverage < 0 {
		coverage = parseCoverage(output)
	}
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
	// Swift: SwiftPM build/test plus the bare compiler for syntax checks.
	// Without these, a Swift repository cannot be gated at all — the
	// 2026-08-08 gauntlet run had `swift build` criteria rejected here,
	// which exhausted every escalation tier.
	"swift build", "swift test", "swiftc ",
	// PHP: linting and script-based test runners (legacy projects rarely
	// have a runnable vendor/, so `php tests/run.php` style runners are
	// the practical gate), plus vendored phpunit and composer checks.
	"php ", "vendor/bin/", "composer validate", "composer install", "composer test",
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
// Returns -1 if not found. NOTE: this reads only the FIRST "coverage:" line;
// for a multi-package target use coverageTotal, which is statement-weighted
// across the whole profile. parseCoverage remains as a single-package fallback.
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

// coverageTotal reduces a coverage profile to a single statement-weighted total
// percentage via `go tool cover -func`. Returns -1 if it cannot be determined.
func coverageTotal(ctx context.Context, workDir, profilePath string) float64 {
	cmd := exec.CommandContext(ctx, "go", "tool", "cover", "-func", profilePath)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return -1
	}
	return parseFuncTotal(string(out))
}

// parseFuncTotal extracts the percentage from the "total:" line of
// `go tool cover -func` output (e.g. "total:\t(statements)\t81.6%"). Returns -1
// if not found.
func parseFuncTotal(output string) float64 {
	re := regexp.MustCompile(`total:\s+\(statements\)\s+([\d.]+)%`)
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

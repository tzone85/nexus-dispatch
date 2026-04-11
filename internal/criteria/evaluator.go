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
	target := c.Target
	if target == "" {
		target = "./..."
	}
	cmd := exec.CommandContext(ctx, "go", "test", target)
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

func evalCommandSucceeds(ctx context.Context, workDir string, c Criterion) Result {
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Target)
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

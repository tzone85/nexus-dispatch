package plugin

import (
	"context"
	"os/exec"
	"time"
)

// PluginQACheck defines a QA check provided by a plugin.
type PluginQACheck struct {
	Name       string
	ScriptPath string
	After      string
}

// QACheckResult holds the outcome of running a plugin QA check.
type QACheckResult struct {
	Name    string
	Passed  bool
	Output  string
	Elapsed time.Duration
}

// RunPluginQACheck executes a plugin QA check script in the given working
// directory and returns the result. The check passes when the script exits
// with code 0.
func RunPluginQACheck(ctx context.Context, check PluginQACheck, workDir string) QACheckResult {
	start := time.Now()
	cmd := exec.CommandContext(ctx, check.ScriptPath)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return QACheckResult{
		Name:    check.Name,
		Passed:  err == nil,
		Output:  string(out),
		Elapsed: time.Since(start),
	}
}

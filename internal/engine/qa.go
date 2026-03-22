package engine

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// QACheckResult holds the outcome of a single QA check (lint, build, or test).
type QACheckResult struct {
	Name    string
	Passed  bool
	Output  string
	Elapsed time.Duration
}

// QAResult holds the aggregate outcome of all QA checks for a story.
type QAResult struct {
	Passed bool
	Checks []QACheckResult
}

// CommandRunner abstracts command execution for testability.
type CommandRunner interface {
	Run(ctx context.Context, workDir, name string, args ...string) (string, error)
}

// ExecRunner executes commands via os/exec.
type ExecRunner struct{}

// Run executes the given command in the specified working directory and
// returns the combined stdout/stderr output.
func (e *ExecRunner) Run(ctx context.Context, workDir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = workDir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// QAConfig describes the commands to run for each QA check.
type QAConfig struct {
	LintCommand  string
	BuildCommand string
	TestCommand  string
}

// QA runs lint, build, and test commands against a worktree directory and
// emits pass/fail events.
type QA struct {
	config     QAConfig
	runner     CommandRunner
	eventStore state.EventStore
	projStore  state.ProjectionStore
}

// NewQA creates a QA instance with the given configuration, command runner,
// event store, and projection store.
func NewQA(cfg QAConfig, runner CommandRunner, es state.EventStore, ps state.ProjectionStore) *QA {
	return &QA{
		config:     cfg,
		runner:     runner,
		eventStore: es,
		projStore:  ps,
	}
}

// Run executes all QA checks (lint, build, test) in the given worktree
// directory for the specified story. It emits STORY_QA_STARTED, then either
// STORY_QA_PASSED or STORY_QA_FAILED.
func (q *QA) Run(ctx context.Context, storyID, worktreePath string) (QAResult, error) {
	// Emit QA started
	startEvt := state.NewEvent(state.EventStoryQAStarted, "qa", storyID, map[string]any{
		"worktree_path": worktreePath,
	})
	if err := q.eventStore.Append(startEvt); err != nil {
		return QAResult{}, fmt.Errorf("emit qa started: %w", err)
	}
	if err := q.projStore.Project(startEvt); err != nil {
		return QAResult{}, fmt.Errorf("project qa started: %w", err)
	}

	checks := []struct {
		name    string
		command string
	}{
		{"lint", q.config.LintCommand},
		{"build", q.config.BuildCommand},
		{"test", q.config.TestCommand},
	}

	result := QAResult{Passed: true}

	for _, check := range checks {
		if check.command == "" {
			continue
		}

		checkResult := q.runCheck(ctx, worktreePath, check.name, check.command)
		result.Checks = append(result.Checks, checkResult)

		if !checkResult.Passed {
			result.Passed = false
		}
	}

	// Emit result event
	eventType := state.EventStoryQAPassed
	if !result.Passed {
		eventType = state.EventStoryReviewFailed
	}

	failedChecks := make([]string, 0)
	for _, c := range result.Checks {
		if !c.Passed {
			failedChecks = append(failedChecks, c.Name)
		}
	}

	resultEvt := state.NewEvent(eventType, "qa", storyID, map[string]any{
		"passed":        result.Passed,
		"total_checks":  len(result.Checks),
		"failed_checks": failedChecks,
	})
	if err := q.eventStore.Append(resultEvt); err != nil {
		return result, fmt.Errorf("emit qa result: %w", err)
	}
	if err := q.projStore.Project(resultEvt); err != nil {
		return result, fmt.Errorf("project qa result: %w", err)
	}

	return result, nil
}

// runCheck executes a single QA command and returns the result.
func (q *QA) runCheck(ctx context.Context, workDir, name, command string) QACheckResult {
	parts := strings.Fields(command)
	if len(parts) == 0 {
		return QACheckResult{Name: name, Passed: false, Output: "empty command"}
	}

	start := time.Now()
	output, err := q.runner.Run(ctx, workDir, parts[0], parts[1:]...)
	elapsed := time.Since(start)

	return QACheckResult{
		Name:    name,
		Passed:  err == nil,
		Output:  output,
		Elapsed: elapsed,
	}
}

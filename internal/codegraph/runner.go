package codegraph

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBinName is the expected binary name on PATH.
const DefaultBinName = "code-review-graph"

// GraphDir is the subdirectory where code-review-graph stores its data.
const GraphDir = ".code-review-graph"

// Runner wraps the code-review-graph CLI for subprocess invocations.
type Runner struct {
	BinPath string
}

// NewRunner creates a Runner, resolving the binary path from PATH.
// If the binary is not found, BinPath is empty and Available() returns false.
func NewRunner() *Runner {
	path, err := exec.LookPath(DefaultBinName)
	if err != nil {
		return &Runner{}
	}
	return &Runner{BinPath: path}
}

// Available reports whether the code-review-graph binary is installed.
func (r *Runner) Available() bool {
	return r.BinPath != ""
}

// Build runs a full graph build for the given repo path.
func (r *Runner) Build(ctx context.Context, repoPath string) error {
	if !r.Available() {
		return fmt.Errorf("code-review-graph not installed")
	}
	_, err := r.run(ctx, repoPath, "build")
	return err
}

// Update runs an incremental graph update against a base git ref.
func (r *Runner) Update(ctx context.Context, repoPath, baseRef string) error {
	if !r.Available() {
		return fmt.Errorf("code-review-graph not installed")
	}
	args := []string{"update"}
	if baseRef != "" {
		args = append(args, "--base", baseRef)
	}
	_, err := r.run(ctx, repoPath, args...)
	return err
}

// DetectChanges runs blast-radius analysis and parses the JSON output.
// Returns an empty ImpactAnalysis (not an error) if the tool is unavailable.
func (r *Runner) DetectChanges(ctx context.Context, repoPath, baseRef string) (*ImpactAnalysis, error) {
	if !r.Available() {
		return &ImpactAnalysis{}, nil
	}
	args := []string{"detect-changes"}
	if baseRef != "" {
		args = append(args, "--base", baseRef)
	}
	out, err := r.run(ctx, repoPath, args...)
	if err != nil {
		return &ImpactAnalysis{}, fmt.Errorf("detect-changes: %w", err)
	}
	return parseDetectChanges(out)
}

// Status returns graph statistics for the given repo path.
// Returns an empty GraphInfo (not an error) if the tool is unavailable.
func (r *Runner) Status(ctx context.Context, repoPath string) (*GraphInfo, error) {
	if !r.Available() {
		return &GraphInfo{}, nil
	}
	out, err := r.run(ctx, repoPath, "status")
	if err != nil {
		return &GraphInfo{}, fmt.Errorf("status: %w", err)
	}
	return parseStatus(out)
}

// GraphDBPath returns the path to the SQLite graph database for a repo.
func GraphDBPath(repoPath string) string {
	return filepath.Join(repoPath, GraphDir, "graph.db")
}

// run executes code-review-graph with the given args in the repo directory.
func (r *Runner) run(ctx context.Context, repoPath string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, r.BinPath, args...)
	cmd.Dir = repoPath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%s %s: %w (stderr: %s)",
			r.BinPath, strings.Join(args, " "), err, stderr.String())
	}
	return stdout.String(), nil
}

// detectChangesJSON mirrors the JSON schema from code-review-graph detect-changes.
type detectChangesJSON struct {
	Summary          string `json:"summary"`
	RiskScore        float64 `json:"risk_score"`
	ChangedFunctions []struct {
		Name      string  `json:"name"`
		FilePath  string  `json:"file_path"`
		Kind      string  `json:"kind"`
		LineStart int     `json:"line_start"`
		LineEnd   int     `json:"line_end"`
		IsTest    bool    `json:"is_test"`
		RiskScore float64 `json:"risk_score"`
	} `json:"changed_functions"`
	TestGaps []struct {
		Name      string `json:"name"`
		File      string `json:"file"`
		LineStart int    `json:"line_start"`
		LineEnd   int    `json:"line_end"`
	} `json:"test_gaps"`
	ReviewPriorities []struct {
		Name      string  `json:"name"`
		FilePath  string  `json:"file_path"`
		Kind      string  `json:"kind"`
		LineStart int     `json:"line_start"`
		LineEnd   int     `json:"line_end"`
		IsTest    bool    `json:"is_test"`
		RiskScore float64 `json:"risk_score"`
	} `json:"review_priorities"`
}

func parseDetectChanges(raw string) (*ImpactAnalysis, error) {
	var parsed detectChangesJSON
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return &ImpactAnalysis{}, fmt.Errorf("parse detect-changes JSON: %w", err)
	}

	ia := &ImpactAnalysis{
		RiskScore: parsed.RiskScore,
		Summary:   parsed.Summary,
	}

	for _, f := range parsed.ChangedFunctions {
		ia.ChangedFunctions = append(ia.ChangedFunctions, ChangedNode{
			Name: f.Name, FilePath: f.FilePath, Kind: f.Kind,
			LineStart: f.LineStart, LineEnd: f.LineEnd,
			RiskScore: f.RiskScore, IsTest: f.IsTest,
		})
	}

	for _, g := range parsed.TestGaps {
		ia.TestGaps = append(ia.TestGaps, TestGap{
			Name: g.Name, FilePath: g.File,
			LineStart: g.LineStart, LineEnd: g.LineEnd,
		})
	}

	for _, p := range parsed.ReviewPriorities {
		ia.ReviewPriorities = append(ia.ReviewPriorities, ChangedNode{
			Name: p.Name, FilePath: p.FilePath, Kind: p.Kind,
			LineStart: p.LineStart, LineEnd: p.LineEnd,
			RiskScore: p.RiskScore, IsTest: p.IsTest,
		})
	}

	ia.AffectedFiles = ia.UniqueAffectedFiles()
	return ia, nil
}

func parseStatus(raw string) (*GraphInfo, error) {
	// status output is line-based: "Nodes: 2189\nEdges: 24062\n..."
	info := &GraphInfo{}
	for _, line := range strings.Split(raw, "\n") {
		parts := strings.SplitN(line, ": ", 2)
		if len(parts) != 2 {
			continue
		}
		key, val := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		switch key {
		case "Nodes":
			fmt.Sscanf(val, "%d", &info.NodeCount)
		case "Edges":
			fmt.Sscanf(val, "%d", &info.EdgeCount)
		case "Files":
			fmt.Sscanf(val, "%d", &info.FileCount)
		case "Languages":
			info.Languages = strings.Split(val, ", ")
		case "Last updated":
			info.LastUpdated, _ = time.Parse(time.RFC3339, val)
		case "Built at commit":
			info.CommitHash = val
		}
	}
	return info, nil
}

package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// DryRunClient simulates LLM responses for testing the full NXD pipeline
// without live API calls. It inspects the system prompt to determine what
// role is calling and returns an appropriate canned response.
type DryRunClient struct {
	mu    sync.Mutex
	calls []CompletionRequest
	delay time.Duration // simulated latency
}

// NewDryRunClient creates a client that generates realistic canned responses.
// The optional delay parameter simulates API latency.
func NewDryRunClient(delay time.Duration) *DryRunClient {
	return &DryRunClient{delay: delay}
}

// Complete inspects the request to determine the caller role and returns a
// plausible canned response. It records every call for later inspection.
func (d *DryRunClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	d.mu.Lock()
	d.calls = append(d.calls, req)
	d.mu.Unlock()

	if d.delay > 0 {
		select {
		case <-time.After(d.delay):
		case <-ctx.Done():
			return CompletionResponse{}, ctx.Err()
		}
	}

	content := d.generateResponse(req)
	return CompletionResponse{
		Content: content,
		Model:   req.Model,
		Usage:   Usage{InputTokens: 100, OutputTokens: 200},
	}, nil
}

// CallCount returns the number of calls made to Complete.
func (d *DryRunClient) CallCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.calls)
}

// CallAt returns the CompletionRequest from the call at the given index.
func (d *DryRunClient) CallAt(index int) CompletionRequest {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.calls[index]
}

// generateResponse inspects the system prompt and user message to determine
// the caller role and returns an appropriate canned response.
func (d *DryRunClient) generateResponse(req CompletionRequest) string {
	system := strings.ToLower(req.System)
	user := ""
	if len(req.Messages) > 0 {
		user = req.Messages[0].Content
	}

	// Requirement classification (NXD's ClassifyRequirement)
	if strings.Contains(system, "classify") && strings.Contains(system, "requirement") {
		return d.classifyResponse()
	}

	// Codebase investigation (NXD's Investigator)
	if strings.Contains(system, "investigat") || strings.Contains(system, "codebase analysis") {
		return d.investigationResponse()
	}

	// Tech Lead planning — return story decomposition
	if strings.Contains(system, "tech lead") || strings.Contains(system, "decompose") {
		return d.planningResponse(user)
	}

	// Code review / QA
	if strings.Contains(system, "review") || strings.Contains(system, "qa agent") {
		return d.reviewResponse()
	}

	// Manager diagnosis
	if strings.Contains(system, "manager") || strings.Contains(system, "diagnos") {
		return d.managerResponse()
	}

	// Supervisor check
	if strings.Contains(system, "supervisor") {
		return d.supervisorResponse()
	}

	// Default — echo back a summary
	return fmt.Sprintf("[DRY RUN] Simulated response for prompt (%d chars)", len(user))
}

// classifyResponse returns a valid JSON classification that NXD's
// ClassifyRequirement can parse.
func (d *DryRunClient) classifyResponse() string {
	result := map[string]any{
		"type":       "feature",
		"confidence": 0.92,
		"reasoning":  "[DRY RUN] Classified as new feature based on requirement text.",
	}
	data, _ := json.Marshal(result)
	return string(data)
}

// investigationResponse returns a valid investigation report JSON.
func (d *DryRunClient) investigationResponse() string {
	report := map[string]any{
		"modules": []map[string]any{
			{"name": "api", "path": "internal/api", "description": "HTTP handlers and routing"},
			{"name": "store", "path": "internal/store", "description": "Data persistence layer"},
		},
		"code_smells":  []string{},
		"risk_areas":   []string{},
		"tech_stack":   "Go with standard library HTTP",
		"test_command": "go test ./...",
	}
	data, _ := json.MarshalIndent(report, "", "  ")
	return string(data)
}

// planningResponse returns a valid JSON array of story objects that the
// planner can parse. It extracts a short title from the requirement text.
func (d *DryRunClient) planningResponse(requirement string) string {
	title := requirement
	if len(title) > 60 {
		title = title[:60]
	}

	stories := []map[string]any{
		{
			"id":                  "s-001",
			"title":               "Project scaffold and directory structure",
			"description":         "Create the directory structure with placeholder files for the feature.",
			"acceptance_criteria": "- Directory structure exists\n- go build ./... succeeds",
			"complexity":          1,
			"depends_on":          []string{},
			"owned_files":         []string{"internal/api/router.go"},
			"wave_hint":           "sequential",
		},
		{
			"id":                  "s-002",
			"title":               "Implement core logic",
			"description":         fmt.Sprintf("Implement the main business logic for: %s", title),
			"acceptance_criteria": "- Core functions implemented\n- Unit tests pass",
			"complexity":          3,
			"depends_on":          []string{"s-001"},
			"owned_files":         []string{"internal/api/handler.go", "internal/api/handler_test.go"},
			"wave_hint":           "parallel",
		},
		{
			"id":                  "s-003",
			"title":               "Add integration and wiring",
			"description":         "Wire the components together and add integration tests.",
			"acceptance_criteria": "- Integration tests pass\n- go test ./... passes",
			"complexity":          2,
			"depends_on":          []string{"s-002"},
			"owned_files":         []string{"internal/api/integration_test.go"},
			"wave_hint":           "sequential",
		},
	}

	data, _ := json.MarshalIndent(stories, "", "  ")
	return string(data)
}

// reviewResponse returns a valid JSON review result that the reviewer can parse.
func (d *DryRunClient) reviewResponse() string {
	result := map[string]any{
		"passed":   true,
		"comments": []string{},
		"summary":  "[DRY RUN] All acceptance criteria met. Code quality is good.",
	}
	data, _ := json.Marshal(result)
	return string(data)
}

// managerResponse returns a valid JSON diagnosis for tier-2 escalation.
func (d *DryRunClient) managerResponse() string {
	action := map[string]any{
		"diagnosis": "[DRY RUN] Story failed due to environment issue.",
		"category":  "environment",
		"action":    "retry",
		"retry_config": map[string]any{
			"target_role":    "senior",
			"reset_tier":     1,
			"worktree_reset": false,
		},
	}
	data, _ := json.Marshal(action)
	return string(data)
}

// supervisorResponse returns a progress assessment.
func (d *DryRunClient) supervisorResponse() string {
	return `ASSESSMENT: [DRY RUN] Stories are progressing well. No drift detected.
REPRIORITIZE: false`
}

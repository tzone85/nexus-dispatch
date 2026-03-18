package llm

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ClaudeCLIClient implements the Client interface by invoking the Claude Code
// CLI tool instead of making direct API calls. This routes LLM completions
// through the user's Claude subscription rather than per-token API credits.
type ClaudeCLIClient struct {
	cliPath string // path to claude binary, default "claude"
}

// NewClaudeCLIClient creates a client that invokes Claude Code CLI for
// completions. The claude binary must be on $PATH or installed at the
// default location.
func NewClaudeCLIClient() *ClaudeCLIClient {
	return &ClaudeCLIClient{cliPath: "claude"}
}

// NewClaudeCLIClientWithPath creates a client that invokes the Claude Code CLI
// at the specified path.
func NewClaudeCLIClientWithPath(cliPath string) *ClaudeCLIClient {
	return &ClaudeCLIClient{cliPath: cliPath}
}

// Complete builds a prompt from the request and invokes
// `claude -p "<prompt>" --output-format text [--model <model>] --max-turns 1`.
// It captures stdout as the completion content.
func (c *ClaudeCLIClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	prompt := buildCLIPrompt(req)

	args := []string{
		"-p", prompt,
		"--output-format", "text",
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	// Prevent interactive loops — single-turn completion only.
	args = append(args, "--max-turns", "1")

	cmd := exec.CommandContext(ctx, c.cliPath, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return CompletionResponse{}, classifyCLIError(err, out)
	}

	return CompletionResponse{
		Content: strings.TrimSpace(string(out)),
		Model:   req.Model,
	}, nil
}

// buildCLIPrompt concatenates the system prompt and user messages into a single
// string suitable for the CLI's -p flag.
func buildCLIPrompt(req CompletionRequest) string {
	var prompt strings.Builder

	if req.System != "" {
		prompt.WriteString(req.System)
		prompt.WriteString("\n\n")
	}
	for _, msg := range req.Messages {
		if msg.Role == RoleUser {
			prompt.WriteString(msg.Content)
			prompt.WriteString("\n")
		}
	}

	return prompt.String()
}

// classifyCLIError inspects CLI output to produce a structured APIError where
// possible, or a generic error otherwise.
func classifyCLIError(err error, output []byte) error {
	text := strings.TrimSpace(string(output))
	lower := strings.ToLower(text)

	if strings.Contains(lower, "credit balance") || strings.Contains(lower, "billing") || strings.Contains(lower, "insufficient_quota") {
		return &APIError{
			StatusCode: 400,
			Message:    text,
			Retryable:  false,
		}
	}

	if strings.Contains(lower, "authentication") || strings.Contains(lower, "unauthorized") {
		return &APIError{
			StatusCode: 401,
			Message:    text,
			Retryable:  false,
		}
	}

	if strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") {
		return &APIError{
			StatusCode: 429,
			Message:    text,
			Retryable:  true,
		}
	}

	return fmt.Errorf("claude CLI error: %w (output: %s)", err, text)
}

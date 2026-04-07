package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// GemmaRuntimeConfig holds configuration for the native Gemma coding runtime.
type GemmaRuntimeConfig struct {
	MaxIterations    int
	CommandAllowlist []string
}

// GemmaRuntime is a native coding runtime that uses Gemma 4's function calling
// to make code edits directly, bypassing external CLI tools like Aider.
type GemmaRuntime struct {
	client llm.Client
	config GemmaRuntimeConfig
}

// NewGemmaRuntime creates a new GemmaRuntime with the given LLM client and
// configuration.
func NewGemmaRuntime(client llm.Client, cfg GemmaRuntimeConfig) *GemmaRuntime {
	return &GemmaRuntime{
		client: client,
		config: cfg,
	}
}

// Name returns the runtime's registered name.
func (g *GemmaRuntime) Name() string { return "gemma" }

// SupportedModels returns the list of models this runtime can use.
func (g *GemmaRuntime) SupportedModels() []string {
	return []string{"gemma4"}
}

// CodingTools returns the tool definitions available to the Gemma coding
// runtime for making code edits.
func CodingTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read the contents of a file at the given path relative to the working directory.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Relative file path to read"}
				},
				"required": ["path"]
			}`),
		},
		{
			Name:        "write_file",
			Description: "Write content to a file at the given path, creating directories as needed.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Relative file path to write"},
					"content": {"type": "string", "description": "File content to write"}
				},
				"required": ["path", "content"]
			}`),
		},
		{
			Name:        "edit_file",
			Description: "Replace an exact text match in a file with new text.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"path": {"type": "string", "description": "Relative file path to edit"},
					"old_text": {"type": "string", "description": "Exact text to find and replace"},
					"new_text": {"type": "string", "description": "Replacement text"}
				},
				"required": ["path", "old_text", "new_text"]
			}`),
		},
		{
			Name:        "run_command",
			Description: "Run a shell command in the working directory. Only allowlisted commands are permitted.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"command": {"type": "string", "description": "Shell command to execute"}
				},
				"required": ["command"]
			}`),
		},
		{
			Name:        "task_complete",
			Description: "Signal that the coding task is complete with a summary of changes made.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"summary": {"type": "string", "description": "Summary of changes made"}
				},
				"required": ["summary"]
			}`),
		},
	}
}

// ExecuteResult holds the outcome of a native runtime execution.
type ExecuteResult struct {
	Summary    string
	Iterations int
	Error      error
}

// Execute runs the main tool-calling loop: sends the goal and tools to the LLM,
// executes tool calls, feeds results back, and repeats until the model calls
// task_complete or max iterations are reached.
func (g *GemmaRuntime) Execute(ctx context.Context, workDir, model, systemPrompt, goal string) ExecuteResult {
	tools := CodingTools()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: goal},
	}

	for i := 0; i < g.config.MaxIterations; i++ {
		resp, err := g.client.Complete(ctx, llm.CompletionRequest{
			Model:     model,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: 8192,
		})
		if err != nil {
			return ExecuteResult{Error: fmt.Errorf("llm completion (iteration %d): %w", i+1, err)}
		}

		// No tool calls means the model is done talking without completing.
		if len(resp.ToolCalls) == 0 {
			return ExecuteResult{
				Summary:    resp.Content,
				Iterations: i + 1,
			}
		}

		// Append the assistant message with tool calls.
		messages = append(messages, llm.Message{
			Role:      llm.RoleAssistant,
			Content:   resp.Content,
			ToolCalls: resp.ToolCalls,
		})

		// Execute each tool call and collect results.
		for _, tc := range resp.ToolCalls {
			// Check for task_complete signal.
			if tc.Name == "task_complete" {
				var args struct {
					Summary string `json:"summary"`
				}
				json.Unmarshal(tc.Arguments, &args)
				return ExecuteResult{
					Summary:    args.Summary,
					Iterations: i + 1,
				}
			}

			result := g.executeTool(tc, workDir)
			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    result.Content,
				ToolCallID: result.CallID,
			})
		}
	}

	return ExecuteResult{
		Summary:    "max iterations reached",
		Iterations: g.config.MaxIterations,
		Error:      fmt.Errorf("reached max iterations (%d) without task completion", g.config.MaxIterations),
	}
}

// executeTool dispatches a tool call to the appropriate handler and returns
// the result.
func (g *GemmaRuntime) executeTool(call llm.ToolCall, workDir string) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	switch call.Name {
	case "read_file":
		return g.execReadFile(call, workDir)
	case "write_file":
		return g.execWriteFile(call, workDir)
	case "edit_file":
		return g.execEditFile(call, workDir)
	case "run_command":
		return g.execRunCommand(call, workDir)
	case "task_complete":
		// Handled in the Execute loop, but return gracefully if called directly.
		result.Content = "task complete"
		return result
	default:
		result.IsError = true
		result.Content = fmt.Sprintf("unknown tool: %s", call.Name)
		return result
	}
}

// safePath resolves a relative path within the working directory and rejects
// any path traversal attempts.
func safePath(relPath, workDir string) (string, error) {
	abs := filepath.Join(workDir, relPath)
	cleaned := filepath.Clean(abs)

	// Ensure the resolved path is still within the working directory.
	if !strings.HasPrefix(cleaned, filepath.Clean(workDir)+string(filepath.Separator)) &&
		cleaned != filepath.Clean(workDir) {
		return "", fmt.Errorf("path traversal blocked: %s resolves outside work directory", relPath)
	}

	return cleaned, nil
}

// execReadFile reads a file relative to the working directory.
func (g *GemmaRuntime) execReadFile(call llm.ToolCall, workDir string) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("invalid arguments: %v", err)
		return result
	}

	absPath, err := safePath(args.Path, workDir)
	if err != nil {
		result.IsError = true
		result.Content = err.Error()
		return result
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("read error: %v", err)
		return result
	}

	result.Content = string(content)
	return result
}

// execWriteFile writes content to a file, creating parent directories as needed.
func (g *GemmaRuntime) execWriteFile(call llm.ToolCall, workDir string) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	var args struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("invalid arguments: %v", err)
		return result
	}

	absPath, err := safePath(args.Path, workDir)
	if err != nil {
		result.IsError = true
		result.Content = err.Error()
		return result
	}

	// Create parent directories if they don't exist.
	dir := filepath.Dir(absPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("mkdir error: %v", err)
		return result
	}

	if err := os.WriteFile(absPath, []byte(args.Content), 0o644); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("write error: %v", err)
		return result
	}

	result.Content = fmt.Sprintf("wrote %d bytes to %s", len(args.Content), args.Path)
	return result
}

// execEditFile performs a find-and-replace operation on a file.
func (g *GemmaRuntime) execEditFile(call llm.ToolCall, workDir string) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	var args struct {
		Path    string `json:"path"`
		OldText string `json:"old_text"`
		NewText string `json:"new_text"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("invalid arguments: %v", err)
		return result
	}

	absPath, err := safePath(args.Path, workDir)
	if err != nil {
		result.IsError = true
		result.Content = err.Error()
		return result
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("read error: %v", err)
		return result
	}

	original := string(content)
	if !strings.Contains(original, args.OldText) {
		result.IsError = true
		result.Content = fmt.Sprintf("old_text not found in %s", args.Path)
		return result
	}

	updated := strings.Replace(original, args.OldText, args.NewText, 1)
	if err := os.WriteFile(absPath, []byte(updated), 0o644); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("write error: %v", err)
		return result
	}

	result.Content = fmt.Sprintf("edited %s: replaced text", args.Path)
	return result
}

// execRunCommand runs a shell command if it matches the allowlist.
func (g *GemmaRuntime) execRunCommand(call llm.ToolCall, workDir string) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("invalid arguments: %v", err)
		return result
	}

	// Check command against allowlist.
	allowed := false
	for _, pattern := range g.config.CommandAllowlist {
		if strings.HasPrefix(args.Command, pattern) {
			allowed = true
			break
		}
	}
	if !allowed {
		result.IsError = true
		result.Content = fmt.Sprintf("command not in allowlist: %s", args.Command)
		return result
	}

	cmd := exec.Command("sh", "-c", args.Command)
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("command error: %v\noutput: %s", err, string(output))
		return result
	}

	result.Content = string(output)
	return result
}

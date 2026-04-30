package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/criteria"
	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/scratchboard"
)

// GemmaRuntimeConfig holds configuration for the native Gemma coding runtime.
type GemmaRuntimeConfig struct {
	MaxIterations      int
	MaxCriteriaRetries int // max times agent can retry after criteria rejection (default: 2)
	CommandAllowlist   []string
}

// ProgressPhase identifies what stage a progress event represents.
type ProgressPhase string

const (
	PhaseThinking   ProgressPhase = "thinking"
	PhaseToolCall   ProgressPhase = "tool_call"
	PhaseToolResult ProgressPhase = "tool_result"
	PhaseError      ProgressPhase = "error"
	PhaseCompleted  ProgressPhase = "completed"
)

// ProgressEvent describes a single progress update during native runtime
// execution. Events are emitted at two granularities: per-iteration (coarse)
// and per-tool-call (fine).
type ProgressEvent struct {
	Iteration int           // 1-based iteration index
	MaxIter   int           // configured max iterations
	Phase     ProgressPhase // what is happening
	Tool      string        // tool name (for tool_call/tool_result phases)
	File      string        // file path (for file operations)
	Command   string        // shell command (for run_command)
	IsError   bool          // whether the tool result was an error
	Detail    string        // brief human-readable description
}

// ProgressCallback receives progress events during execution.
type ProgressCallback func(ProgressEvent)

// GemmaRuntime is a native coding runtime that uses Gemma 4's function calling
// to make code edits directly, bypassing external CLI tools like Aider.
type GemmaRuntime struct {
	client       llm.Client
	config       GemmaRuntimeConfig
	OnProgress   ProgressCallback
	Scratchboard *scratchboard.Scratchboard
	Criteria     []criteria.Criterion // optional success criteria to evaluate after task_complete
	AgentID      string               // used as author when writing to scratchboard
	StoryID      string               // used as context when writing to scratchboard
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
		{
			Name:        "write_scratchboard",
			Description: "Share a discovery with other agents working in parallel. Write only high-value findings: API patterns, required configuration, common gotchas, schema requirements.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Category: pattern, gotcha, schema, config, dependency"},
					"content": {"type": "string", "description": "The discovery to share"}
				},
				"required": ["category", "content"]
			}`),
		},
		{
			Name:        "read_scratchboard",
			Description: "Read discoveries shared by other parallel agents. Use this before starting work to check if others have found relevant patterns or requirements.",
			Parameters: json.RawMessage(`{
				"type": "object",
				"properties": {
					"category": {"type": "string", "description": "Optional category filter (pattern, gotcha, schema, config, dependency)"}
				}
			}`),
		},
	}
}

// ExecuteResult holds the outcome of a native runtime execution.
type ExecuteResult struct {
	Summary        string
	Iterations     int
	Error          error
	CriteriaResult []criteria.Result // populated when criteria are configured
}

// Execute runs the main tool-calling loop: sends the goal and tools to the LLM,
// executes tool calls, feeds results back, and repeats until the model calls
// task_complete or max iterations are reached.
func (g *GemmaRuntime) Execute(ctx context.Context, workDir, model, systemPrompt, goal string) ExecuteResult {
	tools := CodingTools()

	messages := []llm.Message{
		{Role: llm.RoleUser, Content: goal},
	}

	criteriaRejections := 0 // tracks how many times task_complete was rejected by criteria

	for i := 0; i < g.config.MaxIterations; i++ {
		// Coarse progress: iteration started, model is thinking.
		g.emitProgress(ProgressEvent{
			Iteration: i + 1,
			MaxIter:   g.config.MaxIterations,
			Phase:     PhaseThinking,
			Detail:    fmt.Sprintf("iteration %d/%d: waiting for LLM response", i+1, g.config.MaxIterations),
		})

		// S3-5: per-iteration deadline. MaxIterations alone doesn't help if a
		// single Ollama call hangs forever (network stall, server hang). Cap at
		// 5 minutes per iteration so the outer loop can make progress / be
		// cancelled.
		iterCtx, iterCancel := context.WithTimeout(ctx, 5*time.Minute)
		resp, err := g.client.Complete(iterCtx, llm.CompletionRequest{
			Model:     model,
			System:    systemPrompt,
			Messages:  messages,
			Tools:     tools,
			MaxTokens: 8192,
		})
		iterCancel()
		if err != nil {
			g.emitProgress(ProgressEvent{
				Iteration: i + 1,
				MaxIter:   g.config.MaxIterations,
				Phase:     PhaseError,
				IsError:   true,
				Detail:    fmt.Sprintf("LLM error: %v", err),
			})
			return ExecuteResult{Error: fmt.Errorf("llm completion (iteration %d): %w", i+1, err)}
		}

		// Truncate oversized responses to prevent context window exhaustion.
		resp.Content = llm.TruncateContent(resp.Content, llm.MaxResponseContentLen)

		// LB8 (live test): qwen2.5-coder and similar local models commonly
		// emit tool calls as raw JSON in the content field rather than via
		// the structured tool_calls API. If we got no structured calls but
		// the content looks like one or more tool-call objects, parse them
		// out and execute as if they were real tool calls.
		if len(resp.ToolCalls) == 0 {
			extracted := extractInlineToolCalls(resp.Content)
			if len(extracted) > 0 {
				log.Printf("[gemma] recovered %d inline tool call(s) from text content", len(extracted))
				resp.ToolCalls = extracted
			}
		}

		// No tool calls means the model is done talking without completing.
		if len(resp.ToolCalls) == 0 {
			g.emitProgress(ProgressEvent{
				Iteration: i + 1,
				MaxIter:   g.config.MaxIterations,
				Phase:     PhaseCompleted,
				Detail:    "model finished without tool calls",
			})
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

				// Run criteria evaluation BEFORE accepting completion.
				// If criteria fail, feed the error back to the agent so it
				// can self-correct within the current iteration loop instead
				// of failing the story and restarting from scratch.
				//
				// Budget: after MaxCriteriaRetries rejections, STOP and
				// escalate instead of letting the agent thrash. Without this,
				// a weak model might "game" the criteria by deleting tests,
				// adding t.Skip(), or modifying production code to match
				// hallucinated tests — optimizing for gate-passing instead
				// of code quality.
				if len(g.Criteria) > 0 {
					criteriaResult := criteria.EvaluateAll(ctx, workDir, g.Criteria)
					if !criteria.AllPassed(criteriaResult) {
						criteriaRejections++
						failSummary := criteria.FailureSummary(criteriaResult)

						maxRetries := g.config.MaxCriteriaRetries
						if maxRetries <= 0 {
							maxRetries = 2 // default: 2 self-correction attempts
						}

						// Budget exceeded — escalate instead of thrashing.
						if criteriaRejections > maxRetries {
							log.Printf("[native-runtime] %s: criteria rejection budget exhausted (%d/%d), escalating",
								g.StoryID, criteriaRejections, maxRetries)
							g.emitProgress(ProgressEvent{
								Iteration: i + 1,
								MaxIter:   g.config.MaxIterations,
								Phase:     PhaseError,
								Tool:      "task_complete",
								IsError:   true,
								Detail:    fmt.Sprintf("criteria rejection budget exhausted (%d attempts) — escalating to higher tier", criteriaRejections),
							})
							return ExecuteResult{
								Summary:        args.Summary,
								Iterations:     i + 1,
								CriteriaResult: criteriaResult,
								Error: fmt.Errorf("criteria rejection budget exhausted after %d attempts: %s — escalate to a stronger model or human review",
									criteriaRejections, failSummary),
							}
						}

						g.emitProgress(ProgressEvent{
							Iteration: i + 1,
							MaxIter:   g.config.MaxIterations,
							Phase:     PhaseError,
							Tool:      "task_complete",
							IsError:   true,
							Detail:    fmt.Sprintf("criteria failed (%d/%d retries), agent must fix: %s", criteriaRejections, maxRetries, failSummary),
						})

						// Tell the agent what failed. Include the retry budget
						// so it knows urgency and avoids hacky workarounds.
						// Note: the assistant message was already appended above
						// (before the tool-call loop), so we only append the
						// rejection tool result here.
						messages = append(messages, llm.Message{
							Role: llm.RoleTool,
							Content: fmt.Sprintf(
								"COMPLETION REJECTED (attempt %d of %d) — criteria check failed.\n\n"+
									"Fix these issues before calling task_complete again:\n%s\n\n"+
									"IMPORTANT: Fix the ROOT CAUSE. Do NOT delete tests, skip assertions, "+
									"or modify production code just to pass the gate. If you cannot fix the "+
									"issue properly, call task_complete with a summary explaining what you "+
									"could not resolve — it will be escalated to a senior agent.",
								criteriaRejections, maxRetries, failSummary),
							ToolCallID: tc.ID,
						})

						log.Printf("[native-runtime] %s: task_complete rejected (%d/%d): %s",
							g.StoryID, criteriaRejections, maxRetries, failSummary)
						goto nextIteration
					}

					// All criteria passed — accept completion.
					g.emitProgress(ProgressEvent{
						Iteration: i + 1,
						MaxIter:   g.config.MaxIterations,
						Phase:     PhaseCompleted,
						Tool:      "task_complete",
						Detail:    args.Summary,
					})
					return ExecuteResult{
						Summary:        args.Summary,
						Iterations:     i + 1,
						CriteriaResult: criteriaResult,
					}
				}

				// No criteria configured — accept immediately.
				g.emitProgress(ProgressEvent{
					Iteration: i + 1,
					MaxIter:   g.config.MaxIterations,
					Phase:     PhaseCompleted,
					Tool:      "task_complete",
					Detail:    args.Summary,
				})
				return ExecuteResult{
					Summary:    args.Summary,
					Iterations: i + 1,
				}
			}

			// Fine progress: about to execute a tool call.
			file, command := extractToolTarget(tc)
			g.emitProgress(ProgressEvent{
				Iteration: i + 1,
				MaxIter:   g.config.MaxIterations,
				Phase:     PhaseToolCall,
				Tool:      tc.Name,
				File:      file,
				Command:   command,
				Detail:    describeToolCall(tc.Name, file, command),
			})

			result := g.executeTool(tc, workDir)

			// Fine progress: tool call result.
			g.emitProgress(ProgressEvent{
				Iteration: i + 1,
				MaxIter:   g.config.MaxIterations,
				Phase:     PhaseToolResult,
				Tool:      tc.Name,
				File:      file,
				Command:   command,
				IsError:   result.IsError,
				Detail:    describeToolResult(tc.Name, file, command, result.IsError),
			})

			messages = append(messages, llm.Message{
				Role:       llm.RoleTool,
				Content:    result.Content,
				ToolCallID: result.CallID,
			})
		}
	nextIteration:
	}

	g.emitProgress(ProgressEvent{
		Iteration: g.config.MaxIterations,
		MaxIter:   g.config.MaxIterations,
		Phase:     PhaseError,
		IsError:   true,
		Detail:    fmt.Sprintf("reached max iterations (%d)", g.config.MaxIterations),
	})

	return ExecuteResult{
		Summary:    "max iterations reached",
		Iterations: g.config.MaxIterations,
		Error:      fmt.Errorf("reached max iterations (%d) without task completion", g.config.MaxIterations),
	}
}

// emitProgress sends a progress event if a callback is registered.
func (g *GemmaRuntime) emitProgress(evt ProgressEvent) {
	if g.OnProgress != nil {
		g.OnProgress(evt)
	}
}

// extractToolTarget pulls the file path or command from a tool call's arguments.
func extractToolTarget(tc llm.ToolCall) (file, command string) {
	var args struct {
		Path    string `json:"path"`
		Command string `json:"command"`
	}
	json.Unmarshal(tc.Arguments, &args)
	return args.Path, args.Command
}

// describeToolCall returns a human-readable description of a tool invocation.
func describeToolCall(tool, file, command string) string {
	switch tool {
	case "read_file":
		return fmt.Sprintf("reading %s", file)
	case "write_file":
		return fmt.Sprintf("writing %s", file)
	case "edit_file":
		return fmt.Sprintf("editing %s", file)
	case "run_command":
		return fmt.Sprintf("running: %s", command)
	default:
		return fmt.Sprintf("calling %s", tool)
	}
}

// describeToolResult returns a human-readable description of a tool's outcome.
func describeToolResult(tool, file, command string, isError bool) string {
	status := "ok"
	if isError {
		status = "failed"
	}
	switch tool {
	case "read_file":
		return fmt.Sprintf("read %s: %s", file, status)
	case "write_file":
		return fmt.Sprintf("wrote %s: %s", file, status)
	case "edit_file":
		return fmt.Sprintf("edited %s: %s", file, status)
	case "run_command":
		return fmt.Sprintf("command %s: %s", command, status)
	default:
		return fmt.Sprintf("%s: %s", tool, status)
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
	case "write_scratchboard":
		return g.execWriteScratchboard(call)
	case "read_scratchboard":
		return g.execReadScratchboard(call)
	default:
		result.IsError = true
		result.Content = fmt.Sprintf("unknown tool: %s", call.Name)
		return result
	}
}

// safePath resolves a relative path within the working directory and rejects
// any path traversal attempts. Symlinks are resolved to prevent escaping
// the work directory via symlink indirection.
func safePath(relPath, workDir string) (string, error) {
	abs := filepath.Join(workDir, relPath)
	cleaned := filepath.Clean(abs)

	cleanedWorkDir := filepath.Clean(workDir)

	// Ensure the cleaned path is within workDir before symlink resolution.
	if !strings.HasPrefix(cleaned, cleanedWorkDir+string(filepath.Separator)) &&
		cleaned != cleanedWorkDir {
		return "", fmt.Errorf("path traversal blocked: %s resolves outside work directory", relPath)
	}

	// Resolve symlinks to catch indirection that escapes the work directory.
	// Only evaluate if the target exists (new files won't have symlinks).
	realPath, err := filepath.EvalSymlinks(cleaned)
	if err == nil {
		// Target exists — verify the real path is still within workDir.
		realWorkDir, wdErr := filepath.EvalSymlinks(cleanedWorkDir)
		if wdErr != nil {
			realWorkDir = cleanedWorkDir
		}
		if !strings.HasPrefix(realPath, realWorkDir+string(filepath.Separator)) &&
			realPath != realWorkDir {
			return "", fmt.Errorf("path traversal blocked: %s resolves outside work directory via symlink", relPath)
		}
		return realPath, nil
	}

	// Target doesn't exist yet (new file) — return cleaned path.
	return cleaned, nil
}

// isCommandAllowed checks whether a command is permitted by the allowlist.
// It extracts the binary name from the command (first whitespace-delimited token)
// and validates that the full command starts with an allowlisted prefix followed
// by either a space, end-of-string, or the exact match. Shell metacharacters
// (;, |, &, $, `, \n) are rejected outright to prevent command chaining.
func isCommandAllowed(command string, allowlist []string) bool {
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}

	// H9: reject any shell metacharacter that could chain commands, redirect
	// I/O, or escape the allowlist. Belt-and-suspenders alongside the prefix
	// match below.
	for _, ch := range []string{";", "&&", "||", "|", "$(", "$", "`", ">", "<", "\n", "\r", "\t&"} {
		if strings.Contains(command, ch) {
			return false
		}
	}

	for _, pattern := range allowlist {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if command == pattern {
			return true
		}
		// Allow if command starts with pattern followed by a space
		// (e.g., pattern "go test" matches "go test ./..." but not "go testevil").
		if strings.HasPrefix(command, pattern+" ") {
			return true
		}
	}
	return false
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

	if strings.TrimSpace(args.Path) == "" {
		result.IsError = true
		result.Content = "read_file requires a non-empty path argument"
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

	// Live-test discovery: small models sometimes call write_file with an
	// empty path (or just whitespace), producing an error message of "wrote
	// : failed". Reject early with an actionable message.
	if strings.TrimSpace(args.Path) == "" {
		result.IsError = true
		result.Content = "write_file requires a non-empty path argument (e.g. \"path\": \"internal/game/board.go\")"
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

	// Check command against allowlist using safe binary extraction.
	if !isCommandAllowed(args.Command, g.config.CommandAllowlist) {
		result.IsError = true
		// Live-test discovery: small models default to `mkdir -p X` to set up
		// directories, but write_file already auto-creates parents. Steer the
		// model to the right tool instead of just rejecting.
		hint := ""
		trimmed := strings.TrimSpace(args.Command)
		switch {
		case strings.HasPrefix(trimmed, "mkdir"),
			strings.HasPrefix(trimmed, "touch"),
			strings.HasPrefix(trimmed, "cd "),
			trimmed == "cd",
			strings.HasPrefix(trimmed, "pwd"),
			strings.HasPrefix(trimmed, "ls"):
			hint = "\nhint: use the write_file tool — it creates parent directories automatically. mkdir/touch/cd/ls/pwd are not needed."
		case strings.HasPrefix(trimmed, "rm"),
			strings.HasPrefix(trimmed, "mv"),
			strings.HasPrefix(trimmed, "cp"):
			hint = "\nhint: file mutation is intentionally blocked. Use write_file or edit_file. To delete a file, write empty content."
		}
		result.Content = fmt.Sprintf("command not in allowlist: %s%s", args.Command, hint)
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

// execWriteScratchboard writes a discovery to the shared scratchboard.
func (g *GemmaRuntime) execWriteScratchboard(call llm.ToolCall) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	if g.Scratchboard == nil {
		result.Content = "scratchboard not available"
		return result
	}

	var args struct {
		Category string `json:"category"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("invalid arguments: %v", err)
		return result
	}

	if err := g.Scratchboard.Write(scratchboard.Entry{
		AgentID:  g.AgentID,
		StoryID:  g.StoryID,
		Category: args.Category,
		Content:  args.Content,
	}); err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("write error: %v", err)
		return result
	}

	result.Content = fmt.Sprintf("shared to scratchboard [%s]: %s", args.Category, args.Content)
	return result
}

// execReadScratchboard reads recent entries from the shared scratchboard.
func (g *GemmaRuntime) execReadScratchboard(call llm.ToolCall) llm.ToolCallResult {
	result := llm.ToolCallResult{CallID: call.ID}

	if g.Scratchboard == nil {
		result.Content = "scratchboard not available"
		return result
	}

	var args struct {
		Category string `json:"category"`
	}
	json.Unmarshal(call.Arguments, &args)

	entries, err := g.Scratchboard.Read(args.Category, scratchboard.MaxReadEntries)
	if err != nil {
		result.IsError = true
		result.Content = fmt.Sprintf("read error: %v", err)
		return result
	}

	if len(entries) == 0 {
		result.Content = "no entries in scratchboard"
		return result
	}

	var sb strings.Builder
	for _, e := range entries {
		sb.WriteString(fmt.Sprintf("[%s/%s] %s: %s\n", e.StoryID, e.AgentID, e.Category, e.Content))
	}
	result.Content = sb.String()
	return result
}

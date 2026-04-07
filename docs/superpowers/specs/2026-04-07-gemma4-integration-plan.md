# Gemma 4 Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate Gemma 4 as NXD's default model with native function calling, a Google AI fallback provider, and a native Gemma coding runtime.

**Architecture:** Three-layer integration — (1) provider layer adds Google AI client with automatic Ollama fallback, (2) function calling framework gives all LLM-facing roles structured tool contracts replacing free-text parsing, (3) native Gemma runtime bypasses Aider for direct tool-call-based coding. All changes are backward compatible; non-Gemma models degrade gracefully to existing text parsing.

**Tech Stack:** Go 1.23+, Ollama v0.20+ (OpenAI-compatible API), Google AI Studio REST API (`generativelanguage.googleapis.com/v1beta`), Gemma 4 26B MoE via Ollama

**Spec:** `docs/superpowers/specs/2026-04-07-gemma4-integration-design.md`

---

## Phase 1: LLM Foundation Layer

### Task 1: Tool Calling Types

**Files:**
- Create: `internal/llm/tools.go`
- Test: `internal/llm/tools_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/llm/tools_test.go
package llm

import (
	"encoding/json"
	"testing"
)

func TestToolDefinition_MarshalJSON(t *testing.T) {
	td := ToolDefinition{
		Name:        "create_story",
		Description: "Create a new story from a requirement",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"title": {"type": "string"},
				"complexity": {"type": "integer", "minimum": 1, "maximum": 13}
			},
			"required": ["title", "complexity"]
		}`),
	}

	data, err := json.Marshal(td)
	if err != nil {
		t.Fatalf("marshal ToolDefinition: %v", err)
	}

	var got ToolDefinition
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ToolDefinition: %v", err)
	}

	if got.Name != "create_story" {
		t.Errorf("Name = %q, want %q", got.Name, "create_story")
	}
	if got.Description != td.Description {
		t.Errorf("Description mismatch")
	}
}

func TestToolCall_MarshalJSON(t *testing.T) {
	tc := ToolCall{
		Name:      "create_story",
		Arguments: json.RawMessage(`{"title":"Auth endpoint","complexity":3}`),
	}

	data, err := json.Marshal(tc)
	if err != nil {
		t.Fatalf("marshal ToolCall: %v", err)
	}

	var got ToolCall
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal ToolCall: %v", err)
	}

	if got.Name != "create_story" {
		t.Errorf("Name = %q, want %q", got.Name, "create_story")
	}
}

func TestToolCallResult_RoundTrip(t *testing.T) {
	tcr := ToolCallResult{
		CallID:  "call_001",
		Content: `{"story_id": "s-001"}`,
		IsError: false,
	}

	data, err := json.Marshal(tcr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got ToolCallResult
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.CallID != "call_001" {
		t.Errorf("CallID = %q, want %q", got.CallID, "call_001")
	}
	if got.IsError {
		t.Error("expected IsError=false")
	}
}

func TestValidateToolCall_ValidSchema(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"},
			"complexity": {"type": "integer"}
		},
		"required": ["title"]
	}`)

	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{
		Name:      "test",
		Arguments: json.RawMessage(`{"title": "hello", "complexity": 3}`),
	}

	err := ValidateToolCall(td, tc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateToolCall_MissingRequired(t *testing.T) {
	schema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"title": {"type": "string"}
		},
		"required": ["title"]
	}`)

	td := ToolDefinition{Name: "test", Parameters: schema}
	tc := ToolCall{
		Name:      "test",
		Arguments: json.RawMessage(`{"complexity": 3}`),
	}

	err := ValidateToolCall(td, tc)
	if err == nil {
		t.Fatal("expected validation error for missing required field")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestTool -v`
Expected: FAIL — types not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/llm/tools.go
package llm

import (
	"encoding/json"
	"fmt"
)

// ToolDefinition describes a tool the model can call.
type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// ToolCall represents a single function call from the model.
type ToolCall struct {
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ToolCallResult returns the outcome of executing a tool call.
type ToolCallResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// ValidateToolCall checks that the arguments satisfy the schema's required fields.
// This is a lightweight check — validates required fields are present and arguments
// are valid JSON, but does not do full JSON Schema validation.
func ValidateToolCall(def ToolDefinition, call ToolCall) error {
	var args map[string]json.RawMessage
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return fmt.Errorf("invalid arguments JSON: %w", err)
	}

	var schema struct {
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(def.Parameters, &schema); err != nil {
		return fmt.Errorf("invalid schema JSON: %w", err)
	}

	for _, field := range schema.Required {
		if _, ok := args[field]; !ok {
			return fmt.Errorf("missing required field %q", field)
		}
	}

	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/llm/ -run TestTool -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/tools.go internal/llm/tools_test.go
git commit -m "feat: add tool calling types for function calling framework"
```

---

### Task 2: Update Client Types for Tool Support

**Files:**
- Modify: `internal/llm/client.go`

- [ ] **Step 1: Update CompletionRequest and CompletionResponse**

Add tool fields to the existing structs in `internal/llm/client.go`. The full updated file:

```go
package llm

import "context"

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type Role string

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type CompletionRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int
	Temperature float64
	System      string
	Tools       []ToolDefinition
	ToolChoice  string
}

type CompletionResponse struct {
	Content    string
	Model      string
	StopReason string
	Usage      Usage
	ToolCalls  []ToolCall
}

type Usage struct {
	InputTokens  int
	OutputTokens int
}

type Client interface {
	Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
}
```

- [ ] **Step 2: Verify existing tests still pass**

Run: `go test ./internal/llm/ -v`
Expected: PASS (new fields are zero-valued by default, no breakage)

- [ ] **Step 3: Commit**

```bash
git add internal/llm/client.go
git commit -m "feat: extend CompletionRequest/Response with tool calling fields"
```

---

### Task 3: Tool Compatibility Layer

**Files:**
- Create: `internal/llm/tool_compat.go`
- Test: `internal/llm/tool_compat_test.go`

- [ ] **Step 1: Write the failing test**

```go
// internal/llm/tool_compat_test.go
package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHasToolSupport_Gemma4(t *testing.T) {
	tests := []struct {
		provider string
		model    string
		want     bool
	}{
		{"ollama", "gemma4:26b", true},
		{"ollama", "gemma4:31b", true},
		{"ollama", "gemma4:e4b", true},
		{"google", "gemma-4-26b", true},
		{"google+ollama", "gemma4:26b", true},
		{"anthropic", "claude-opus-4-20250514", true},
		{"openai", "gpt-4o", true},
		{"ollama", "deepseek-coder-v2:latest", false},
		{"ollama", "qwen2.5-coder:14b", false},
		{"ollama", "codellama:13b", false},
	}

	for _, tt := range tests {
		got := HasToolSupport(tt.provider, tt.model)
		if got != tt.want {
			t.Errorf("HasToolSupport(%q, %q) = %v, want %v", tt.provider, tt.model, got, tt.want)
		}
	}
}

func TestInjectToolSchema_ProducesValidPrompt(t *testing.T) {
	tools := []ToolDefinition{
		{
			Name:        "create_story",
			Description: "Create a story",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`),
		},
	}

	result := InjectToolSchema("You are a planner.", tools)

	if !strings.Contains(result, "create_story") {
		t.Error("expected tool name in injected prompt")
	}
	if !strings.Contains(result, "You are a planner.") {
		t.Error("expected original system prompt preserved")
	}
	if !strings.Contains(result, "JSON") {
		t.Error("expected JSON instruction in injected prompt")
	}
}

func TestParseToolCallsFromText_Valid(t *testing.T) {
	text := `{"tool_calls": [{"name": "create_story", "arguments": {"title": "Auth"}}]}`

	calls, err := ParseToolCallsFromText(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
	if calls[0].Name != "create_story" {
		t.Errorf("Name = %q, want %q", calls[0].Name, "create_story")
	}
}

func TestParseToolCallsFromText_WithCodeFences(t *testing.T) {
	text := "```json\n{\"tool_calls\": [{\"name\": \"submit_review\", \"arguments\": {\"verdict\": \"approve\"}}]}\n```"

	calls, err := ParseToolCallsFromText(text)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(calls))
	}
}

func TestParseToolCallsFromText_Invalid(t *testing.T) {
	_, err := ParseToolCallsFromText("this is not json at all")
	if err == nil {
		t.Fatal("expected error for non-JSON text")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run "TestHasToolSupport|TestInjectToolSchema|TestParseToolCalls" -v`
Expected: FAIL — functions not defined

- [ ] **Step 3: Write the implementation**

```go
// internal/llm/tool_compat.go
package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

var toolSupportedModels = []string{
	"gemma4",
	"gemma-4",
}

var toolSupportedProviders = map[string]bool{
	"anthropic": true,
	"openai":    true,
	"google":    true,
}

func HasToolSupport(provider, model string) bool {
	baseProvider := provider
	if strings.Contains(provider, "+") {
		parts := strings.SplitN(provider, "+", 2)
		baseProvider = parts[0]
	}

	if toolSupportedProviders[baseProvider] {
		return true
	}

	lower := strings.ToLower(model)
	for _, prefix := range toolSupportedModels {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func InjectToolSchema(systemPrompt string, tools []ToolDefinition) string {
	if len(tools) == 0 {
		return systemPrompt
	}

	var b strings.Builder
	b.WriteString(systemPrompt)
	b.WriteString("\n\n## Available Tools\n\n")
	b.WriteString("You MUST respond with a JSON object containing a \"tool_calls\" array.\n")
	b.WriteString("Each element must have \"name\" (string) and \"arguments\" (object).\n\n")

	for _, tool := range tools {
		b.WriteString(fmt.Sprintf("### %s\n", tool.Name))
		b.WriteString(fmt.Sprintf("%s\n", tool.Description))
		b.WriteString(fmt.Sprintf("Parameters: %s\n\n", string(tool.Parameters)))
	}

	b.WriteString("Respond ONLY with the JSON object. No prose before or after.\n")
	return b.String()
}

type textToolResponse struct {
	ToolCalls []struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"tool_calls"`
}

func ParseToolCallsFromText(text string) ([]ToolCall, error) {
	cleaned := strings.TrimSpace(text)

	if strings.HasPrefix(cleaned, "```") {
		lines := strings.Split(cleaned, "\n")
		if len(lines) >= 3 {
			lines = lines[1 : len(lines)-1]
			cleaned = strings.Join(lines, "\n")
		}
	}

	var resp textToolResponse
	if err := json.Unmarshal([]byte(cleaned), &resp); err != nil {
		return nil, fmt.Errorf("parse tool calls from text: %w", err)
	}

	calls := make([]ToolCall, len(resp.ToolCalls))
	for i, tc := range resp.ToolCalls {
		calls[i] = ToolCall{
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return calls, nil
}
```

- [ ] **Step 4: Run all tests**

Run: `go test ./internal/llm/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/tool_compat.go internal/llm/tool_compat_test.go
git commit -m "feat: add tool compatibility layer with graceful degradation"
```

---

### Task 4: Ollama Client — Tool Calling Support

**Files:**
- Modify: `internal/llm/ollama.go`
- Create: `internal/llm/ollama_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/llm/ollama_test.go` with `TestOllamaClient_Complete_WithTools` (mock HTTP server returns tool call response, verify tools are sent in request body and ToolCalls are parsed from response) and `TestOllamaClient_Complete_NoTools` (verify existing text responses still work). See spec for full test code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestOllamaClient -v`
Expected: FAIL — Ollama client doesn't handle tools yet

- [ ] **Step 3: Update ollama.go**

Add new internal types (`ollamaTool`, `ollamaFunction`, `ollamaToolCall`) and update the request/response structs. In `Complete()`: map `req.Tools` to OpenAI-compatible `tools` array, handle `RoleTool` messages with `tool_call_id`, extract `tool_calls` from response `choices[0].message.tool_calls`. See spec for full type definitions and mapping code.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/llm/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/ollama.go internal/llm/ollama_test.go
git commit -m "feat: add tool calling support to Ollama client"
```

---

### Task 5: Google AI Client

**Files:**
- Create: `internal/llm/google.go`
- Test: `internal/llm/google_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/llm/google_test.go` with three tests: `TestGoogleClient_Complete_TextResponse` (mock server returns text, verify API key in URL query param), `TestGoogleClient_Complete_ToolCallResponse` (mock returns functionCall parts, verify tools sent and parsed), `TestGoogleClient_Complete_QuotaExhausted` (mock returns 429, verify `IsQuotaError` returns true). See spec for full test code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestGoogleClient -v`
Expected: FAIL — GoogleClient not defined

- [ ] **Step 3: Write the implementation**

Create `internal/llm/google.go` with `GoogleClient` struct, `QuotaError` type, `IsQuotaError()` helper, and `Complete()` method that maps to Google AI Studio's `generateContent` endpoint. Handle `systemInstruction`, `contents`, `tools` (as `functionDeclarations`), and parse `functionCall` parts from response. See spec for complete implementation.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/llm/ -run TestGoogleClient -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/llm/google.go internal/llm/google_test.go
git commit -m "feat: add Google AI Studio client with tool calling and quota detection"
```

---

### Task 6: FallbackClient

**Files:**
- Create: `internal/llm/fallback.go`
- Test: `internal/llm/fallback_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/llm/fallback_test.go` with `mockClient` test double and five tests: `TestFallbackClient_UsesPrimaryOnSuccess`, `TestFallbackClient_FallsBackOnQuotaError`, `TestFallbackClient_SkipsPrimaryAfterQuota`, `TestFallbackClient_ResetsCooldown` (50ms cooldown), `TestFallbackClient_PropagatesNonQuotaErrors`. See spec for full test code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run TestFallbackClient -v`
Expected: FAIL — FallbackClient not defined

- [ ] **Step 3: Write the implementation**

Create `internal/llm/fallback.go` with `FallbackClient` using `atomic.Bool` for `quotaExhausted` and a goroutine-based cooldown timer. `Complete()` tries primary first, falls back on any error, marks quota exhausted on `QuotaError`, and schedules reset after cooldown. See spec for complete implementation.

- [ ] **Step 4: Run tests with race detection**

Run: `go test ./internal/llm/ -run TestFallbackClient -v -race`
Expected: PASS (no race conditions)

- [ ] **Step 5: Commit**

```bash
git add internal/llm/fallback.go internal/llm/fallback_test.go
git commit -m "feat: add FallbackClient with automatic quota-based failover"
```

---

### Task 7: Config Schema Updates

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/loader.go`
- Modify: `internal/config/config_test.go`

- [ ] **Step 1: Write the failing tests**

Add to `internal/config/config_test.go`: `TestValidation_GoogleProvider` (google+ollama with google_model passes), `TestValidation_GoogleProvider_MissingGoogleModel` (fails without google_model), `TestDefaultConfig_Gemma4Defaults` (verifies gemma4:26b defaults), `TestValidation_NativeRuntime` (verifies gemma runtime in defaults). See spec for full test code.

- [ ] **Step 2: Run test to verify failures**

Run: `go test ./internal/config/ -run "TestValidation_Google|TestDefaultConfig_Gemma|TestValidation_Native" -v`
Expected: FAIL

- [ ] **Step 3: Update config.go**

Add `GoogleModel` and `FallbackCooldownS` to `ModelConfig`. Add `Native`, `MaxIterations`, `CommandAllowlist` to `RuntimeConfig`. Add `validProviders` map. Add `ModelsConfig.All()` helper. Update `Validate()` to check google_model requirement and native runtime constraints.

- [ ] **Step 4: Update loader.go defaults**

Create `gemma4Default(maxTokens)` helper. Update all role defaults to use `gemma4:26b` with `google+ollama` provider. Add `gemma` native runtime to default runtimes alongside aider, claude-code, codex.

- [ ] **Step 5: Update existing tests**

Fix assertions in `TestDefaultConfig` (expect gemma4:26b), `TestDefaultConfig_IncludesRuntimes` (expect 4 runtimes), `TestDefaultConfig_IncludesModels` (expect google+ollama provider).

- [ ] **Step 6: Run all config tests**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/loader.go internal/config/config_test.go
git commit -m "feat: update config schema for Gemma 4 defaults, google provider, native runtime"
```

---

### Task 8: Update Client Construction in CLI

**Files:**
- Modify: `internal/cli/req.go`

- [ ] **Step 1: Update buildLLMClient**

Add `"google"` and `"google+ollama"` cases to the switch. For `"google"`: read `GOOGLE_AI_API_KEY`, create `GoogleClient`. For `"google+ollama"`: create both clients, if no API key degrade to Ollama-only with log message, otherwise create `FallbackClient` with 60s cooldown. Add imports for `"log"`, `"time"`.

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/nxd/`
Expected: Success

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/cli/req.go
git commit -m "feat: add google and google+ollama provider support to CLI"
```

---

## Phase 2: Role-Specific Tool Schemas

### Task 9: Planner Tool Schemas + Engine Refactor

**Files:**
- Create: `internal/engine/planner_tools.go`
- Create: `internal/engine/planner_tools_test.go`
- Modify: `internal/engine/planner.go`

- [ ] **Step 1: Write failing test for tool definitions**

Create `internal/engine/planner_tools_test.go` with `TestPlannerTools_Definitions` (3 tools: create_story, set_wave_plan, request_clarification), `TestPlannerTools_ValidateCreateStory` (valid/invalid calls), `TestProcessPlannerToolCalls_CreateStory` (processes 2 stories + wave plan). See spec for full test code with exact assertions.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestPlannerTools -v`
Expected: FAIL

- [ ] **Step 3: Implement planner_tools.go**

Define `PlannerTools()` returning 3 tool definitions with JSON Schema parameters. Define `PlannerToolResult`, `ToolStory`, `ToolClarification` types. Implement `ProcessPlannerToolCalls()` that iterates calls, creates stories with sequential IDs (s-001, s-002...), extracts wave plan, and handles clarification requests. See spec for complete implementation.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/engine/ -run TestPlannerTools -v`
Expected: PASS

- [ ] **Step 5: Update planner.go to use tools when available**

In `Plan()`, before the LLM call: check `llm.HasToolSupport(provider, model)`. If true, add `Tools: PlannerTools()` and `ToolChoice: "required"` to request. After response: if tool calls present, process via `ProcessPlannerToolCalls()` and map to `PlannedStory` slice. If no tools or processing error, fall back to existing `parseStoriesFromText()` (extract current JSON parsing into this method).

- [ ] **Step 6: Run full engine tests**

Run: `go test ./internal/engine/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/engine/planner_tools.go internal/engine/planner_tools_test.go internal/engine/planner.go
git commit -m "feat: add planner tool schemas and integrate with Tech Lead planning"
```

---

### Task 10: Reviewer Tool Schemas + Engine Refactor

**Files:**
- Create: `internal/engine/reviewer_tools.go`
- Create: `internal/engine/reviewer_tools_test.go`
- Modify: `internal/engine/reviewer.go`

- [ ] **Step 1: Write failing test**

Create `internal/engine/reviewer_tools_test.go` with `TestReviewerTools_Definitions` (2 tools), `TestProcessReviewerToolCalls_Approve` (verdict=approve with file comments), `TestProcessReviewerToolCalls_InvalidVerdict` (rejects "maybe"). See spec for full test code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestReviewer -v`
Expected: FAIL

- [ ] **Step 3: Implement reviewer_tools.go**

Define `ReviewerTools()` with submit_review (verdict enum, file_comments, suggested_changes) and request_more_context tools. Define `ReviewToolResult`, `ReviewFileComment`, `ReviewSuggestedChange`, `ReviewContextRequest` types. Implement `ProcessReviewerToolCalls()` with verdict validation. See spec for complete implementation.

- [ ] **Step 4: Update reviewer.go to use tools**

Same pattern as planner: check HasToolSupport, add tools, process calls, convert `ReviewToolResult` to existing `ReviewResult`, fall back to text parsing.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/engine/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/engine/reviewer_tools.go internal/engine/reviewer_tools_test.go internal/engine/reviewer.go
git commit -m "feat: add reviewer tool schemas and integrate with senior review"
```

---

### Task 11: Supervisor Tool Schemas + Engine Refactor

**Files:**
- Create: `internal/engine/supervisor_tools.go`
- Create: `internal/engine/supervisor_tools_test.go`
- Modify: `internal/engine/supervisor.go`

- [ ] **Step 1: Write failing test**

Test `SupervisorTools()` returns 2 tools (report_drift, reprioritize). Test `ProcessSupervisorToolCalls` with valid drift report and invalid drift_type.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestSupervisor -v`

- [ ] **Step 3: Implement supervisor_tools.go**

Tools: `report_drift(story_id, drift_type, severity, recommendation)` with enums, `reprioritize(story_id, new_wave, reason)`. Types: `SupervisorToolResult`, `DriftReport`, `Reprioritization`. Implement `ProcessSupervisorToolCalls()`.

- [ ] **Step 4: Update supervisor.go**

Same tool integration pattern. Convert `SupervisorToolResult` to existing `SupervisorResult`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/engine/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/engine/supervisor_tools.go internal/engine/supervisor_tools_test.go internal/engine/supervisor.go
git commit -m "feat: add supervisor tool schemas and integrate with drift detection"
```

---

### Task 12: Manager Tool Schemas + Engine Refactor

**Files:**
- Create: `internal/engine/manager_tools.go`
- Create: `internal/engine/manager_tools_test.go`
- Modify: `internal/engine/manager.go`

- [ ] **Step 1: Write failing test**

Test `ManagerTools()` returns 2 tools (escalation_decision, split_story). Test `ProcessManagerToolCalls` with valid/invalid actions.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/engine/ -run TestManagerTools -v`

- [ ] **Step 3: Implement manager_tools.go**

Tools: `escalation_decision(story_id, action, reason, assigned_to)` with action enum, `split_story(original_story_id, new_stories[])`. Types: `ManagerToolResult`, `EscalationDecision`, `StorySplit`. Implement `ProcessManagerToolCalls()`.

- [ ] **Step 4: Update manager.go**

Same tool integration pattern. Convert `ManagerToolResult` to existing `ManagerAction`.

- [ ] **Step 5: Run tests**

Run: `go test ./internal/engine/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/engine/manager_tools.go internal/engine/manager_tools_test.go internal/engine/manager.go
git commit -m "feat: add manager tool schemas and integrate with escalation handling"
```

---

## Phase 3: Native Gemma Runtime

### Task 13: Gemma Runtime Implementation

**Files:**
- Create: `internal/runtime/gemma.go`
- Create: `internal/runtime/gemma_test.go`
- Modify: `internal/runtime/registry.go`

- [ ] **Step 1: Write failing test**

Create `internal/runtime/gemma_test.go` with tests for: `Name()` returns "gemma", `SupportedModels()` includes "gemma4", `CodingTools()` returns 5 tools, `executeTool` for read_file/write_file/edit_file (using t.TempDir()), path traversal blocked, command allowlist enforced. See spec for full test code.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/runtime/ -run TestGemmaRuntime -v`
Expected: FAIL

- [ ] **Step 3: Implement gemma.go**

Create `GemmaRuntime` with `Execute()` loop (sends tools to LLM, executes tool calls, returns results, loops until task_complete or max iterations). Implement `executeTool()` dispatcher and individual handlers: `execReadFile`, `execWriteFile`, `execEditFile`, `execRunCommand` (with allowlist), `task_complete`. All file operations validate against path traversal via `safePath()`. See spec for complete implementation.

- [ ] **Step 4: Update registry.go**

Add native runtime detection to `NewRegistry`. When `cfg.Native` is true, store config for later `GemmaRuntime` creation (requires LLM client injection at dispatch time).

- [ ] **Step 5: Run tests**

Run: `go test ./internal/runtime/ -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/runtime/gemma.go internal/runtime/gemma_test.go internal/runtime/registry.go
git commit -m "feat: add native Gemma coding runtime with tool-based execution loop"
```

---

## Phase 4: Model Registry Update

### Task 14: Update Model Registry

**Files:**
- Modify: `internal/llm/models.go`
- Modify: `internal/llm/models_test.go`

- [ ] **Step 1: Update models.go**

Replace `RecommendedModels()` to list 4 Gemma 4 models (26b, 31b, e4b, e2b) followed by 4 legacy models (deepseek, qwen variants). Update `ModelForRole()` to return `"gemma4:26b"` for all roles.

- [ ] **Step 2: Update models_test.go**

Update `TestRecommendedModels_AllRolesCovered` to expect 8 models. Update `TestModelForRole_KnownRoles` to expect `"gemma4:26b"` for all roles.

- [ ] **Step 3: Run tests**

Run: `go test ./internal/llm/ -v`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/llm/models.go internal/llm/models_test.go
git commit -m "feat: update model registry with Gemma 4 as default, retain legacy models"
```

---

## Phase 5: Documentation

### Task 15: Gemma 4 User Guide

**Files:**
- Create: `docs/guides/gemma-4-guide.md`

- [ ] **Step 1: Write the guide**

Sections: Why Gemma 4, Quick Start (ollama pull + nxd init + nxd req), Google AI Free Tier (optional, GOOGLE_AI_API_KEY, fallback behavior), Hardware Guide for M4/24GB, Function Calling overview, Choosing a Runtime (gemma native vs aider comparison table), Model Size Guide (e4b/26b/31b), Troubleshooting.

- [ ] **Step 2: Commit**

```bash
git add docs/guides/gemma-4-guide.md
git commit -m "docs: add Gemma 4 user guide with quick start and hardware recommendations"
```

---

### Task 16: Function Calling Reference

**Files:**
- Create: `docs/guides/function-calling.md`

- [ ] **Step 1: Write the reference**

Sections: Overview, Tool Definitions by Role (with JSON examples), Validation and retry behavior, Graceful Degradation, Native Runtime Tools, Adding Custom Tools.

- [ ] **Step 2: Commit**

```bash
git add docs/guides/function-calling.md
git commit -m "docs: add function calling technical reference"
```

---

### Task 17: Migration Guide

**Files:**
- Create: `docs/guides/migration-from-v0.md`

- [ ] **Step 1: Write the migration guide**

Sections: What Changed, Your Existing Config Still Works, Step-by-Step Migration, Config Diff (old vs new), Optional Google AI, Optional Native Runtime, Rollback.

- [ ] **Step 2: Commit**

```bash
git add docs/guides/migration-from-v0.md
git commit -m "docs: add migration guide from v0 (DeepSeek/Qwen) to Gemma 4"
```

---

### Task 18: Update Existing Documentation

**Files:**
- Modify: `README.md`
- Modify: `docs/guides/getting-started.md`
- Modify: `docs/guides/model-selection.md`
- Modify: `docs/guides/configuration.md`
- Modify: `docs/guides/troubleshooting.md`

- [ ] **Step 1: Update README.md**

Prerequisites: gemma4:26b, Hardware table: Gemma 4 sizes, Agent Roles table: new models, Quick Start: gemma4, Configuration: google+ollama example, Documentation table: add new guides.

- [ ] **Step 2: Update getting-started.md**

Rewrite Quick Start for Gemma 4. Move DeepSeek/Qwen to "Alternative Setup (Legacy)".

- [ ] **Step 3: Update model-selection.md**

Add Gemma 4 family with benchmarks and comparison tables.

- [ ] **Step 4: Update configuration.md**

Document google+ollama provider, google_model, fallback_cooldown_s, native runtime config.

- [ ] **Step 5: Update troubleshooting.md**

Add Gemma 4, Google AI, function calling, and native runtime troubleshooting sections.

- [ ] **Step 6: Verify config**

Run: `go build -o /tmp/nxd ./cmd/nxd/ && cd /tmp && rm -f nxd.yaml && /tmp/nxd init && /tmp/nxd config validate`
Expected: PASSED

- [ ] **Step 7: Commit**

```bash
git add README.md docs/guides/
git commit -m "docs: update all guides for Gemma 4 defaults, google provider, and function calling"
```

---

## Phase 6: Final Verification

### Task 19: Full Test Suite + Build Verification

- [ ] **Step 1: Run full test suite with race detection**

Run: `go test ./... -race -count=1`
Expected: PASS

- [ ] **Step 2: Build binary**

Run: `go build -o /tmp/nxd ./cmd/nxd/`
Expected: Success

- [ ] **Step 3: Verify CLI commands**

Run: `/tmp/nxd --help && /tmp/nxd --version`
Then in a temp dir: `/tmp/nxd init && /tmp/nxd config validate && /tmp/nxd config show | head -30 && /tmp/nxd status`
Expected: All succeed, config shows gemma4:26b defaults

- [ ] **Step 4: Verify test coverage**

Run: `go test ./... -coverprofile=coverage.out && go tool cover -func=coverage.out | tail -1`
Expected: >= 80%

- [ ] **Step 5: Final commit if needed**

```bash
git status
```

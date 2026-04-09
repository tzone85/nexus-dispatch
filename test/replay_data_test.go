package test

import (
	"encoding/json"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// --- Planner responses (text mode) ---

func happyPathPlannerResponse() llm.CompletionResponse {
	stories := `[
		{
			"id": "s-001",
			"title": "Implement thread-safe key-value store package",
			"description": "Create a store package with Store struct supporting Get, Set, Delete, List with sync.RWMutex for concurrent access",
			"acceptance_criteria": "All four operations work correctly under concurrent access. List returns sorted keys.",
			"complexity": 3,
			"depends_on": [],
			"owned_files": ["store/store.go"]
		},
		{
			"id": "s-002",
			"title": "Add HTTP API endpoints for key-value store",
			"description": "Add HTTP handlers in main.go for POST/GET/DELETE /kv/{key} and GET /kv using the store package",
			"acceptance_criteria": "All four HTTP endpoints return correct status codes and response bodies",
			"complexity": 3,
			"depends_on": ["s-001"],
			"owned_files": ["main.go"]
		},
		{
			"id": "s-003",
			"title": "Add unit and integration tests",
			"description": "Write unit tests for the store package and HTTP integration tests for the API endpoints",
			"acceptance_criteria": "All tests pass. Store tests cover concurrent access. HTTP tests cover all endpoints.",
			"complexity": 3,
			"depends_on": ["s-001", "s-002"],
			"owned_files": ["store/store_test.go", "main_test.go"]
		}
	]`
	return llm.CompletionResponse{Content: stories, Model: "gemma4:26b"}
}

// --- Reviewer responses ---

func approveReviewResponse() llm.CompletionResponse {
	review := `{"passed": true, "comments": [], "summary": "Clean implementation. Thread safety via RWMutex is correct."}`
	return llm.CompletionResponse{Content: review, Model: "gemma4:26b"}
}

func rejectReviewResponse(feedback string) llm.CompletionResponse {
	review, _ := json.Marshal(map[string]any{
		"passed":   false,
		"comments": []map[string]string{{"file": "store/store.go", "comment": feedback}},
		"summary":  feedback,
	})
	return llm.CompletionResponse{Content: string(review), Model: "gemma4:26b"}
}

// --- Function calling responses (tool call mode) ---

func plannerToolCallResponse() llm.CompletionResponse {
	return llm.CompletionResponse{
		Model: "gemma4:26b",
		ToolCalls: []llm.ToolCall{
			{
				Name: "create_story",
				Arguments: json.RawMessage(`{"title":"Implement thread-safe key-value store package","description":"Create store package with Get, Set, Delete, List and sync.RWMutex","complexity":3,"acceptance_criteria":"All operations work under concurrent access. List returns sorted keys.","dependencies":[]}`),
			},
			{
				Name: "create_story",
				Arguments: json.RawMessage(`{"title":"Add HTTP API endpoints","description":"HTTP handlers for POST/GET/DELETE /kv/{key} and GET /kv","complexity":3,"acceptance_criteria":"All endpoints return correct status codes","dependencies":["s-001"]}`),
			},
			{
				Name: "create_story",
				Arguments: json.RawMessage(`{"title":"Add unit and integration tests","description":"Unit tests for store, integration tests for HTTP API","complexity":3,"acceptance_criteria":"All tests pass with concurrent access coverage","dependencies":["s-001","s-002"]}`),
			},
			{
				Name:      "set_wave_plan",
				Arguments: json.RawMessage(`{"waves":[["s-001"],["s-002"],["s-003"]]}`),
			},
		},
	}
}

func reviewerToolCallResponse() llm.CompletionResponse {
	return llm.CompletionResponse{
		Model: "gemma4:26b",
		ToolCalls: []llm.ToolCall{
			{
				Name:      "submit_review",
				Arguments: json.RawMessage(`{"verdict":"approve","summary":"Clean implementation with correct concurrency patterns","file_comments":[],"suggested_changes":[]}`),
			},
		},
	}
}

// --- Single-story planner responses ---

func singleStoryPlannerResponse() llm.CompletionResponse {
	stories := `[
		{
			"id": "s-001",
			"title": "Implement basic key-value store",
			"description": "Create a simple in-memory key-value store with Get and Set operations",
			"acceptance_criteria": "Get and Set work correctly with string keys and values",
			"complexity": 2,
			"depends_on": [],
			"owned_files": ["store/store.go"]
		}
	]`
	return llm.CompletionResponse{Content: stories, Model: "gemma4:26b"}
}

// twoWavePlannerResponse returns a planner response with 2 stories: wave 1 (s-001) and wave 2 (s-002).
func twoWavePlannerResponse() llm.CompletionResponse {
	stories := `[
		{
			"id": "s-001",
			"title": "Foundation types and interfaces",
			"description": "Define core types and interfaces for the system",
			"acceptance_criteria": "Types compile and are usable by downstream stories",
			"complexity": 2,
			"depends_on": [],
			"owned_files": ["types.go"]
		},
		{
			"id": "s-002",
			"title": "Storage implementation",
			"description": "Implement file-based storage using foundation types",
			"acceptance_criteria": "Storage reads and writes correctly",
			"complexity": 3,
			"depends_on": ["s-001"],
			"owned_files": ["storage.go"]
		}
	]`
	return llm.CompletionResponse{Content: stories, Model: "gemma4:26b"}
}

// --- Diamond dependency responses ---

func diamondDepsPlannerResponse() llm.CompletionResponse {
	stories := `[
		{"id":"s-001","title":"Foundation types","description":"Core types and interfaces","acceptance_criteria":"Types compile","complexity":2,"depends_on":[],"owned_files":["types.go"]},
		{"id":"s-002","title":"Storage layer","description":"File-based storage","acceptance_criteria":"Read/write works","complexity":3,"depends_on":["s-001"],"owned_files":["storage.go"]},
		{"id":"s-003","title":"Validation layer","description":"Input validation","acceptance_criteria":"Validates all inputs","complexity":2,"depends_on":["s-001"],"owned_files":["validate.go"]},
		{"id":"s-004","title":"API integration","description":"Wire storage + validation into API","acceptance_criteria":"API uses both layers","complexity":5,"depends_on":["s-002","s-003"],"owned_files":["api.go"]}
	]`
	return llm.CompletionResponse{Content: stories, Model: "gemma4:26b"}
}

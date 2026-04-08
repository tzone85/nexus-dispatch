//go:build e2e

package test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func replayScenarios() []Scenario {
	return []Scenario{
		scenarioHappyPath(),
		scenarioDiamondDeps(),
		scenarioFunctionCalling(),
	}
}

func scenarioHappyPath() Scenario {
	return Scenario{
		Name:        "happy_path_multi_story",
		Requirement: "Build a key-value store package with Get, Set, Delete, List operations. Thread-safe. Add HTTP API and tests.",
		Fixture:     FixtureConfig{},
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				happyPathPlannerResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(3, 3),
			AssertComplexityRange(1, 13),
			AssertDependenciesValid(),
			AssertEventsEmitted(
				state.EventReqSubmitted,
				state.EventStoryCreated,
				state.EventReqPlanned,
			),
			AssertMinEvents(5),
		},
	}
}

func scenarioDiamondDeps() Scenario {
	return Scenario{
		Name:        "diamond_dependency_chain",
		Requirement: "Build a system with foundation types, storage layer, validation layer, and API that depends on both.",
		Fixture:     FixtureConfig{},
		ReplayOnly:  true,
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				diamondDepsPlannerResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
				approveReviewResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(4, 4),
			AssertEventsEmitted(state.EventStoryCreated, state.EventReqPlanned),
			AssertMinEvents(6),
		},
	}
}

func scenarioFunctionCalling() Scenario {
	return Scenario{
		Name:        "function_calling_round_trip",
		Requirement: "Build a key-value store with Get, Set, Delete, List. Thread-safe. HTTP API.",
		Fixture:     FixtureConfig{},
		Replay: &ReplayConfig{
			Responses: []llm.CompletionResponse{
				plannerToolCallResponse(),
				reviewerToolCallResponse(),
				reviewerToolCallResponse(),
				reviewerToolCallResponse(),
			},
		},
		Assertions: []Assertion{
			AssertStoriesCreated(3, 3),
			AssertComplexityRange(1, 13),
			AssertEventsEmitted(state.EventStoryCreated, state.EventReqPlanned),
		},
	}
}

func TestReplayScenarios(t *testing.T) {
	for _, scenario := range replayScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			RunScenarioWithAssertions(t, scenario, ModeReplay)
		})
	}
}

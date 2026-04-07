//go:build live

package test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func liveScenarios() []Scenario {
	return []Scenario{
		scenarioLiveFullRoundTrip(),
		scenarioLiveHappyPath(),
		scenarioLiveFunctionCalling(),
	}
}

func scenarioLiveFullRoundTrip() Scenario {
	return Scenario{
		Name:     "live_full_round_trip",
		LiveOnly: true,
		Requirement: `Build a key-value store package with the following:
a store package with Store struct that supports Set(key, value string),
Get(key string) (string, bool), Delete(key string), and List() []string
(returns sorted keys). The store must be safe for concurrent access.
Add unit tests for the store package.`,
		Fixture: FixtureConfig{},
		Assertions: []Assertion{
			AssertStoriesCreated(2, 10),
			AssertComplexityRange(1, 13),
			AssertDependenciesValid(),
			AssertEventsEmitted(
				state.EventReqSubmitted,
				state.EventStoryCreated,
				state.EventReqPlanned,
			),
			AssertMinEvents(4),
		},
	}
}

func scenarioLiveHappyPath() Scenario {
	return Scenario{
		Name:        "live_happy_path",
		Requirement: "Build a key-value store with Get, Set, Delete, List. Thread-safe. Add tests.",
		Fixture:     FixtureConfig{},
		Assertions: []Assertion{
			AssertStoriesCreated(2, 10),
			AssertComplexityRange(1, 13),
		},
	}
}

func scenarioLiveFunctionCalling() Scenario {
	return Scenario{
		Name:        "live_function_calling",
		Requirement: "Build a key-value store with Get, Set, Delete, List. Concurrent access safe.",
		Fixture:     FixtureConfig{},
		Assertions: []Assertion{
			AssertStoriesCreated(1, 10),
			AssertComplexityRange(1, 13),
			AssertEventsEmitted(state.EventStoryCreated),
		},
	}
}

func TestLiveScenarios(t *testing.T) {
	RequireOllama(t)
	for _, scenario := range liveScenarios() {
		t.Run(scenario.Name, func(t *testing.T) {
			RunScenarioWithAssertions(t, scenario, ModeLive)
		})
	}
}

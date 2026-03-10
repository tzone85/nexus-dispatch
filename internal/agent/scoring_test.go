package agent_test

import (
	"math"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/agent"
)

func TestComputeReputation(t *testing.T) {
	scores := []agent.Score{
		{AgentID: "jr-1", StoryID: "s-1", Quality: 4, Reliability: 5, DurationS: 120},
		{AgentID: "jr-1", StoryID: "s-2", Quality: 5, Reliability: 5, DurationS: 180},
		{AgentID: "jr-1", StoryID: "s-3", Quality: 3, Reliability: 4, DurationS: 300},
	}

	rep := agent.ComputeReputation(scores)
	if rep.TotalStories != 3 {
		t.Fatalf("expected 3 stories, got %d", rep.TotalStories)
	}
	if math.Abs(rep.AvgQuality-4.0) > 0.01 {
		t.Fatalf("expected avg quality 4.0, got %f", rep.AvgQuality)
	}
	if math.Abs(rep.AvgReliability-4.666) > 0.01 {
		t.Fatalf("expected avg reliability ~4.67, got %f", rep.AvgReliability)
	}
}

func TestComputeReputation_Empty(t *testing.T) {
	rep := agent.ComputeReputation(nil)
	if rep.TotalStories != 0 {
		t.Fatal("expected 0 stories for nil input")
	}
}

func TestOverallScore(t *testing.T) {
	rep := agent.AgentReputation{
		TotalStories:   5,
		AvgQuality:     5.0,
		AvgReliability: 5.0,
		AvgDurationS:   0, // instant
	}
	score := rep.OverallScore()
	if score != 100.0 {
		t.Fatalf("perfect agent should score 100, got %f", score)
	}
}

func TestOverallScore_Zero(t *testing.T) {
	rep := agent.AgentReputation{}
	if rep.OverallScore() != 0 {
		t.Fatal("empty reputation should score 0")
	}
}

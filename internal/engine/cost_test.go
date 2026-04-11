package engine_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/engine"
)

func TestCostCalculator_SingleStory(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Add login", Complexity: 3, Role: "junior"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 0)
	if est.Summary.StoryCount != 1 {
		t.Fatalf("expected 1 story, got %d", est.Summary.StoryCount)
	}
	if est.Summary.TotalPoints != 3 {
		t.Fatalf("expected 3 points, got %d", est.Summary.TotalPoints)
	}
	if est.Summary.HoursLow != 2.0 {
		t.Fatalf("expected hours_low 2.0, got %f", est.Summary.HoursLow)
	}
	if est.Summary.HoursHigh != 3.0 {
		t.Fatalf("expected hours_high 3.0, got %f", est.Summary.HoursHigh)
	}
	if est.Summary.QuoteLow != 300.0 {
		t.Fatalf("expected quote_low 300.0, got %f", est.Summary.QuoteLow)
	}
	if est.Summary.QuoteHigh != 450.0 {
		t.Fatalf("expected quote_high 450.0, got %f", est.Summary.QuoteHigh)
	}
}

func TestCostCalculator_MultipleStories(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 1, Role: "junior"},
		{Title: "Story B", Complexity: 5, Role: "intermediate"},
		{Title: "Story C", Complexity: 8, Role: "senior"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 0)
	if est.Summary.StoryCount != 3 {
		t.Fatalf("expected 3 stories, got %d", est.Summary.StoryCount)
	}
	if est.Summary.TotalPoints != 14 {
		t.Fatalf("expected 14 points, got %d", est.Summary.TotalPoints)
	}
	if est.Summary.HoursLow != 8.5 {
		t.Fatalf("expected hours_low 8.5, got %f", est.Summary.HoursLow)
	}
	if est.Summary.HoursHigh != 14.0 {
		t.Fatalf("expected hours_high 14.0, got %f", est.Summary.HoursHigh)
	}
}

func TestCostCalculator_RateOverride(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 3, Role: "junior"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 175.0)
	if est.Summary.QuoteLow != 350.0 {
		t.Fatalf("expected quote_low 350.0, got %f", est.Summary.QuoteLow)
	}
	if est.Summary.QuoteHigh != 525.0 {
		t.Fatalf("expected quote_high 525.0, got %f", est.Summary.QuoteHigh)
	}
	if est.Summary.Rate != 175.0 {
		t.Fatalf("expected rate 175.0, got %f", est.Summary.Rate)
	}
}

func TestCostCalculator_SubscriptionModeLLMCostZero(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 5, Role: "intermediate"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 0)
	if est.Summary.LLMCost != 0.0 {
		t.Fatalf("expected LLM cost 0.0, got %f", est.Summary.LLMCost)
	}
	if est.Summary.MarginPercent != 100.0 {
		t.Fatalf("expected margin 100%%, got %f", est.Summary.MarginPercent)
	}
}

func TestCostCalculator_UnknownComplexityFallback(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 4, Role: "junior"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 0)
	if est.Summary.HoursLow != 2.0 {
		t.Fatalf("expected fallback hours_low 2.0, got %f", est.Summary.HoursLow)
	}
}

func TestCostCalculator_PerStoryAmounts(t *testing.T) {
	cfg := config.DefaultConfig()
	stories := []engine.StoryEstimate{
		{Title: "Story A", Complexity: 3, Role: "junior"},
		{Title: "Story B", Complexity: 5, Role: "intermediate"},
	}
	est := engine.CalculateCost(stories, cfg.Billing, 0)
	if est.Stories[0].CostLow != 300.0 || est.Stories[0].CostHigh != 450.0 {
		t.Fatalf("story A cost: expected [300, 450], got [%f, %f]", est.Stories[0].CostLow, est.Stories[0].CostHigh)
	}
	if est.Stories[1].CostLow != 450.0 || est.Stories[1].CostHigh != 750.0 {
		t.Fatalf("story B cost: expected [450, 750], got [%f, %f]", est.Stories[1].CostLow, est.Stories[1].CostHigh)
	}
}

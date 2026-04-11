package engine_test

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/engine"
)

func TestQuickEstimate_SimpleRequirement(t *testing.T) {
	stories := engine.QuickEstimate("Add a health check endpoint")
	if len(stories) < 1 {
		t.Fatal("expected at least 1 story")
	}
	for _, s := range stories {
		if s.Complexity > 5 {
			t.Fatalf("simple requirement should not produce complexity > 5, got %d", s.Complexity)
		}
	}
}

func TestQuickEstimate_ConjunctionsIncreaseStories(t *testing.T) {
	simple := engine.QuickEstimate("Add login")
	complex := engine.QuickEstimate("Add login, registration, and password reset")
	if len(complex) <= len(simple) {
		t.Fatalf("conjunctions should produce more stories: simple=%d, complex=%d", len(simple), len(complex))
	}
}

func TestQuickEstimate_ComplexityKeywords(t *testing.T) {
	simple := engine.QuickEstimate("Add a text field")
	complex := engine.QuickEstimate("Add real-time authentication with OAuth2 integration")
	avgSimple := quickAvgComplexity(simple)
	avgComplex := quickAvgComplexity(complex)
	if avgComplex <= avgSimple {
		t.Fatalf("complexity keywords should increase average: simple=%f, complex=%f", avgSimple, avgComplex)
	}
}

func TestQuickEstimate_PluralIndicators(t *testing.T) {
	singular := engine.QuickEstimate("Add OAuth provider")
	plural := engine.QuickEstimate("Add OAuth providers")
	if len(plural) <= len(singular) {
		t.Fatalf("plural should produce more stories: singular=%d, plural=%d", len(singular), len(plural))
	}
}

func TestQuickEstimate_AlwaysProducesAtLeastOne(t *testing.T) {
	stories := engine.QuickEstimate("")
	if len(stories) < 1 {
		t.Fatal("should always produce at least 1 story even for empty input")
	}
}

func TestQuickEstimate_ValidFibonacciComplexity(t *testing.T) {
	valid := map[int]bool{1: true, 2: true, 3: true, 5: true, 8: true, 13: true}
	stories := engine.QuickEstimate("Add authentication with Google and GitHub providers and session management")
	for _, s := range stories {
		if !valid[s.Complexity] {
			t.Fatalf("complexity %d is not a valid Fibonacci value", s.Complexity)
		}
	}
}

func quickAvgComplexity(stories []engine.StoryEstimate) float64 {
	if len(stories) == 0 {
		return 0
	}
	total := 0
	for _, s := range stories {
		total += s.Complexity
	}
	return float64(total) / float64(len(stories))
}

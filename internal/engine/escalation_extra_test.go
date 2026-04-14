package engine

import (
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestMaxRetriesForTier_HighTiers(t *testing.T) {
	routing := config.RoutingConfig{
		MaxRetriesBeforeEscalation: 3,
		MaxSeniorRetries:           2,
		MaxManagerAttempts:         1,
	}
	esc := NewEscalationMachine(nil, routing)

	// Tier 3 = 1 (tech lead re-plan)
	if got := esc.MaxRetriesForTier(3); got != 1 {
		t.Errorf("tier 3: expected 1, got %d", got)
	}
	// Tier 4+ = 0 (pause)
	if got := esc.MaxRetriesForTier(4); got != 0 {
		t.Errorf("tier 4: expected 0, got %d", got)
	}
	if got := esc.MaxRetriesForTier(5); got != 0 {
		t.Errorf("tier 5: expected 0, got %d", got)
	}
}

func TestRetryCountAtCurrentTier_ScopedAfterEscalation(t *testing.T) {
	fs := testEscalationStore(t)

	// Tier 0 failure (before escalation)
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-scope", map[string]any{
		"summary": "tier 0 failure",
	}))

	// Escalate to tier 1
	fs.Append(state.NewEvent(state.EventStoryEscalated, "monitor", "s-scope", map[string]any{
		"from_tier": 0, "to_tier": 1,
	}))

	// Tier 1 failures (NXD only counts EventStoryReviewFailed)
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-scope", map[string]any{
		"summary": "tier 1 fail 1",
	}))
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-scope", map[string]any{
		"summary": "tier 1 fail 2",
	}))

	esc := NewEscalationMachine(fs, defaultRoutingConfig())
	count, err := esc.RetryCountAtCurrentTier("s-scope")
	if err != nil {
		t.Fatalf("RetryCountAtCurrentTier: %v", err)
	}
	// Should count only the 2 failures after escalation
	if count != 2 {
		t.Errorf("expected 2 tier-1 failures, got %d", count)
	}
}

func TestShouldEscalate_AtExactLimit(t *testing.T) {
	fs := testEscalationStore(t)
	routing := config.RoutingConfig{
		MaxRetriesBeforeEscalation: 2,
	}

	// Two failures at tier 0 should trigger escalation (NXD counts ReviewFailed only)
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-exact", map[string]any{
		"summary": "fail 1",
	}))
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-exact", map[string]any{
		"summary": "fail 2",
	}))

	esc := NewEscalationMachine(fs, routing)
	should, nextTier, err := esc.ShouldEscalate("s-exact")
	if err != nil {
		t.Fatalf("ShouldEscalate: %v", err)
	}
	if !should {
		t.Error("expected escalation at exact limit")
	}
	if nextTier != 1 {
		t.Errorf("expected next tier 1, got %d", nextTier)
	}
}

func TestShouldEscalate_BelowLimit(t *testing.T) {
	fs := testEscalationStore(t)
	routing := config.RoutingConfig{
		MaxRetriesBeforeEscalation: 3,
	}

	// Only one failure
	fs.Append(state.NewEvent(state.EventStoryReviewFailed, "reviewer", "s-below", map[string]any{
		"summary": "fail 1",
	}))

	esc := NewEscalationMachine(fs, routing)
	should, tier, err := esc.ShouldEscalate("s-below")
	if err != nil {
		t.Fatalf("ShouldEscalate: %v", err)
	}
	if should {
		t.Error("should not escalate below limit")
	}
	if tier != 0 {
		t.Errorf("expected tier 0, got %d", tier)
	}
}

package engine

import (
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// EscalationMachine tracks escalation tiers, counts retries per tier,
// validates split actions, and mutates the DAG when stories are split.
type EscalationMachine struct {
	eventStore state.EventStore
	routing    config.RoutingConfig
}

// NewEscalationMachine creates an EscalationMachine backed by the given
// event store and routing configuration.
func NewEscalationMachine(es state.EventStore, routing config.RoutingConfig) *EscalationMachine {
	return &EscalationMachine{eventStore: es, routing: routing}
}

// CurrentTier returns the story's current escalation tier: the to_tier of
// the most recent STORY_ESCALATED event (latest timestamp; append order breaks
// ties). Returns 0 if no escalation events exist.
//
// "Latest" — not "highest" — is deliberate. A manager retry de-escalates a
// story by emitting STORY_ESCALATED with to_tier 0; taking the maximum tier
// ever reached made that a no-op and the story was intercepted for manager
// diagnosis on every subsequent wave. The projection (stories.escalation_tier)
// has always used latest-wins, so this also removes a disagreement between
// the two views of the same history.
func (e *EscalationMachine) CurrentTier(storyID string) (int, error) {
	events, err := e.eventStore.List(state.EventFilter{
		Type:    state.EventStoryEscalated,
		StoryID: storyID,
	})
	if err != nil {
		return 0, err
	}
	return latestEscalationTier(events), nil
}

// latestEscalationTier picks the to_tier from the most recent well-formed
// STORY_ESCALATED event in events. Events without a numeric to_tier are
// ignored. Shared by the EscalationMachine and the Dispatcher so both agree
// on what "current tier" means.
func latestEscalationTier(events []state.Event) int {
	tier := 0
	var latest time.Time
	for _, evt := range events {
		payload := state.DecodePayload(evt.Payload)
		toTier, ok := payload["to_tier"].(float64)
		if !ok {
			continue
		}
		if latest.IsZero() || !evt.Timestamp.Before(latest) {
			latest = evt.Timestamp
			tier = int(toTier)
		}
	}
	return tier
}

// lastEscalationTime returns the timestamp of the most recent
// STORY_ESCALATED event for the given story. Returns the zero time if
// no escalation events exist.
func (e *EscalationMachine) lastEscalationTime(storyID string) time.Time {
	events, _ := e.eventStore.List(state.EventFilter{
		Type:    state.EventStoryEscalated,
		StoryID: storyID,
	})
	if len(events) == 0 {
		return time.Time{}
	}

	latest := events[0].Timestamp
	for _, evt := range events[1:] {
		if evt.Timestamp.After(latest) {
			latest = evt.Timestamp
		}
	}
	return latest
}

// RetryCountAtCurrentTier counts STORY_REVIEW_FAILED events that occurred
// after the most recent escalation (in either direction — a manager retry
// that lowers the tier also opens a fresh retry budget). This scopes retry
// counts to the current tier so that failures from prior tiers are not
// double-counted.
func (e *EscalationMachine) RetryCountAtCurrentTier(storyID string) (int, error) {
	after := e.lastEscalationTime(storyID)
	return e.eventStore.Count(state.EventFilter{
		Type:    state.EventStoryReviewFailed,
		StoryID: storyID,
		After:   after,
	})
}

// MaxRetriesForTier returns the retry limit for a given escalation tier.
//
//	Tier 0: config.MaxRetriesBeforeEscalation (same-role retry)
//	Tier 1: config.MaxSeniorRetries (senior retry)
//	Tier 2: config.MaxManagerAttempts (manager diagnosis)
//	Tier 3: 1 (tech_lead re-plan, single attempt)
//	Tier 4+: 0 (pause / human fallback)
func (e *EscalationMachine) MaxRetriesForTier(tier int) int {
	switch tier {
	case 0:
		return e.routing.MaxRetriesBeforeEscalation
	case 1:
		return e.routing.MaxSeniorRetries
	case 2:
		return e.routing.MaxManagerAttempts
	case 3:
		return 1
	default:
		return 0
	}
}

// ShouldEscalate returns whether the story should escalate to the next
// tier based on retry count vs. the tier limit. It also returns the
// target tier. When the current tier's retry limit is reached or
// exceeded, escalation is triggered.
func (e *EscalationMachine) ShouldEscalate(storyID string) (bool, int, error) {
	tier, err := e.CurrentTier(storyID)
	if err != nil {
		return false, 0, err
	}

	count, err := e.RetryCountAtCurrentTier(storyID)
	if err != nil {
		return false, 0, err
	}

	maxRetries := e.MaxRetriesForTier(tier)
	if count >= maxRetries {
		return true, tier + 1, nil
	}
	return false, tier, nil
}

// SplitChild holds data for one child story produced by a split action.
type SplitChild struct {
	ID                 string
	Suffix             string
	Title              string
	Description        string
	AcceptanceCriteria string
	Complexity         int
	OwnedFiles         []string
}

// maxSplitDepth is the maximum nesting depth for story splits.
const maxSplitDepth = 2

// ValidateSplit checks constraints on a proposed split action:
//   - The parent must not exceed the maximum split depth.
//   - No two children may claim the same owned file.
//   - No child may exceed the given maximum complexity.
func (e *EscalationMachine) ValidateSplit(parentSplitDepth int, children []SplitChild, maxComplexity int) error {
	if parentSplitDepth >= maxSplitDepth {
		return fmt.Errorf("max split depth (%d) reached", maxSplitDepth)
	}

	suffixSet := make(map[string]bool, len(children))
	for _, child := range children {
		if suffixSet[child.Suffix] {
			return fmt.Errorf("duplicate child suffix: %q", child.Suffix)
		}
		suffixSet[child.Suffix] = true
	}

	ownedFiles := make(map[string]bool)
	for _, child := range children {
		for _, f := range child.OwnedFiles {
			if ownedFiles[f] {
				return fmt.Errorf("overlapping owned file: %s", f)
			}
			ownedFiles[f] = true
		}
		if child.Complexity > maxComplexity {
			return fmt.Errorf("child complexity %d exceeds max %d", child.Complexity, maxComplexity)
		}
	}
	return nil
}

// ApplySplit mutates the DAG and RunContext for a split action. Each child
// node is added to the DAG with edges to the parent's original
// dependencies, and any nodes that depended on the parent now depend on
// all children. The caller must hold any applicable DAG mutex.
func (e *EscalationMachine) ApplySplit(
	dag *graph.DAG,
	rc *RunContext,
	parentID string,
	children []SplitChild,
	depEdges [][]string,
	parentDeps []string,
	dependents []string,
) {
	for _, child := range children {
		dag.AddNode(child.ID)

		// Inherit parent's original dependencies.
		for _, dep := range parentDeps {
			dag.AddEdge(child.ID, dep)
		}

		// Add the child to the planned stories list.
		rc.PlannedStories = append(rc.PlannedStories, PlannedStory{
			ID:                 child.ID,
			Title:              child.Title,
			Description:        child.Description,
			AcceptanceCriteria: FlexibleString(child.AcceptanceCriteria),
			Complexity:         child.Complexity,
			OwnedFiles:         child.OwnedFiles,
		})
	}

	// Add explicit inter-child dependency edges.
	for _, edge := range depEdges {
		if len(edge) == 2 {
			dag.AddEdge(edge[0], edge[1])
		}
	}

	// Anything that depended on the parent now depends on all children.
	for _, depID := range dependents {
		for _, child := range children {
			dag.AddEdge(depID, child.ID)
		}
	}
}

package engine

import (
	"fmt"
	"log"

	"github.com/tzone85/nexus-dispatch/internal/llm"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// capacityPauseReason builds the standardized pause reason for a transient
// capacity / overload exhaustion at a given pipeline stage. The phrasing tells
// the operator the failure is transient and that resuming after the Ollama
// server recovers will continue from where it left off.
func capacityPauseReason(stage string, err error) string {
	return fmt.Sprintf(
		"Ollama capacity/overload during %s — transient, resume after the server recovers (free a GPU slot / let the model finish loading / free memory): %v",
		stage, err,
	)
}

// pauseIfCapacity inspects an LLM-call error. If it is a transient capacity
// exhaustion (HTTP 429/503/529, server busy, no slots, model loading, OOM,
// connection refused, context deadline), it pauses the requirement cleanly and
// returns true — WITHOUT consuming an escalation attempt or advancing the tier,
// since the failure has nothing to do with the story's quality and will succeed
// once the Ollama server recovers.
//
// Returns false for every other error so the caller's normal handling runs.
func (m *Monitor) pauseIfCapacity(storyID, stage string, err error) bool {
	if !llm.IsCapacityError(err) {
		return false
	}
	log.Printf("[pipeline] Ollama capacity/overload during %s for %s — pausing without escalation: %v",
		stage, storyID, err)
	m.pauseRequirement(storyID, capacityPauseReason(stage, err))
	return true
}

// agentCompletionHasCapacityError reports whether the most recent
// STORY_COMPLETED event for a story carries a capacity/overload signature in
// its recorded error envelope. Native (Gemma) agents run in-process and call
// Ollama directly; when the server is overloaded the agent's completion call
// fails, the executor records the error in the STORY_COMPLETED payload, and the
// agent produces an empty diff. Without this scan a capacity-limited agent looks
// identical to a lazy agent and the story is wrongly escalated as "produced no
// code changes". The error envelope is the only evidence of the transient cause.
func (m *Monitor) agentCompletionHasCapacityError(storyID string) bool {
	events, err := m.eventStore.List(state.EventFilter{
		Type:    state.EventStoryCompleted,
		StoryID: storyID,
	})
	if err != nil || len(events) == 0 {
		return false
	}
	// Scan the latest completion event's recorded error.
	latest := events[len(events)-1]
	payload := state.DecodePayload(latest.Payload)
	errText, _ := payload["error"].(string)
	if errText == "" {
		return false
	}
	return llm.ContainsCapacitySignature(errText)
}

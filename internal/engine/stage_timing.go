package engine

import (
	"log"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// stageEventStore is the narrow surface stage_timing needs from the event +
// projection layer. The Monitor and CLI orchestrators both satisfy this so
// EmitStageCompleted can be called without dragging in the full Monitor.
type stageEventStore interface {
	Append(state.Event) error
}

type stageProjStore interface {
	Project(state.Event) error
}

// EmitStageCompleted writes a STAGE_COMPLETED event with stage, outcome,
// and elapsed duration. Use to instrument pipeline stages so the dashboard
// and reporter can show wall-clock time per stage without re-deriving it
// from a pair of bracketing events.
//
//	stage    "plan" | "dispatch" | "execute" | "review" | "qa" | "merge"
//	outcome  "success" | "failure"
//	storyID  may be empty for pipeline-wide stages (plan, dispatch).
//
// Errors are logged but never returned: stage timing is observability and
// must not abort the caller's pipeline path.
func EmitStageCompleted(es stageEventStore, ps stageProjStore, agentID, storyID, stage, outcome string, started time.Time) {
	if es == nil || ps == nil {
		return
	}
	payload := map[string]any{
		"stage":       stage,
		"outcome":     outcome,
		"duration_ms": time.Since(started).Milliseconds(),
	}
	evt := state.NewEvent(state.EventStageCompleted, agentID, storyID, payload)
	if err := es.Append(evt); err != nil {
		log.Printf("[stage] append STAGE_COMPLETED for %s/%s: %v", storyID, stage, err)
		return
	}
	if err := ps.Project(evt); err != nil {
		log.Printf("[stage] project STAGE_COMPLETED for %s/%s: %v", storyID, stage, err)
	}
}

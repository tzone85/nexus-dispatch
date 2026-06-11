package engine

import (
	"log"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// emitAppender + emitProjector are the narrow store contracts emitEventOrLog
// needs. Both real (state.EventStore, state.ProjectionStore) and the test
// fakes in stage_timing_test.go satisfy these by structural typing — Go
// interfaces don't need explicit conformance.
type emitAppender interface {
	Append(state.Event) error
}

type emitProjector interface {
	Project(state.Event) error
}

// emitEventOrLog persists evt to both the event store and the projection
// store, logging failures instead of swallowing them silently.
//
// Why this exists: monitor.go and controller.go had ~18 fire-and-forget
// .Append() / .Project() call sites that discarded their error. For an
// event-sourced system that is the worst failure mode — a dropped
// STORY_ESCALATED or STORY_SPLIT permanently desyncs the projection from
// the event log, and the system appears healthy while silently stalling.
// Tagged-format logs ("[event-drop]") let log aggregation alert on these.
//
// The helper deliberately does not return the error. Many call sites are in
// deferred cleanups, escalation handlers, and timer-driven goroutines whose
// signatures cannot propagate an error up. Returning would force a wider
// refactor; a tagged-log floor is the minimum the audit calls for and
// turns invisible drops into greppable noise.
func emitEventOrLog(es emitAppender, ps emitProjector, evt state.Event) {
	if es == nil || ps == nil {
		log.Printf("[event-drop] missing store; type=%s story=%s", evt.Type, evt.StoryID)
		return
	}
	if err := es.Append(evt); err != nil {
		log.Printf("[event-drop] append failed; type=%s story=%s: %v", evt.Type, evt.StoryID, err)
		return
	}
	if err := ps.Project(evt); err != nil {
		// Append succeeded but projection failed → durable desync. Operators
		// need to see this; replaying the event log will recover the
		// projection on next startup, so this is loud-not-fatal.
		log.Printf("[event-partial] append ok but project failed; type=%s story=%s: %v",
			evt.Type, evt.StoryID, err)
		return
	}
}

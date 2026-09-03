package engine

import (
	"crypto/rand"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Story attempts.
//
// A story can be dispatched many times (review reset, QA failure, manager
// retry, crash recovery). Each dispatch is one *attempt*: one agent, one
// worktree run, one STORY_STARTED → STORY_COMPLETED lifecycle. Before attempt
// ids existed every event was keyed by story id only, so a STORY_COMPLETED
// left behind by attempt 1 looked identical to attempt 2 finishing — the
// monitor would run the post-execution pipeline against a worktree the new
// agent had barely touched.
//
// The Dispatcher mints one attempt id per assignment; the Executor and
// Monitor stamp it onto every event they emit for that run via
// state.NewEventForAttempt, and attempt-sensitive checks
// (nativeAgentCompleted) filter on it.

// newAttemptID returns a unique, sortable attempt id for one dispatch of
// storyID. The story id prefix keeps log lines and dashboards readable; the
// ULID suffix makes the id unique across re-dispatches and processes.
func newAttemptID(storyID string) string {
	return storyID + "-a" + ulid.MustNew(ulid.Timestamp(time.Now()), rand.Reader).String()
}

// nativeAgentCompleted reports whether the event store holds a
// STORY_COMPLETED for this story's *current attempt*. When attemptID is set
// only events stamped with it count, so a stale completion from an earlier
// attempt can never complete a later one. An empty attemptID (legacy callers,
// agents recovered from pre-attempt logs) falls back to "any STORY_COMPLETED
// for the story", which is the pre-attempt behaviour.
func nativeAgentCompleted(es state.EventStore, storyID, attemptID string) bool {
	events, err := es.List(state.EventFilter{
		Type:      state.EventStoryCompleted,
		StoryID:   storyID,
		AttemptID: attemptID,
		Limit:     1, // we only need to know whether ANY exist; perf win on long-lived stores
	})
	return err == nil && len(events) > 0
}

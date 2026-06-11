package engine

import (
	"context"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
	"github.com/tzone85/nexus-dispatch/internal/devdb/null"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func TestOutcomeForGracefulShutdown(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	deadlined, cancel2 := context.WithDeadline(context.Background(), time.Unix(0, 0))
	defer cancel2()

	tests := []struct {
		name    string
		parent  context.Context
		current devdb.StoryOutcome
		want    devdb.StoryOutcome
	}{
		{
			name:    "ok parent leaves failed alone",
			parent:  context.Background(),
			current: devdb.OutcomeFailed,
			want:    devdb.OutcomeFailed,
		},
		{
			name:    "cancelled parent rewrites failed to paused",
			parent:  cancelled,
			current: devdb.OutcomeFailed,
			want:    devdb.OutcomePaused,
		},
		{
			name:    "cancelled parent leaves success alone",
			parent:  cancelled,
			current: devdb.OutcomeSuccess,
			want:    devdb.OutcomeSuccess,
		},
		{
			name:    "deadline exceeded stays failed (real timeout)",
			parent:  deadlined,
			current: devdb.OutcomeFailed,
			want:    devdb.OutcomeFailed,
		},
		{
			name:    "nil parent leaves outcome alone",
			parent:  nil,
			current: devdb.OutcomeFailed,
			want:    devdb.OutcomeFailed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := outcomeForGracefulShutdown(tc.parent, tc.current)
			if got != tc.want {
				t.Fatalf("outcomeForGracefulShutdown = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestReleaseDevDB_NilLifecycle(t *testing.T) {
	if err := releaseDevDB(nil, devdb.DB{ID: "x"}, devdb.OutcomeSuccess); err != nil {
		t.Fatalf("nil lifecycle should be a no-op, got %v", err)
	}
}

// noopAppender swallows events for tests that don't care to assert them.
type noopAppender struct{}

func (noopAppender) Append(state.Event) error { return nil }

// TestReleaseDevDB_BoundedByTimeout asserts that a happy-path Release call
// against the null provider completes well under devdbReleaseTimeout. The
// regression we're guarding against is the prior `context.Background()`
// usage that gave the call no deadline at all — a hung Docker daemon could
// then pin the monitor goroutine forever. The null provider returns
// instantly; if anyone ever wires a context-ignoring sleep into Lifecycle
// or null.Provider.Delete, this assertion fires.
func TestReleaseDevDB_BoundedByTimeout(t *testing.T) {
	lc := devdb.NewLifecycle(null.New(), noopAppender{}, devdb.Config{Provider: "null"})
	db := devdb.DB{ID: "noop", Provider: "null"}
	start := time.Now()
	if err := releaseDevDB(lc, db, devdb.OutcomeFailed); err != nil {
		t.Fatalf("release: %v", err)
	}
	if elapsed := time.Since(start); elapsed > devdbReleaseTimeout {
		t.Fatalf("release blew the timeout: %v > %v", elapsed, devdbReleaseTimeout)
	}
}

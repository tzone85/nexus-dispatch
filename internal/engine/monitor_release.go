package engine

import (
	"context"
	"errors"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// devdbReleaseTimeout bounds how long postExecutionPipeline's defer will
// wait for the devdb provider (typically Docker) to release a story DB.
// Tested side-effect: a hung Docker daemon can no longer wedge the
// monitor poll loop indefinitely.
const devdbReleaseTimeout = 30 * time.Second

// releaseDevDB calls lifecycle.Release behind a fresh, bounded context.
// The parent pipeline context cannot be reused — it has either expired
// (5-minute pipeline deadline fired) or been cancelled (shutdown), and
// devdb.Lifecycle implementations expect a usable ctx for their backend
// (e.g. Docker pg DROP). context.Background() alone is unsafe because
// it has no deadline and the goroutine would leak on a broken daemon.
func releaseDevDB(lc *devdb.Lifecycle, db devdb.DB, outcome devdb.StoryOutcome) error {
	if lc == nil {
		return nil
	}
	releaseCtx, cancel := context.WithTimeout(context.Background(), devdbReleaseTimeout)
	defer cancel()
	return lc.Release(releaseCtx, db, outcome)
}

// outcomeForGracefulShutdown rewrites the release outcome when the parent
// pipeline context was cancelled (Ctrl-C, daemon stop) rather than
// completing. A graceful shutdown is not a failure — recording it as
// such pollutes the metrics dashboard and triggers false post-mortem
// investigations. Mapping to devdb.OutcomePaused matches how a
// human-initiated pause is recorded elsewhere.
//
// Two important invariants:
//
//   - context.DeadlineExceeded (our own 5-minute pipeline timeout) is left
//     as failed: that is a real "pipeline could not finish" signal worth
//     surfacing.
//   - Outcomes other than OutcomeFailed are untouched. A merge that
//     succeeded just before shutdown still records OutcomeSuccess.
func outcomeForGracefulShutdown(parentCtx context.Context, current devdb.StoryOutcome) devdb.StoryOutcome {
	if current != devdb.OutcomeFailed {
		return current
	}
	if parentCtx == nil {
		return current
	}
	if errors.Is(parentCtx.Err(), context.Canceled) {
		return devdb.OutcomePaused
	}
	return current
}

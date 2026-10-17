package state

import (
	"database/sql"
	"errors"
	"fmt"
)

// BackfillStoryBranches sets stories.branch from STORY_STARTED events for
// rows projected before projectStoryStarted persisted the branch. It only
// touches rows whose branch is still empty, so an already-populated branch is
// never changed. The LAST branch per story wins, matching what a replay
// through projectStoryStarted produces, so a backfilled and a rebuilt
// projection agree. One UPDATE per story, not per event.
//
// It runs once: a projection_meta flag records completion so read-only
// commands do not rescan the event log and take the write lock on every
// start (see NeedsStoryBranchBackfill). All updates run in one transaction
// under the store mutex.
func (s *SQLiteStore) BackfillStoryBranches(events []Event) error {
	// The scan below runs BEFORE s.mu is taken on purpose: decodePayload is
	// pure, the events are the caller's slice, and holding the store mutex
	// across a full log scan would block every reader for no reason.
	last := make(map[string]string)
	for _, evt := range events {
		if evt.Type != EventStoryStarted || evt.StoryID == "" || evt.Payload == nil {
			continue
		}
		if b := payloadStr(s.decodePayload(evt), "branch"); b != "" {
			last[evt.StoryID] = b
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("backfill branches: begin: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // no-op after a successful Commit
	for id, b := range last {
		if _, err := tx.Exec(`UPDATE stories SET branch = ? WHERE id = ? AND branch = ''`, b, id); err != nil {
			return fmt.Errorf("backfill branch for %s: %w", id, err)
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO projection_meta (key, value) VALUES (?, 1)`, branchBackfillKey); err != nil {
		return fmt.Errorf("backfill branches: record completion: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("backfill branches: commit: %w", err)
	}
	return nil
}

// branchBackfillKey is the projection_meta row set once BackfillStoryBranches
// has run against this database.
const branchBackfillKey = "story_branch_backfill_done"

// NeedsStoryBranchBackfill reports whether BackfillStoryBranches has not yet
// run against this projection.
func (s *SQLiteStore) NeedsStoryBranchBackfill() (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var v int
	err := s.db.QueryRow(`SELECT value FROM projection_meta WHERE key = ?`, branchBackfillKey).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", branchBackfillKey, err)
	}
	return v == 0, nil
}

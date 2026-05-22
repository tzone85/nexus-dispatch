package devdb

import (
	"context"
	"strings"
	"time"
)

// FindOrphans returns DBs that:
//   - have a name starting with "<prefix>-"
//   - have a parsed story ID NOT in activeStoryIDs
//
// Used by `nxd resume` to identify DBs left behind by previously crashed pipelines.
func FindOrphans(ctx context.Context, p Provider, prefix string, activeStoryIDs []string) ([]DB, error) {
	all, err := p.List(ctx)
	if err != nil {
		return nil, err
	}
	active := make(map[string]struct{}, len(activeStoryIDs))
	for _, id := range activeStoryIDs {
		active[id] = struct{}{}
	}

	var orphans []DB
	for _, db := range all {
		if !strings.HasPrefix(db.Name, prefix+"-") {
			continue
		}
		story := ParseStoryID(prefix, db.Name)
		if _, ok := active[story]; ok {
			continue
		}
		orphans = append(orphans, db)
	}
	return orphans, nil
}

// ReleaseOrphans deletes orphan DBs older than minAge.
// Younger orphans are returned in `kept` for human review.
// Errors during Delete are accumulated (first error returned); per-DB
// progress is preserved (already-deleted DBs are in `deleted`).
func ReleaseOrphans(ctx context.Context, p Provider, orphans []DB, minAge time.Duration) (deleted, kept []DB, err error) {
	cutoff := time.Now().Add(-minAge)
	var firstErr error
	for _, db := range orphans {
		if db.CreatedAt.After(cutoff) {
			kept = append(kept, db)
			continue
		}
		if delErr := p.Delete(ctx, db.ID); delErr != nil {
			if firstErr == nil {
				firstErr = delErr
			}
			kept = append(kept, db)
			continue
		}
		deleted = append(deleted, db)
	}
	return deleted, kept, firstErr
}

package docker

import (
	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// CollectOrphansByName is a pure function: from a list of candidate DB names,
// return those that match the prefix and whose parsed story-id is not in the
// active set. The higher-level provider GC wraps this with a real
// List() call. Tests using this helper avoid any I/O dependency.
//
// Names that don't pass devdb.IsValid or whose story-id can't be parsed are
// silently skipped — invalid names cannot be orphans of our own bookkeeping.
func CollectOrphansByName(candidates []string, prefix string, activeStoryIDs []string) []string {
	active := make(map[string]struct{}, len(activeStoryIDs))
	for _, id := range activeStoryIDs {
		active[id] = struct{}{}
	}
	var out []string
	for _, name := range candidates {
		if !devdb.IsValid(name) {
			continue
		}
		story := devdb.ParseStoryID(prefix, name)
		if story == "" {
			continue
		}
		if _, ok := active[story]; ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

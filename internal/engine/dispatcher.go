package engine

import (
	"fmt"
	"path/filepath"

	"github.com/tzone85/nexus-dispatch/internal/agent"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Assignment represents a story routed to a specific agent role with session
// and branch metadata.
type Assignment struct {
	StoryID      string
	Role         agent.Role
	AgentID      string
	SessionName  string
	WorktreePath string
	Branch       string
}

// Dispatcher routes ready stories to agent roles based on complexity and
// emits assignment events.
type Dispatcher struct {
	config     config.Config
	eventStore state.EventStore
	projStore  state.ProjectionStore
}

// NewDispatcher creates a Dispatcher wired to the given configuration, event
// store, and projection store.
func NewDispatcher(cfg config.Config, es state.EventStore, ps state.ProjectionStore) *Dispatcher {
	return &Dispatcher{
		config:     cfg,
		eventStore: es,
		projStore:  ps,
	}
}

// DispatchWave identifies stories ready for execution (all dependencies
// satisfied) and assigns each to an agent role based on complexity. It returns
// assignments for all dispatchable stories and emits AGENT_SPAWNED and
// STORY_ASSIGNED events. waveNumber is recorded on the STORY_ASSIGNED event
// so that the completion summary can group stories by wave.
//
// Sequential-first ordering: if any ready stories have WaveHint=="sequential",
// only one sequential story is dispatched. Otherwise, parallel stories are
// dispatched with overlap filtering to prevent file conflicts.
func (d *Dispatcher) DispatchWave(dag *graph.DAG, completed map[string]bool, reqID string, stories []PlannedStory, waveNumber int) ([]Assignment, error) {
	readyIDs := dag.ReadyNodes(completed)
	if len(readyIDs) == 0 {
		return nil, nil
	}

	storyMap := make(map[string]PlannedStory, len(stories))
	for _, s := range stories {
		storyMap[s.ID] = s
	}

	// Collect ready stories and auto-tag wave hints
	readyStories := make([]PlannedStory, 0, len(readyIDs))
	for _, id := range readyIDs {
		if s, ok := storyMap[id]; ok {
			readyStories = append(readyStories, s)
		}
	}
	d.autoTagWaveHints(readyStories)

	// Determine which stories to dispatch this wave
	dispatchable := d.selectDispatchable(readyStories)

	assignments := make([]Assignment, 0, len(dispatchable))
	agentCounter := 0

	for _, story := range dispatchable {
		role := d.routeStory(story)
		agentCounter++
		agentID := fmt.Sprintf("%s-%s-%d", role, reqID, agentCounter)
		sessionName := fmt.Sprintf("nxd-%s-%s-%d", reqID, role, agentCounter)
		branch := fmt.Sprintf("nxd/%s", story.ID)

		assignment := Assignment{
			StoryID:     story.ID,
			Role:        role,
			AgentID:     agentID,
			SessionName: sessionName,
			Branch:      branch,
		}
		assignments = append(assignments, assignment)

		// Emit spawn event
		spawnEvt := state.NewEvent(state.EventAgentSpawned, agentID, story.ID, map[string]any{
			"role":         string(role),
			"session_name": sessionName,
		})
		if err := d.eventStore.Append(spawnEvt); err != nil {
			return nil, fmt.Errorf("emit agent spawned: %w", err)
		}

		// Emit assignment event
		assignEvt := state.NewEvent(state.EventStoryAssigned, agentID, story.ID, map[string]any{
			"agent_id": agentID,
			"wave":     waveNumber,
		})
		if err := d.eventStore.Append(assignEvt); err != nil {
			return nil, fmt.Errorf("emit story assigned: %w", err)
		}
		if err := d.projStore.Project(assignEvt); err != nil {
			return nil, fmt.Errorf("project story assigned: %w", err)
		}
	}

	return assignments, nil
}

// autoTagWaveHints assigns wave hints to stories that don't already have one
// by checking if any owned file matches a sequential file pattern from config.
func (d *Dispatcher) autoTagWaveHints(stories []PlannedStory) {
	for i := range stories {
		if stories[i].WaveHint != "" {
			continue
		}
		for _, f := range stories[i].OwnedFiles {
			if d.matchesSequentialPattern(f) {
				stories[i].WaveHint = "sequential"
				break
			}
		}
		if stories[i].WaveHint == "" {
			stories[i].WaveHint = "parallel"
		}
	}
}

// matchesSequentialPattern checks if a file path matches any of the configured
// sequential file patterns using filepath.Match.
func (d *Dispatcher) matchesSequentialPattern(file string) bool {
	for _, pattern := range d.config.Planning.SequentialFilePatterns {
		// Match against the full path
		if matched, _ := filepath.Match(pattern, file); matched {
			return true
		}
		// Also match against just the filename for patterns like "package.json"
		if matched, _ := filepath.Match(pattern, filepath.Base(file)); matched {
			return true
		}
	}
	return false
}

// selectDispatchable applies sequential-first ordering and overlap filtering
// to determine which ready stories should be dispatched in this wave.
func (d *Dispatcher) selectDispatchable(readyStories []PlannedStory) []PlannedStory {
	// Check if any ready stories are sequential
	var sequential []PlannedStory
	var parallel []PlannedStory
	for _, s := range readyStories {
		if s.WaveHint == "sequential" {
			sequential = append(sequential, s)
		} else {
			parallel = append(parallel, s)
		}
	}

	// Sequential-first: if any sequential stories are ready, dispatch only
	// one of them (they modify shared/core files and must run alone).
	if len(sequential) > 0 {
		return []PlannedStory{sequential[0]}
	}

	// For parallel stories, filter out those that share owned files with
	// stories already selected for this wave.
	return d.filterOverlapping(parallel)
}

// filterOverlapping removes stories whose owned files overlap with
// already-selected stories in this wave.
func (d *Dispatcher) filterOverlapping(stories []PlannedStory) []PlannedStory {
	dispatched := make([]PlannedStory, 0, len(stories))
	claimedFiles := make(map[string]bool)

	for _, s := range stories {
		if d.hasFileConflict(s, claimedFiles) {
			continue
		}
		// Claim this story's files
		for _, f := range s.OwnedFiles {
			claimedFiles[f] = true
		}
		dispatched = append(dispatched, s)
	}

	return dispatched
}

// hasFileConflict checks if any of the story's owned files are already claimed.
func (d *Dispatcher) hasFileConflict(story PlannedStory, claimed map[string]bool) bool {
	for _, f := range story.OwnedFiles {
		if claimed[f] {
			return true
		}
	}
	return false
}

// routeStory determines the agent role for a story. If the story has an
// ESCALATION_CREATED event, it is routed to a senior agent regardless of
// complexity. Otherwise, it falls back to complexity-based routing.
func (d *Dispatcher) routeStory(story PlannedStory) agent.Role {
	escalationCount, err := d.eventStore.Count(state.EventFilter{
		Type:    state.EventEscalationCreated,
		StoryID: story.ID,
	})
	if err == nil && escalationCount > 0 {
		return agent.RoleSenior
	}
	return agent.RouteByComplexity(story.Complexity, d.config.Routing)
}

package engine

import (
	"fmt"

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
// STORY_ASSIGNED events.
func (d *Dispatcher) DispatchWave(dag *graph.DAG, completed map[string]bool, reqID string, stories []PlannedStory) ([]Assignment, error) {
	readyIDs := dag.ReadyNodes(completed)
	if len(readyIDs) == 0 {
		return nil, nil
	}

	storyMap := make(map[string]PlannedStory, len(stories))
	for _, s := range stories {
		storyMap[s.ID] = s
	}

	assignments := make([]Assignment, 0, len(readyIDs))
	agentCounter := 0

	for _, storyID := range readyIDs {
		story, ok := storyMap[storyID]
		if !ok {
			continue
		}

		role := agent.RouteByComplexity(story.Complexity, d.config.Routing)
		agentCounter++
		agentID := fmt.Sprintf("%s-%s-%d", role, reqID, agentCounter)
		sessionName := fmt.Sprintf("vxd-%s-%s-%d", reqID, role, agentCounter)
		branch := fmt.Sprintf("vxd/%s", storyID)

		assignment := Assignment{
			StoryID:     storyID,
			Role:        role,
			AgentID:     agentID,
			SessionName: sessionName,
			Branch:      branch,
		}
		assignments = append(assignments, assignment)

		// Emit spawn event
		spawnEvt := state.NewEvent(state.EventAgentSpawned, agentID, storyID, map[string]any{
			"role":         string(role),
			"session_name": sessionName,
		})
		if err := d.eventStore.Append(spawnEvt); err != nil {
			return nil, fmt.Errorf("emit agent spawned: %w", err)
		}

		// Emit assignment event
		assignEvt := state.NewEvent(state.EventStoryAssigned, agentID, storyID, map[string]any{
			"agent_id": agentID,
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

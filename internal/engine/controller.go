package engine

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ControlActionKind identifies the type of control action taken.
type ControlActionKind string

const (
	ActionCancel       ControlActionKind = "cancel"
	ActionRestart      ControlActionKind = "restart"
	ActionReprioritize ControlActionKind = "reprioritize"
)

// ControlAction records a single control decision.
type ControlAction struct {
	Kind    ControlActionKind `json:"kind"`
	StoryID string            `json:"story_id"`
	Reason  string            `json:"reason"`
}

// Controller is an active periodic controller that monitors story progress
// and takes corrective actions (cancel, restart, reprioritize) based on
// stuck detection and optional LLM-powered drift analysis.
type Controller struct {
	config     config.ControllerConfig
	supervisor *Supervisor
	eventStore state.EventStore
	projStore  state.ProjectionStore

	mu            sync.Mutex
	lastActionAt  time.Time
	cancelFuncs   map[string]context.CancelFunc // storyID -> cancel for native runtimes
}

// NewController creates a Controller. The supervisor may be nil if LLM-based
// drift analysis is not available.
func NewController(cfg config.ControllerConfig, sup *Supervisor, es state.EventStore, ps state.ProjectionStore) *Controller {
	return &Controller{
		config:      cfg,
		supervisor:  sup,
		eventStore:  es,
		projStore:   ps,
		cancelFuncs: make(map[string]context.CancelFunc),
	}
}

// RegisterCancel stores a cancel function for a native runtime story, enabling
// the controller to stop it if stuck.
func (c *Controller) RegisterCancel(storyID string, cancel context.CancelFunc) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cancelFuncs[storyID] = cancel
}

// DeregisterCancel removes the cancel function for a story, called by the
// runtime goroutine on normal completion. Without this the cancelFuncs map
// would grow unbounded across the lifetime of the daemon (H5).
func (c *Controller) DeregisterCancel(storyID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.cancelFuncs, storyID)
}

// RunLoop ticks at the configured interval and analyzes active stories.
// It blocks until ctx is cancelled.
func (c *Controller) RunLoop(ctx context.Context) {
	if !c.config.Enabled {
		return
	}

	interval := time.Duration(c.config.IntervalS) * time.Second
	if interval <= 0 {
		interval = 60 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	log.Printf("[controller] started, interval=%s, stuck_threshold=%ds", interval, c.config.MaxStuckDurationS)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.tick(ctx)
		}
	}
}

func (c *Controller) tick(ctx context.Context) {
	stories, err := c.projStore.ListStories(state.StoryFilter{Status: "in_progress"})
	if err != nil {
		log.Printf("[controller] list stories: %v", err)
		return
	}

	if len(stories) == 0 {
		return
	}

	// Check cooldown.
	c.mu.Lock()
	if !c.lastActionAt.IsZero() && time.Since(c.lastActionAt) < time.Duration(c.config.CooldownS)*time.Second {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	stuckThreshold := time.Duration(c.config.MaxStuckDurationS) * time.Second
	actionsThisTick := 0

	for _, story := range stories {
		if actionsThisTick >= c.config.MaxActionsPerTick {
			break
		}

		// Check if story is stuck by looking for recent progress events.
		lastProgress := c.lastProgressTime(story.ID)
		stuckDuration := time.Since(lastProgress)

		if stuckDuration < stuckThreshold {
			continue
		}

		log.Printf("[controller] story %s stuck for %s (threshold: %s)",
			story.ID, stuckDuration.Round(time.Second), stuckThreshold)

		// Emit stuck detection event for observability.
		c.eventStore.Append(state.NewEvent(state.EventControllerStuckDetected, "controller", story.ID, map[string]any{
			"stuck_duration_s": int(stuckDuration.Seconds()),
			"threshold_s":      c.config.MaxStuckDurationS,
			"escalation_tier":  story.EscalationTier,
		}))

		action := c.decideAction(story)
		if action == nil {
			continue
		}

		c.executeAction(ctx, *action)
		actionsThisTick++

		c.mu.Lock()
		c.lastActionAt = time.Now()
		c.mu.Unlock()
	}

	// Emit analysis event.
	c.eventStore.Append(state.NewEvent(state.EventControllerAnalysis, "controller", "", map[string]any{
		"stories_checked": len(stories),
		"actions_taken":   actionsThisTick,
	}))
}

func (c *Controller) lastProgressTime(storyID string) time.Time {
	// Check STORY_PROGRESS events first, then fall back to STORY_STARTED.
	events, _ := c.eventStore.List(state.EventFilter{
		Type:    state.EventStoryProgress,
		StoryID: storyID,
	})
	if len(events) > 0 {
		return events[len(events)-1].Timestamp
	}

	events, _ = c.eventStore.List(state.EventFilter{
		Type:    state.EventStoryStarted,
		StoryID: storyID,
	})
	if len(events) > 0 {
		return events[len(events)-1].Timestamp
	}

	return time.Time{}
}

func (c *Controller) decideAction(story state.Story) *ControlAction {
	// Priority: reprioritize > restart > cancel.
	// Reprioritize bumps to next tier; restart resets; cancel just stops.
	if c.config.AutoReprioritize {
		return &ControlAction{
			Kind:    ActionReprioritize,
			StoryID: story.ID,
			Reason:  "stuck beyond threshold, reprioritizing to higher tier",
		}
	}
	if c.config.AutoRestart {
		return &ControlAction{
			Kind:    ActionRestart,
			StoryID: story.ID,
			Reason:  "stuck beyond threshold, restarting",
		}
	}
	if c.config.AutoCancel {
		return &ControlAction{
			Kind:    ActionCancel,
			StoryID: story.ID,
			Reason:  "stuck beyond threshold",
		}
	}
	return nil
}

func (c *Controller) executeAction(ctx context.Context, action ControlAction) {
	log.Printf("[controller] executing %s on %s: %s", action.Kind, action.StoryID, action.Reason)

	switch action.Kind {
	case ActionCancel:
		c.cancelStory(action.StoryID)
	case ActionRestart:
		c.cancelStory(action.StoryID)
		c.resetStoryToDraft(action.StoryID, action.Reason)
	case ActionReprioritize:
		c.reprioritizeStory(action.StoryID, action.Reason)
	}

	c.eventStore.Append(state.NewEvent(state.EventControllerAction, "controller", action.StoryID, map[string]any{
		"kind":   string(action.Kind),
		"reason": action.Reason,
	}))
}

func (c *Controller) cancelStory(storyID string) {
	c.mu.Lock()
	cancel, ok := c.cancelFuncs[storyID]
	if ok {
		delete(c.cancelFuncs, storyID)
	}
	c.mu.Unlock()

	if ok {
		cancel()
		log.Printf("[controller] cancelled native runtime for %s", storyID)
	}

	c.eventStore.Append(state.NewEvent(state.EventAgentTerminated, "controller", storyID, map[string]any{
		"reason": "controller cancelled stuck agent",
	}))
}

func (c *Controller) resetStoryToDraft(storyID, reason string) {
	evt := state.NewEvent(state.EventStoryRecovery, "controller", storyID, map[string]any{
		"new_status": "draft",
		"reason":     reason,
	})
	c.eventStore.Append(evt)
	c.projStore.Project(evt)
}

// reprioritizeStory cancels the current agent, bumps the story's escalation
// tier, and resets it to draft so it gets re-dispatched at a higher tier.
func (c *Controller) reprioritizeStory(storyID, reason string) {
	c.cancelStory(storyID)

	// Bump escalation tier via event.
	story, err := c.projStore.GetStory(storyID)
	if err != nil {
		log.Printf("[controller] get story %s for reprioritize: %v", storyID, err)
		return
	}

	nextTier := story.EscalationTier + 1
	escEvt := state.NewEvent(state.EventStoryEscalated, "controller", storyID, map[string]any{
		"from_tier": story.EscalationTier,
		"to_tier":   nextTier,
		"reason":    reason,
		"source":    "controller",
	})
	c.eventStore.Append(escEvt)
	c.projStore.Project(escEvt)

	// Reset to draft for re-dispatch.
	c.resetStoryToDraft(storyID, reason)
}

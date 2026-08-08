package engine

import (
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// TimelineEvent is one human-readable moment in a requirement's history.
type TimelineEvent struct {
	Time    time.Time
	StoryID string
	Label   string
}

// TimelineStory is a per-story rollup: when it ran and how long it took.
type TimelineStory struct {
	ID       string
	Title    string
	Status   string
	Wave     int
	Started  time.Time
	Ended    time.Time
	Duration time.Duration
}

// Timeline is the reconstructed story of one requirement, built purely from
// the event log + projection. `nxd timeline` renders it; the web dashboard
// could reuse it unchanged.
type Timeline struct {
	ReqID   string
	Title   string
	Status  string
	Start   time.Time
	End     time.Time
	Entries []TimelineEvent
	Stories []TimelineStory
	Merged  int
	Total   int
}

// timelineSkip lists event types too noisy for a human timeline — heartbeats,
// per-iteration progress, and periodic controller/supervisor ticks.
var timelineSkip = map[state.EventType]bool{
	state.EventStoryProgress:      true,
	state.EventAgentCheckpoint:    true,
	state.EventControllerAnalysis: true,
	state.EventSupervisorCheck:    true,
	state.EventStageCompleted:     true,
	state.EventDirectiveAcked:     true,
	state.EventReqPlanningStarted: true,
}

// BuildTimeline reconstructs the requirement's chronology. Pure: it never
// touches stores, so it is trivially testable and reusable.
func BuildTimeline(req state.Requirement, stories []state.Story, events []state.Event) Timeline {
	tl := Timeline{
		ReqID:  req.ID,
		Title:  req.Title,
		Status: req.Status,
		Total:  len(stories),
	}

	storyByID := make(map[string]state.Story, len(stories))
	for _, s := range stories {
		storyByID[s.ID] = s
		if s.Status == "merged" {
			tl.Merged++
		}
	}

	started := map[string]time.Time{}
	ended := map[string]time.Time{}

	for _, evt := range events {
		if !timelineEventBelongs(evt, req.ID, storyByID) || timelineSkip[evt.Type] {
			continue
		}
		if tl.Start.IsZero() || evt.Timestamp.Before(tl.Start) {
			tl.Start = evt.Timestamp
		}
		if evt.Timestamp.After(tl.End) {
			tl.End = evt.Timestamp
		}
		switch evt.Type {
		case state.EventStoryStarted:
			if t, ok := started[evt.StoryID]; !ok || evt.Timestamp.Before(t) {
				started[evt.StoryID] = evt.Timestamp
			}
		case state.EventStoryMerged, state.EventStoryCompleted:
			if evt.Timestamp.After(ended[evt.StoryID]) {
				ended[evt.StoryID] = evt.Timestamp
			}
		}
		tl.Entries = append(tl.Entries, TimelineEvent{
			Time:    evt.Timestamp,
			StoryID: evt.StoryID,
			Label:   timelineLabel(evt, storyByID),
		})
	}

	for _, s := range stories {
		ts := TimelineStory{
			ID:      s.ID,
			Title:   s.Title,
			Status:  s.Status,
			Wave:    s.Wave,
			Started: started[s.ID],
			Ended:   ended[s.ID],
		}
		if !ts.Started.IsZero() && !ts.Ended.IsZero() && ts.Ended.After(ts.Started) {
			ts.Duration = ts.Ended.Sub(ts.Started)
		}
		tl.Stories = append(tl.Stories, ts)
	}

	return tl
}

// timelineEventBelongs reports whether evt is part of this requirement's
// history: either it targets one of the requirement's stories, or it is a
// requirement-level event whose payload names the requirement.
func timelineEventBelongs(evt state.Event, reqID string, stories map[string]state.Story) bool {
	if evt.StoryID != "" {
		_, ok := stories[evt.StoryID]
		return ok
	}
	p := state.DecodePayload(evt.Payload)
	for _, key := range []string{"id", "req_id"} {
		if v, ok := p[key].(string); ok && v == reqID {
			return true
		}
	}
	return false
}

// timelineLabel renders one event as a short human line. Falls back to the
// raw event type so new events are never invisible.
func timelineLabel(evt state.Event, stories map[string]state.Story) string {
	p := state.DecodePayload(evt.Payload)
	name := evt.StoryID
	if s, ok := stories[evt.StoryID]; ok && s.Title != "" {
		name = fmt.Sprintf("%s (%q)", evt.StoryID, s.Title)
	}

	switch evt.Type {
	case state.EventReqSubmitted:
		return "requirement submitted"
	case state.EventReqPlanned:
		if n, ok := p["story_count"].(float64); ok {
			return fmt.Sprintf("planned: %d stories", int(n))
		}
		return "requirement planned"
	case state.EventStoryCreated:
		return "story created: " + name
	case state.EventStoryAssigned:
		return fmt.Sprintf("assigned %s → %s", name, evt.AgentID)
	case state.EventStoryStarted:
		return "started " + name
	case state.EventStoryCompleted:
		return "agent finished " + name
	case state.EventStoryReviewPassed:
		return "review passed: " + name
	case state.EventStoryReviewFailed:
		return "review FAILED: " + name
	case state.EventStoryQAPassed:
		return "QA passed: " + name
	case state.EventStoryQAFailed:
		return "QA FAILED: " + name
	case state.EventStorySecurityPassed:
		return "security gate passed: " + name
	case state.EventStorySecurityFailed:
		return "security gate FAILED: " + name
	case state.EventStoryMerged:
		return "merged " + name
	case state.EventStoryEscalated:
		return "escalated " + name
	case state.EventReqPaused:
		if r, ok := p["reason"].(string); ok && r != "" {
			return "requirement paused: " + r
		}
		return "requirement paused"
	case state.EventReqResumed:
		return "requirement resumed"
	case state.EventReqBudgetWarning:
		return fmt.Sprintf("budget warning: $%.2f of $%.2f spent", floatFrom(p, "spent_usd"), floatFrom(p, "budget_usd"))
	case state.EventReqBudgetExceeded:
		return fmt.Sprintf("budget EXCEEDED: $%.2f of $%.2f spent", floatFrom(p, "spent_usd"), floatFrom(p, "budget_usd"))
	case state.EventHumanReviewNeeded:
		return "human review needed"
	case state.EventReqBlocked:
		return "requirement BLOCKED (mainline stayed red)"
	case state.EventReqCompleted:
		return "requirement completed"
	default:
		if evt.StoryID != "" {
			return fmt.Sprintf("%s: %s", evt.Type, name)
		}
		return string(evt.Type)
	}
}

func floatFrom(p map[string]any, key string) float64 {
	if v, ok := p[key].(float64); ok {
		return v
	}
	return 0
}

package engine

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/metrics"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// ReportStatus classifies the overall delivery outcome for a client report.
type ReportStatus string

const (
	// ReportStatusDone indicates all stories merged with no escalations or retries.
	ReportStatusDone ReportStatus = "DONE"
	// ReportStatusDoneWithConcerns indicates delivery completed but with escalations or retries.
	ReportStatusDoneWithConcerns ReportStatus = "DONE_WITH_CONCERNS"
	// ReportStatusBlocked indicates one or more stories are blocked or paused.
	ReportStatusBlocked ReportStatus = "BLOCKED"
	// ReportStatusNeedsContext indicates the requirement is still in progress.
	ReportStatusNeedsContext ReportStatus = "NEEDS_CONTEXT"
)

// ReportData is the fully assembled delivery report for a single requirement.
// It is a value type — all fields are populated by ReportBuilder.Build and
// no method mutates an existing ReportData.
type ReportData struct {
	RequirementID string
	Title         string
	Description   string
	RepoPath      string
	ReqStatus     string
	Status        ReportStatus
	GeneratedAt   time.Time

	Stories []ReportStory
	Effort  Estimate

	// Timeline is an ordered list of significant events for narrative display.
	Timeline []TimelineEntry

	// AgentStats summarises work done by each agent role.
	AgentStats []AgentStat
}

// ReportStory holds per-story delivery data for the report.
type ReportStory struct {
	ID               string
	Title            string
	Status           string
	Complexity       int
	PRUrl            string
	PRNumber         int
	Wave             int
	EscalationCount  int
	RetryCount       int
	Duration         time.Duration
	EscalationTier   int
}

// TimelineEntry is a single significant event in the delivery timeline.
type TimelineEntry struct {
	Timestamp   time.Time
	EventType   string
	StoryID     string
	Description string
}

// AgentStat summarises work performed by a given agent role.
type AgentStat struct {
	AgentID    string
	StoriesWorked int
	Escalations   int
}

// ReportBuilder assembles ReportData from the event and projection stores.
type ReportBuilder struct {
	es  state.EventStore
	ps  *state.SQLiteStore
	cfg config.Config
}

// NewReportBuilder creates a ReportBuilder wired to the given stores and config.
// ps must be *state.SQLiteStore (not the interface) because ListRequirementsFiltered
// is not on the ProjectionStore interface.
func NewReportBuilder(es state.EventStore, ps *state.SQLiteStore, cfg config.Config) *ReportBuilder {
	return &ReportBuilder{es: es, ps: ps, cfg: cfg}
}

// Build assembles a complete ReportData for the given requirement ID.
// It returns an error if the requirement is not found or if a store query fails.
func (rb *ReportBuilder) Build(reqID string) (ReportData, error) {
	req, err := rb.ps.GetRequirement(reqID)
	if err != nil {
		return ReportData{}, fmt.Errorf("get requirement %s: %w", reqID, err)
	}

	stories, err := rb.ps.ListStories(state.StoryFilter{ReqID: reqID})
	if err != nil {
		return ReportData{}, fmt.Errorf("list stories for %s: %w", reqID, err)
	}

	reportStories, err := rb.buildStories(stories)
	if err != nil {
		return ReportData{}, fmt.Errorf("build stories: %w", err)
	}

	effort := rb.buildEffort(stories)
	timeline := rb.buildTimeline(reqID, stories)
	agentStats := rb.buildAgentStats(stories)

	status := rb.classifyStatus(req, reportStories)

	return ReportData{
		RequirementID: req.ID,
		Title:         req.Title,
		Description:   req.Description,
		RepoPath:      req.RepoPath,
		ReqStatus:     req.Status,
		Status:        status,
		GeneratedAt:   time.Now().UTC(),
		Stories:       reportStories,
		Effort:        effort,
		Timeline:      timeline,
		AgentStats:    agentStats,
	}, nil
}

// buildStories converts state.Story slice into []ReportStory by querying
// event counts for escalations and retries for each story.
func (rb *ReportBuilder) buildStories(stories []state.Story) ([]ReportStory, error) {
	result := make([]ReportStory, 0, len(stories))

	for _, s := range stories {
		escalations, err := rb.es.List(state.EventFilter{
			Type:    state.EventStoryEscalated,
			StoryID: s.ID,
		})
		if err != nil {
			return nil, fmt.Errorf("list escalations for story %s: %w", s.ID, err)
		}

		retries, err := rb.countRetries(s.ID)
		if err != nil {
			return nil, fmt.Errorf("count retries for story %s: %w", s.ID, err)
		}

		duration := rb.storyDuration(s)

		result = append(result, ReportStory{
			ID:              s.ID,
			Title:           s.Title,
			Status:          s.Status,
			Complexity:      s.Complexity,
			PRUrl:           s.PRUrl,
			PRNumber:        s.PRNumber,
			Wave:            s.Wave,
			EscalationCount: len(escalations),
			RetryCount:      retries,
			Duration:        duration,
			EscalationTier:  s.EscalationTier,
		})
	}

	return result, nil
}

// countRetries returns the total number of review and QA failures for a story,
// which represent retry attempts.
func (rb *ReportBuilder) countRetries(storyID string) (int, error) {
	reviewFails, err := rb.es.List(state.EventFilter{
		Type:    state.EventStoryReviewFailed,
		StoryID: storyID,
	})
	if err != nil {
		return 0, err
	}

	qaFails, err := rb.es.List(state.EventFilter{
		Type:    state.EventStoryQAFailed,
		StoryID: storyID,
	})
	if err != nil {
		return 0, err
	}

	return len(reviewFails) + len(qaFails), nil
}

// storyDuration computes elapsed time from story creation to merge.
// Returns 0 if the story is not yet merged or timestamps are unavailable.
func (rb *ReportBuilder) storyDuration(s state.Story) time.Duration {
	if s.MergedAt.IsZero() || s.CreatedAt.IsZero() {
		return 0
	}
	d := s.MergedAt.Sub(s.CreatedAt)
	if d < 0 {
		return 0
	}
	return d
}

// buildEffort maps the stories to StoryEstimate values and calls CalculateCostWithTokens
// using actual token usage from the metrics store when available.
func (rb *ReportBuilder) buildEffort(stories []state.Story) Estimate {
	estimates := make([]StoryEstimate, 0, len(stories))
	for _, s := range stories {
		estimates = append(estimates, StoryEstimate{
			Title:      s.Title,
			Complexity: s.Complexity,
			Role:       s.AgentID,
		})
	}

	// Sum actual token usage from the metrics store.
	inputTokens, outputTokens := rb.sumTokenUsage()
	return CalculateCostWithTokens(estimates, rb.cfg.Billing, 0, inputTokens, outputTokens)
}

// sumTokenUsage reads the metrics.jsonl file and sums all token counts.
// Returns (0, 0) if the file doesn't exist or can't be read.
func (rb *ReportBuilder) sumTokenUsage() (inputTokens, outputTokens int) {
	stateDir := rb.cfg.Workspace.StateDir
	if stateDir == "" {
		return 0, 0
	}
	// Expand ~ manually since report may run without CLI helpers.
	metricsPath := filepath.Join(stateDir, "metrics.jsonl")
	recorder := metrics.NewRecorder(metricsPath)
	entries, err := recorder.ReadAll()
	if err != nil || len(entries) == 0 {
		return 0, 0
	}
	for _, e := range entries {
		inputTokens += e.TokensIn
		outputTokens += e.TokensOut
	}
	return inputTokens, outputTokens
}

// buildTimeline builds an ordered list of significant delivery events.
// It includes requirement lifecycle events and key story events.
func (rb *ReportBuilder) buildTimeline(reqID string, stories []state.Story) []TimelineEntry {
	var entries []TimelineEntry

	// Collect relevant event types for the requirement level
	reqEventTypes := []state.EventType{
		state.EventReqSubmitted,
		state.EventReqPlanned,
		state.EventReqCompleted,
		state.EventReqPaused,
	}
	for _, evtType := range reqEventTypes {
		evts, _ := rb.es.List(state.EventFilter{Type: evtType})
		for _, evt := range evts {
			payload := state.DecodePayload(evt.Payload)
			if id, _ := payload["id"].(string); id == reqID {
				entries = append(entries, TimelineEntry{
					Timestamp:   evt.Timestamp,
					EventType:   string(evt.Type),
					Description: rb.describeReqEvent(evt.Type),
				})
			}
		}
	}

	// Collect significant story events
	storyEventTypes := []state.EventType{
		state.EventStoryMerged,
		state.EventStoryEscalated,
		state.EventStoryPRCreated,
		state.EventStoryReviewFailed,
		state.EventStoryQAFailed,
	}
	storyIDSet := make(map[string]string, len(stories))
	for _, s := range stories {
		storyIDSet[s.ID] = s.Title
	}

	for _, evtType := range storyEventTypes {
		for storyID, storyTitle := range storyIDSet {
			evts, _ := rb.es.List(state.EventFilter{
				Type:    evtType,
				StoryID: storyID,
			})
			for _, evt := range evts {
				entries = append(entries, TimelineEntry{
					Timestamp:   evt.Timestamp,
					EventType:   string(evt.Type),
					StoryID:     storyID,
					Description: rb.describeStoryEvent(evt.Type, storyTitle),
				})
			}
		}
	}

	// Sort by timestamp ascending
	sortTimelineEntries(entries)

	return entries
}

// buildAgentStats aggregates per-agent work across all stories.
func (rb *ReportBuilder) buildAgentStats(stories []state.Story) []AgentStat {
	statsMap := make(map[string]*AgentStat)

	for _, s := range stories {
		if s.AgentID == "" {
			continue
		}
		stat, ok := statsMap[s.AgentID]
		if !ok {
			stat = &AgentStat{AgentID: s.AgentID}
			statsMap[s.AgentID] = stat
		}
		stat.StoriesWorked++

		escalations, _ := rb.es.List(state.EventFilter{
			Type:    state.EventStoryEscalated,
			StoryID: s.ID,
		})
		stat.Escalations += len(escalations)
	}

	result := make([]AgentStat, 0, len(statsMap))
	for _, stat := range statsMap {
		result = append(result, *stat)
	}
	return result
}

// classifyStatus determines the overall delivery status for the report.
// Logic:
//   - BLOCKED if any story is paused or blocked
//   - DONE if req is completed and no escalations/retries across any story
//   - DONE_WITH_CONCERNS if req is completed but stories had escalations or retries
//   - NEEDS_CONTEXT otherwise (in-progress or unknown)
func (rb *ReportBuilder) classifyStatus(req state.Requirement, stories []ReportStory) ReportStatus {
	for _, s := range stories {
		if s.Status == "paused" || s.Status == "blocked" {
			return ReportStatusBlocked
		}
	}

	if req.Status == "completed" {
		for _, s := range stories {
			if s.EscalationCount > 0 || s.RetryCount > 0 {
				return ReportStatusDoneWithConcerns
			}
		}
		return ReportStatusDone
	}

	return ReportStatusNeedsContext
}

// describeReqEvent returns a human-readable description for a requirement event.
func (rb *ReportBuilder) describeReqEvent(evtType state.EventType) string {
	switch evtType {
	case state.EventReqSubmitted:
		return "Requirement submitted"
	case state.EventReqPlanned:
		return "Stories planned"
	case state.EventReqCompleted:
		return "Requirement completed"
	case state.EventReqPaused:
		return "Requirement paused"
	default:
		return string(evtType)
	}
}

// describeStoryEvent returns a human-readable description for a story event.
func (rb *ReportBuilder) describeStoryEvent(evtType state.EventType, storyTitle string) string {
	switch evtType {
	case state.EventStoryMerged:
		return fmt.Sprintf("Story merged: %s", storyTitle)
	case state.EventStoryEscalated:
		return fmt.Sprintf("Story escalated: %s", storyTitle)
	case state.EventStoryPRCreated:
		return fmt.Sprintf("PR created: %s", storyTitle)
	case state.EventStoryReviewFailed:
		return fmt.Sprintf("Review failed: %s", storyTitle)
	case state.EventStoryQAFailed:
		return fmt.Sprintf("QA failed: %s", storyTitle)
	default:
		return fmt.Sprintf("%s: %s", evtType, storyTitle)
	}
}

// sortTimelineEntries sorts entries in-place by timestamp ascending.
// Uses a simple insertion sort — timeline lengths are small (< 100 entries).
func sortTimelineEntries(entries []TimelineEntry) {
	for i := 1; i < len(entries); i++ {
		key := entries[i]
		j := i - 1
		for j >= 0 && entries[j].Timestamp.After(key.Timestamp) {
			entries[j+1] = entries[j]
			j--
		}
		entries[j+1] = key
	}
}

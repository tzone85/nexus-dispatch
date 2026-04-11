// internal/web/data.go
package web

import (
	"encoding/json"

	"github.com/tzone85/nexus-dispatch/internal/graph"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

type StateSnapshot struct {
	Agents          []state.Agent       `json:"agents"`
	Stories         []state.Story       `json:"stories"`
	Pipeline        PipelineCounts      `json:"pipeline"`
	Events          []EventSummary      `json:"events"`
	Escalations     []state.Escalation  `json:"escalations"`
	Requirements    []state.Requirement `json:"requirements"`
	Metrics         *MetricsSummary     `json:"metrics"`
	MemPalaceStatus *MemPalaceStatus    `json:"mempalace_status"`
	ReviewGates     []ReviewGateItem    `json:"review_gates"`
	Investigations  []InvestigationItem `json:"investigations"`
	RecoveryLog     []RecoveryItem      `json:"recovery_log"`
	DAG             *graph.DAGExport    `json:"dag,omitempty"`
}

type PipelineCounts struct {
	Planned    int `json:"planned"`
	InProgress int `json:"in_progress"`
	Review     int `json:"review"`
	QA         int `json:"qa"`
	PR         int `json:"pr"`
	Merged     int `json:"merged"`
	Split      int `json:"split"`
}

type EventSummary struct {
	Type      string         `json:"type"`
	Timestamp string         `json:"timestamp"`
	AgentID   string         `json:"agent_id"`
	StoryID   string         `json:"story_id"`
	Payload   map[string]any `json:"payload,omitempty"`
}

func (s *Server) BuildSnapshot() (StateSnapshot, error) {
	snap := StateSnapshot{}

	reqs, err := s.projStore.ListRequirementsFiltered(s.reqFilter)
	if err != nil {
		return snap, err
	}
	snap.Requirements = reqs

	// Collect stories for visible requirements
	for _, req := range reqs {
		stories, err := s.projStore.ListStories(state.StoryFilter{ReqID: req.ID})
		if err != nil {
			continue
		}
		snap.Stories = append(snap.Stories, stories...)
	}

	// Pipeline counts
	for _, story := range snap.Stories {
		switch mapStatusToBucket(story.Status) {
		case "planned":
			snap.Pipeline.Planned++
		case "in_progress":
			snap.Pipeline.InProgress++
		case "review":
			snap.Pipeline.Review++
		case "qa":
			snap.Pipeline.QA++
		case "pr_submitted":
			snap.Pipeline.PR++
		case "merged":
			snap.Pipeline.Merged++
		case "split":
			snap.Pipeline.Split++
		}
	}

	snap.Agents, _ = s.projStore.ListAgents(state.AgentFilter{})
	snap.Escalations, _ = s.projStore.ListEscalations()

	// Last 50 events
	events, _ := s.eventStore.List(state.EventFilter{Limit: 50})
	for _, evt := range events {
		snap.Events = append(snap.Events, EventSummary{
			Type:      string(evt.Type),
			Timestamp: evt.Timestamp.Format("15:04:05"),
			AgentID:   evt.AgentID,
			StoryID:   evt.StoryID,
		})
	}

	// Metrics from cache
	if s.metricsCache != nil {
		snap.Metrics = s.metricsCache.Get()
	}

	// MemPalace status
	snap.MemPalaceStatus = MemPalaceCheck(s.mempalace)

	// Review gates: stories at merge_ready + requirements pending review
	gates := []ReviewGateItem{}
	for _, story := range snap.Stories {
		if story.Status == "merge_ready" {
			gates = append(gates, ReviewGateItem{
				ID:     story.ID,
				Type:   "story",
				Title:  story.Title,
				Status: story.Status,
			})
		}
	}
	for _, req := range snap.Requirements {
		if req.Status == "pending_review" {
			gates = append(gates, ReviewGateItem{
				ID:     req.ID,
				Type:   "requirement",
				Title:  req.Title,
				Status: req.Status,
			})
		}
	}
	snap.ReviewGates = gates

	// Recovery log from STORY_RECOVERY events
	recoveryEvents, _ := s.eventStore.List(state.EventFilter{
		Type:  state.EventStoryRecovery,
		Limit: 50,
	})
	recoveries := []RecoveryItem{}
	for _, evt := range recoveryEvents {
		payload := state.DecodePayload(evt.Payload)
		recType, _ := payload["type"].(string)
		desc, _ := payload["description"].(string)
		recoveries = append(recoveries, RecoveryItem{
			StoryID:     evt.StoryID,
			Type:        recType,
			Description: desc,
			Timestamp:   evt.Timestamp.Format("15:04:05"),
		})
	}
	snap.RecoveryLog = recoveries

	// Investigations from INVESTIGATION_COMPLETED events
	invEvents, _ := s.eventStore.List(state.EventFilter{
		Type:  state.EventInvestigationCompleted,
		Limit: 50,
	})
	investigations := []InvestigationItem{}
	for _, evt := range invEvents {
		payload := state.DecodePayload(evt.Payload)
		summary, _ := payload["summary"].(string)
		reqID, _ := payload["req_id"].(string)
		moduleCount := intFromPayload(payload, "module_count")
		smellCount := intFromPayload(payload, "smell_count")
		riskCount := intFromPayload(payload, "risk_count")
		investigations = append(investigations, InvestigationItem{
			ReqID:       reqID,
			Summary:     summary,
			ModuleCount: moduleCount,
			SmellCount:  smellCount,
			RiskCount:   riskCount,
		})
	}
	snap.Investigations = investigations

	// Include DAG export if available.
	snap.DAG = s.dagExport

	return snap, nil
}

// intFromPayload extracts an integer from a decoded JSON payload map.
// JSON numbers decode as float64; this safely converts them.
func intFromPayload(m map[string]any, key string) int {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	default:
		return 0
	}
}

func (s *Server) SnapshotJSON() ([]byte, error) {
	snap, err := s.BuildSnapshot()
	if err != nil {
		return nil, err
	}
	return json.Marshal(snap)
}

// mapStatusToBucket maps story statuses to pipeline buckets.
// This duplicates dashboard.mapStatusToBucket — both packages need it
// and they live in different packages.
func mapStatusToBucket(status string) string {
	switch status {
	case "draft", "estimated", "planned", "assigned":
		return "planned"
	case "in_progress":
		return "in_progress"
	case "review":
		return "review"
	case "qa", "qa_started", "qa_failed":
		return "qa"
	case "pr_submitted":
		return "pr_submitted"
	case "merged":
		return "merged"
	case "split":
		return "split"
	default:
		return "planned"
	}
}

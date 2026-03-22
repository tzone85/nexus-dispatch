// internal/web/handlers.go
package web

import (
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

var agentIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

const maxEscalationTier = 3

// HandleCommand dispatches a WebSocket command to the appropriate handler.
func (s *Server) HandleCommand(action string, payload json.RawMessage) WSResponse {
	switch action {
	case "pause_requirement":
		return s.handlePause(payload)
	case "resume_requirement":
		return s.handleResume(payload)
	case "retry_story":
		return s.handleRetry(payload)
	case "reassign_story":
		return s.handleReassign(payload)
	case "escalate_story":
		return s.handleEscalate(payload)
	case "kill_agent":
		return s.handleKill(payload)
	case "edit_story":
		return s.handleEdit(payload)
	default:
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "unknown command"}
	}
}

// --- payload types ---

type reqPayload struct {
	ReqID string `json:"req_id"`
}

type storyPayload struct {
	StoryID    string `json:"story_id"`
	TargetTier int    `json:"target_tier,omitempty"`
}

type agentPayload struct {
	AgentID string `json:"agent_id"`
}

type editPayload struct {
	StoryID            string `json:"story_id"`
	Title              string `json:"title,omitempty"`
	Description        string `json:"description,omitempty"`
	AcceptanceCriteria string `json:"acceptance_criteria,omitempty"`
	Complexity         int    `json:"complexity,omitempty"`
}

// --- handlers ---

func (s *Server) handlePause(payload json.RawMessage) WSResponse {
	const action = "pause_requirement"

	var p reqPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.ReqID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid req_id"}
	}

	req, err := s.findRequirement(p.ReqID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if req == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "requirement not found"}
	}
	if req.Status == "paused" {
		return WSResponse{Type: "command_result", Action: action, Success: true, Message: "already paused"}
	}

	evt := state.NewEvent(state.EventReqPaused, "dashboard", "", map[string]any{
		"id":     p.ReqID,
		"source": "dashboard",
	})
	if err := s.eventStore.Append(evt); err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: fmt.Sprintf("event error: %v", err)}
	}
	s.projStore.Project(evt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: "Requirement paused"}
}

func (s *Server) handleResume(payload json.RawMessage) WSResponse {
	const action = "resume_requirement"

	var p reqPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.ReqID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid req_id"}
	}

	req, err := s.findRequirement(p.ReqID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if req == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "requirement not found"}
	}
	if req.Status != "paused" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "requirement is not paused"}
	}

	evt := state.NewEvent(state.EventReqResumed, "dashboard", "", map[string]any{
		"id":     p.ReqID,
		"source": "dashboard",
	})
	if err := s.eventStore.Append(evt); err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: fmt.Sprintf("event error: %v", err)}
	}
	s.projStore.Project(evt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: "Requirement resumed"}
}

func (s *Server) handleRetry(payload json.RawMessage) WSResponse {
	const action = "retry_story"

	var p storyPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.StoryID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid story_id"}
	}

	story, err := s.findStory(p.StoryID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if story == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "story not found"}
	}

	escEvt := state.NewEvent(state.EventStoryEscalated, "dashboard", p.StoryID, map[string]any{
		"from_tier": story.EscalationTier,
		"to_tier":   0,
		"reason":    "manual retry from dashboard",
		"source":    "dashboard",
	})
	s.eventStore.Append(escEvt) //nolint:errcheck
	s.projStore.Project(escEvt) //nolint:errcheck

	resetEvt := state.NewEvent(state.EventStoryReviewFailed, "dashboard", p.StoryID, map[string]any{
		"source": "dashboard",
	})
	s.eventStore.Append(resetEvt) //nolint:errcheck
	s.projStore.Project(resetEvt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: "Story retried at tier 0"}
}

func (s *Server) handleReassign(payload json.RawMessage) WSResponse {
	const action = "reassign_story"

	var p storyPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.StoryID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid story_id"}
	}
	if p.TargetTier < 0 || p.TargetTier > maxEscalationTier {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: fmt.Sprintf("target_tier must be 0-%d", maxEscalationTier)}
	}

	story, err := s.findStory(p.StoryID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if story == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "story not found"}
	}

	escEvt := state.NewEvent(state.EventStoryEscalated, "dashboard", p.StoryID, map[string]any{
		"from_tier": story.EscalationTier,
		"to_tier":   p.TargetTier,
		"reason":    "manual reassign from dashboard",
		"source":    "dashboard",
	})
	s.eventStore.Append(escEvt) //nolint:errcheck
	s.projStore.Project(escEvt) //nolint:errcheck

	resetEvt := state.NewEvent(state.EventStoryReviewFailed, "dashboard", p.StoryID, map[string]any{
		"source": "dashboard",
	})
	s.eventStore.Append(resetEvt) //nolint:errcheck
	s.projStore.Project(resetEvt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: fmt.Sprintf("Story reassigned to tier %d", p.TargetTier)}
}

func (s *Server) handleEscalate(payload json.RawMessage) WSResponse {
	const action = "escalate_story"

	var p storyPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.StoryID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid story_id"}
	}

	story, err := s.findStory(p.StoryID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if story == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "story not found"}
	}

	nextTier := story.EscalationTier + 1
	if nextTier > maxEscalationTier {
		nextTier = maxEscalationTier
	}

	escEvt := state.NewEvent(state.EventStoryEscalated, "dashboard", p.StoryID, map[string]any{
		"from_tier": story.EscalationTier,
		"to_tier":   nextTier,
		"reason":    "manual escalation from dashboard",
		"source":    "dashboard",
	})
	if err := s.eventStore.Append(escEvt); err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: fmt.Sprintf("event error: %v", err)}
	}
	s.projStore.Project(escEvt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: fmt.Sprintf("Story escalated to tier %d", nextTier)}
}

func (s *Server) handleKill(payload json.RawMessage) WSResponse {
	const action = "kill_agent"

	var p agentPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.AgentID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid agent_id"}
	}

	if !agentIDPattern.MatchString(p.AgentID) {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid agent_id format"}
	}

	agents, err := s.projStore.ListAgents(state.AgentFilter{})
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}

	var sessionName string
	for _, a := range agents {
		if a.ID == p.AgentID {
			sessionName = a.SessionName
			break
		}
	}
	if sessionName == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "agent not found or no session"}
	}

	cmd := exec.Command("tmux", "kill-session", "-t", sessionName)
	if err := cmd.Run(); err != nil {
		log.Printf("[cmd] kill-session %s: %v", sessionName, err)
		// Don't fail — session may already be dead.
	}

	evt := state.NewEvent(state.EventAgentTerminated, p.AgentID, "", map[string]any{
		"reason": "killed from dashboard",
		"source": "dashboard",
	})
	s.eventStore.Append(evt) //nolint:errcheck
	s.projStore.Project(evt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: fmt.Sprintf("Agent %s killed", p.AgentID)}
}

func (s *Server) handleEdit(payload json.RawMessage) WSResponse {
	const action = "edit_story"

	var p editPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.StoryID == "" {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "invalid story_id"}
	}

	story, err := s.findStory(p.StoryID)
	if err != nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "store error"}
	}
	if story == nil {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "story not found"}
	}

	changes := make(map[string]any)
	if p.Title != "" {
		changes["title"] = p.Title
	}
	if p.Description != "" {
		changes["description"] = p.Description
	}
	if p.AcceptanceCriteria != "" {
		changes["acceptance_criteria"] = p.AcceptanceCriteria
	}
	if p.Complexity > 0 {
		changes["complexity"] = p.Complexity
	}

	if len(changes) == 0 {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: "no changes provided"}
	}

	// projectStoryRewritten expects payload.changes to be a nested map.
	evt := state.NewEvent(state.EventStoryRewritten, "dashboard", p.StoryID, map[string]any{
		"changes": changes,
		"source":  "dashboard",
	})
	s.eventStore.Append(evt) //nolint:errcheck
	s.projStore.Project(evt) //nolint:errcheck

	return WSResponse{Type: "command_result", Action: action, Success: true, Message: "Story updated and reset to draft"}
}

// --- lookup helpers ---

// findRequirement returns a pointer to the matching Requirement, or nil if not found.
// Returns (nil, error) on store failure.
func (s *Server) findRequirement(reqID string) (*state.Requirement, error) {
	reqs, err := s.projStore.ListRequirementsFiltered(state.ReqFilter{})
	if err != nil {
		return nil, err
	}
	for i := range reqs {
		if reqs[i].ID == reqID {
			return &reqs[i], nil
		}
	}
	return nil, nil
}

// findStory returns a pointer to the matching Story, or nil if not found.
// Returns (nil, error) on store failure.
func (s *Server) findStory(storyID string) (*state.Story, error) {
	stories, err := s.projStore.ListStories(state.StoryFilter{})
	if err != nil {
		return nil, err
	}
	for i := range stories {
		if stories[i].ID == storyID {
			return &stories[i], nil
		}
	}
	return nil, nil
}

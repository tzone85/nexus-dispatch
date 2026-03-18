package state

import "time"

// Requirement represents a high-level user requirement that gets broken into stories.
type Requirement struct {
	ID          string
	Title       string
	Description string
	Status      string
	RepoPath    string
	CreatedAt   time.Time
}

// ReqFilter specifies criteria for filtering requirements from the projection store.
type ReqFilter struct {
	RepoPath        string
	ExcludeArchived bool
}

// Story represents a single unit of work derived from a requirement.
type Story struct {
	ID                 string
	ReqID              string
	Title              string
	Description        string
	AcceptanceCriteria string
	Complexity         int
	Status             string
	AgentID            string
	Branch             string
	PRUrl              string
	OwnedFiles         []string
	WaveHint           string
	CreatedAt          time.Time
}

// Agent represents an AI agent that can work on stories.
type Agent struct {
	ID             string
	Type           string
	Model          string
	Runtime        string
	Status         string
	CurrentStoryID string
	SessionName    string
	CreatedAt      time.Time
}

// StoryFilter specifies criteria for filtering stories from the projection store.
type StoryFilter struct {
	Status string
	ReqID  string
}

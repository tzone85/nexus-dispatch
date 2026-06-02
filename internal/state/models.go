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
	PRNumber           int
	OwnedFiles         []string
	WaveHint           string
	Wave               int
	EscalationTier     int
	SplitDepth         int
	CreatedAt          time.Time
	MergedAt           time.Time
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

// StoryDatabase represents one row in the story_databases projection — the
// per-story devdb lifecycle status surfaced to dashboards and CLIs.
type StoryDatabase struct {
	StoryID         string
	DBID            string
	DBName          string
	Provider        string
	Status          string // created | failed | deleted
	Template        string
	Error           string
	CreatedAt       time.Time
	DeletedAt       time.Time
	DurationSeconds float64
	BytesUsed       int64
}

// StoryDBFilter specifies criteria for filtering story_databases rows.
type StoryDBFilter struct {
	StoryID string
	Status  string
}

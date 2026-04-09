package state

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/oklog/ulid/v2"
)

// initSQL is the schema migration applied on store creation.
// This mirrors migrations/001_init.sql kept in the repository root for
// external tooling (e.g. CLI migration commands).
const initSQL = `
CREATE TABLE IF NOT EXISTS requirements (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    description TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS stories (
    id TEXT PRIMARY KEY,
    req_id TEXT NOT NULL,
    title TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    acceptance_criteria TEXT NOT NULL DEFAULT '',
    complexity INTEGER NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'draft',
    agent_id TEXT NOT NULL DEFAULT '',
    branch TEXT NOT NULL DEFAULT '',
    pr_url TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agents (
    id TEXT PRIMARY KEY,
    type TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    runtime TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'idle',
    current_story_id TEXT NOT NULL DEFAULT '',
    session_name TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS story_deps (
    story_id TEXT NOT NULL,
    depends_on_id TEXT NOT NULL,
    PRIMARY KEY (story_id, depends_on_id)
);

CREATE TABLE IF NOT EXISTS escalations (
    id TEXT PRIMARY KEY,
    story_id TEXT NOT NULL DEFAULT '',
    from_agent TEXT NOT NULL,
    reason TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    resolution TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP
);

CREATE TABLE IF NOT EXISTS agent_scores (
    id TEXT PRIMARY KEY,
    agent_id TEXT NOT NULL,
    story_id TEXT NOT NULL,
    quality INTEGER NOT NULL DEFAULT 0,
    reliability INTEGER NOT NULL DEFAULT 0,
    duration_s INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

// SQLiteStore implements ProjectionStore using SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore opens a SQLite database and applies the schema migration.
func NewSQLiteStore(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	if _, err := db.Exec(initSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply migration: %w", err)
	}

	// Migrate existing databases: add acceptance_criteria column if missing.
	db.Exec(`ALTER TABLE stories ADD COLUMN acceptance_criteria TEXT NOT NULL DEFAULT ''`)

	// Migrate existing databases: add owned_files and wave_hint columns if missing.
	db.Exec(`ALTER TABLE stories ADD COLUMN owned_files TEXT NOT NULL DEFAULT '[]'`)
	db.Exec(`ALTER TABLE stories ADD COLUMN wave_hint TEXT NOT NULL DEFAULT 'parallel'`)

	// Migrate existing databases: add repo_path column to requirements if missing.
	db.Exec(`ALTER TABLE requirements ADD COLUMN repo_path TEXT NOT NULL DEFAULT ''`)

	// Migrate existing databases: add wave and pr_number columns if missing.
	db.Exec(`ALTER TABLE stories ADD COLUMN wave INTEGER NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE stories ADD COLUMN pr_number INTEGER NOT NULL DEFAULT 0`)

	// Migrate existing databases: add escalation columns if missing.
	escalationMigrations := []string{
		"ALTER TABLE stories ADD COLUMN escalation_tier INTEGER DEFAULT 0",
		"ALTER TABLE stories ADD COLUMN split_depth INTEGER DEFAULT 0",
		"ALTER TABLE escalations ADD COLUMN from_tier INTEGER DEFAULT 0",
		"ALTER TABLE escalations ADD COLUMN to_tier INTEGER DEFAULT 0",
	}
	for _, m := range escalationMigrations {
		db.Exec(m) // errors ignored for idempotency
	}

	// Migrate: add classification columns to requirements
	db.Exec(`ALTER TABLE requirements ADD COLUMN req_type TEXT NOT NULL DEFAULT ''`)
	db.Exec(`ALTER TABLE requirements ADD COLUMN is_existing BOOLEAN NOT NULL DEFAULT 0`)
	db.Exec(`ALTER TABLE requirements ADD COLUMN investigation_report_json TEXT NOT NULL DEFAULT ''`)

	return &SQLiteStore{db: db}, nil
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Project applies a domain event to the projection tables, updating the
// materialized state accordingly.
func (s *SQLiteStore) Project(evt Event) error {
	payload := s.decodePayload(evt)

	switch evt.Type {
	case EventReqSubmitted:
		return s.projectReqSubmitted(payload)
	case EventReqAnalyzed:
		return s.updateReqStatus(payload, "analyzed")
	case EventReqPlanned:
		return s.updateReqStatus(payload, "planned")
	case EventReqPaused:
		return s.updateReqStatus(payload, "paused")
	case EventReqResumed:
		return s.updateReqStatus(payload, "planned")
	case EventReqCompleted:
		return s.updateReqStatus(payload, "completed")

	case EventReqClassified:
		reqID, _ := payload["req_id"].(string)
		reqType, _ := payload["req_type"].(string)
		isExisting, _ := payload["is_existing"].(bool)
		isExistingInt := 0
		if isExisting {
			isExistingInt = 1
		}
		_, err := s.db.Exec(`UPDATE requirements SET req_type = ?, is_existing = ? WHERE id = ?`, reqType, isExistingInt, reqID)
		return err

	case EventInvestigationCompleted:
		reqID, _ := payload["req_id"].(string)
		reportJSON, _ := payload["report"].(string)
		_, err := s.db.Exec(`UPDATE requirements SET investigation_report_json = ? WHERE id = ?`, reportJSON, reqID)
		return err

	case EventStoryCreated:
		return s.projectStoryCreated(payload)
	case EventStoryEstimated:
		return s.updateStoryStatus(evt.StoryID, "estimated")
	case EventStoryAssigned:
		return s.projectStoryAssigned(evt.StoryID, payload)
	case EventStoryStarted:
		return s.updateStoryStatus(evt.StoryID, "in_progress")
	case EventStoryProgress:
		return nil // progress events are informational only
	case EventStoryCompleted:
		return s.updateStoryStatus(evt.StoryID, "review")
	case EventStoryReviewRequested:
		return s.updateStoryStatus(evt.StoryID, "review")
	case EventStoryReviewPassed:
		return s.updateStoryStatus(evt.StoryID, "qa")
	case EventStoryReviewFailed:
		return s.updateStoryStatus(evt.StoryID, "draft")
	case EventStoryQAStarted:
		return s.updateStoryStatus(evt.StoryID, "qa")
	case EventStoryQAPassed:
		return s.updateStoryStatus(evt.StoryID, "pr_submitted")
	case EventStoryPRCreated:
		return s.projectStoryPRCreated(evt.StoryID, payload)
	case EventStoryMerged:
		return s.updateStoryStatus(evt.StoryID, "merged")

	case EventStoryEscalated:
		return s.projectStoryEscalated(evt, payload)
	case EventStoryRewritten:
		return s.projectStoryRewritten(evt.StoryID, payload)
	case EventStorySplit:
		return s.updateStoryStatus(evt.StoryID, "split")

	default:
		// Unhandled event types are silently ignored to allow forward
		// compatibility as new event types are added.
		return nil
	}
}

// GetRequirement returns a single requirement by ID.
func (s *SQLiteStore) GetRequirement(id string) (Requirement, error) {
	var req Requirement
	err := s.db.QueryRow(
		`SELECT id, title, description, status, repo_path, created_at FROM requirements WHERE id = ?`,
		id,
	).Scan(&req.ID, &req.Title, &req.Description, &req.Status, &req.RepoPath, &req.CreatedAt)
	if err != nil {
		return Requirement{}, fmt.Errorf("get requirement %s: %w", id, err)
	}
	return req, nil
}

// GetStory returns a single story by ID.
func (s *SQLiteStore) GetStory(id string) (Story, error) {
	var story Story
	var ownedFilesJSON string
	err := s.db.QueryRow(
		`SELECT id, req_id, title, description, acceptance_criteria, complexity, status, agent_id, branch, pr_url, pr_number, owned_files, wave_hint, wave, escalation_tier, split_depth, created_at
		 FROM stories WHERE id = ?`,
		id,
	).Scan(
		&story.ID, &story.ReqID, &story.Title, &story.Description,
		&story.AcceptanceCriteria, &story.Complexity, &story.Status, &story.AgentID, &story.Branch,
		&story.PRUrl, &story.PRNumber, &ownedFilesJSON, &story.WaveHint, &story.Wave,
		&story.EscalationTier, &story.SplitDepth, &story.CreatedAt,
	)
	if err != nil {
		return Story{}, fmt.Errorf("get story %s: %w", id, err)
	}
	if ownedFilesJSON != "" {
		json.Unmarshal([]byte(ownedFilesJSON), &story.OwnedFiles)
	}
	if story.OwnedFiles == nil {
		story.OwnedFiles = []string{}
	}
	return story, nil
}

// ListStories returns stories matching the given filter.
func (s *SQLiteStore) ListStories(filter StoryFilter) ([]Story, error) {
	query := `SELECT id, req_id, title, description, acceptance_criteria, complexity, status, agent_id, branch, pr_url, pr_number, owned_files, wave_hint, wave, escalation_tier, split_depth, created_at FROM stories`
	var conditions []string
	var args []any

	if filter.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.ReqID != "" {
		conditions = append(conditions, "req_id = ?")
		args = append(args, filter.ReqID)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list stories: %w", err)
	}
	defer rows.Close()

	var stories []Story
	for rows.Next() {
		var story Story
		var ownedFilesJSON string
		if err := rows.Scan(
			&story.ID, &story.ReqID, &story.Title, &story.Description,
			&story.AcceptanceCriteria, &story.Complexity, &story.Status, &story.AgentID, &story.Branch,
			&story.PRUrl, &story.PRNumber, &ownedFilesJSON, &story.WaveHint, &story.Wave,
			&story.EscalationTier, &story.SplitDepth, &story.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan story: %w", err)
		}
		if ownedFilesJSON != "" {
			json.Unmarshal([]byte(ownedFilesJSON), &story.OwnedFiles)
		}
		if story.OwnedFiles == nil {
			story.OwnedFiles = []string{}
		}
		stories = append(stories, story)
	}
	return stories, rows.Err()
}

// ListRequirements returns all requirements ordered by creation time.
func (s *SQLiteStore) ListRequirements() ([]Requirement, error) {
	return s.ListRequirementsFiltered(ReqFilter{})
}

// ListRequirementsFiltered returns requirements matching the given filter,
// ordered by creation time.
func (s *SQLiteStore) ListRequirementsFiltered(filter ReqFilter) ([]Requirement, error) {
	query := `SELECT id, title, description, status, repo_path, created_at FROM requirements`
	var conditions []string
	var args []any

	if filter.RepoPath != "" {
		conditions = append(conditions, "repo_path = ?")
		args = append(args, filter.RepoPath)
	}
	if filter.ExcludeArchived {
		conditions = append(conditions, "status != 'archived'")
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ")
	}
	query += " ORDER BY created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list requirements: %w", err)
	}
	defer rows.Close()

	var reqs []Requirement
	for rows.Next() {
		var req Requirement
		if err := rows.Scan(&req.ID, &req.Title, &req.Description, &req.Status, &req.RepoPath, &req.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan requirement: %w", err)
		}
		reqs = append(reqs, req)
	}
	return reqs, rows.Err()
}

// AgentFilter specifies criteria for filtering agents.
type AgentFilter struct {
	Status string
}

// ListAgents returns agents matching the given filter, ordered by creation time.
func (s *SQLiteStore) ListAgents(filter AgentFilter) ([]Agent, error) {
	query := `SELECT id, type, model, runtime, status, current_story_id, session_name, created_at FROM agents`
	var args []any

	if filter.Status != "" {
		query += " WHERE status = ?"
		args = append(args, filter.Status)
	}
	query += " ORDER BY created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list agents: %w", err)
	}
	defer rows.Close()

	var agents []Agent
	for rows.Next() {
		var a Agent
		if err := rows.Scan(
			&a.ID, &a.Type, &a.Model, &a.Runtime,
			&a.Status, &a.CurrentStoryID, &a.SessionName, &a.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan agent: %w", err)
		}
		agents = append(agents, a)
	}
	return agents, rows.Err()
}

// Escalation represents a recorded escalation between agent roles.
type Escalation struct {
	ID         string
	StoryID    string
	FromAgent  string
	Reason     string
	Status     string
	Resolution string
	FromTier   int
	ToTier     int
	CreatedAt  string
}

// ListEscalations returns all escalations ordered by creation time descending.
func (s *SQLiteStore) ListEscalations() ([]Escalation, error) {
	rows, err := s.db.Query(
		`SELECT id, story_id, from_agent, reason, status, resolution, from_tier, to_tier, created_at
		 FROM escalations ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list escalations: %w", err)
	}
	defer rows.Close()

	var escalations []Escalation
	for rows.Next() {
		var e Escalation
		if err := rows.Scan(
			&e.ID, &e.StoryID, &e.FromAgent, &e.Reason,
			&e.Status, &e.Resolution, &e.FromTier, &e.ToTier, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan escalation: %w", err)
		}
		escalations = append(escalations, e)
	}
	return escalations, rows.Err()
}

// StoryDep represents a dependency edge between stories.
type StoryDep struct {
	StoryID     string
	DependsOnID string
}

// ListStoryDeps returns all dependency edges for stories belonging to the given requirement.
func (s *SQLiteStore) ListStoryDeps(reqID string) ([]StoryDep, error) {
	rows, err := s.db.Query(
		`SELECT sd.story_id, sd.depends_on_id
		 FROM story_deps sd
		 JOIN stories s ON sd.story_id = s.id
		 WHERE s.req_id = ?`, reqID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var deps []StoryDep
	for rows.Next() {
		var d StoryDep
		if err := rows.Scan(&d.StoryID, &d.DependsOnID); err != nil {
			return nil, err
		}
		deps = append(deps, d)
	}
	return deps, rows.Err()
}

// --- private helpers ---

func (s *SQLiteStore) decodePayload(evt Event) map[string]any {
	if evt.Payload == nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(evt.Payload, &m); err != nil {
		return map[string]any{}
	}
	return m
}

func (s *SQLiteStore) projectReqSubmitted(payload map[string]any) error {
	_, err := s.db.Exec(
		`INSERT INTO requirements (id, title, description, status, repo_path) VALUES (?, ?, ?, 'pending', ?)`,
		payloadStr(payload, "id"),
		payloadStr(payload, "title"),
		payloadStr(payload, "description"),
		payloadStr(payload, "repo_path"),
	)
	return err
}

func (s *SQLiteStore) updateReqStatus(payload map[string]any, status string) error {
	id := payloadStr(payload, "id")
	_, err := s.db.Exec(
		`UPDATE requirements SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, id,
	)
	return err
}

func (s *SQLiteStore) projectStoryCreated(payload map[string]any) error {
	complexity := payloadInt(payload, "complexity")
	storyID := payloadStr(payload, "id")

	ownedFilesJSON := "[]"
	if of, ok := payload["owned_files"]; ok {
		if b, err := json.Marshal(of); err == nil {
			ownedFilesJSON = string(b)
		}
	}

	waveHint := payloadStr(payload, "wave_hint")
	if waveHint == "" {
		waveHint = "parallel"
	}

	splitDepth := payloadInt(payload, "split_depth")

	_, err := s.db.Exec(
		`INSERT INTO stories (id, req_id, title, description, acceptance_criteria, complexity, status, owned_files, wave_hint, split_depth)
		 VALUES (?, ?, ?, ?, ?, ?, 'draft', ?, ?, ?)`,
		storyID,
		payloadStr(payload, "req_id"),
		payloadStr(payload, "title"),
		payloadStr(payload, "description"),
		payloadStr(payload, "acceptance_criteria"),
		complexity,
		ownedFilesJSON,
		waveHint,
		splitDepth,
	)
	if err != nil {
		return err
	}

	// Populate story_deps table
	if deps, ok := payload["depends_on"]; ok {
		if depSlice, ok := deps.([]any); ok {
			for _, dep := range depSlice {
				if depStr, ok := dep.(string); ok && depStr != "" {
					_, err := s.db.Exec(
						`INSERT OR IGNORE INTO story_deps (story_id, depends_on_id) VALUES (?, ?)`,
						storyID, depStr,
					)
					if err != nil {
						return fmt.Errorf("insert story dep %s -> %s: %w", storyID, depStr, err)
					}
				}
			}
		}
	}
	return nil
}

func (s *SQLiteStore) projectStoryAssigned(storyID string, payload map[string]any) error {
	agentID := payloadStr(payload, "agent_id")
	wave := payloadInt(payload, "wave")
	_, err := s.db.Exec(
		`UPDATE stories SET status = 'assigned', agent_id = ?, wave = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		agentID, wave, storyID,
	)
	return err
}

func (s *SQLiteStore) projectStoryPRCreated(storyID string, payload map[string]any) error {
	prNumber := payloadInt(payload, "pr_number")
	prURL := payloadStr(payload, "pr_url")
	_, err := s.db.Exec(
		`UPDATE stories SET status = 'pr_submitted', pr_url = ?, pr_number = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		prURL, prNumber, storyID,
	)
	return err
}

func (s *SQLiteStore) updateStoryStatus(storyID, status string) error {
	_, err := s.db.Exec(
		`UPDATE stories SET status = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		status, storyID,
	)
	return err
}

// BackfillAcceptanceCriteria updates stories that have an empty
// acceptance_criteria by extracting it from STORY_CREATED events.
// This handles databases created before the column was added.
func (s *SQLiteStore) BackfillAcceptanceCriteria(events []Event) {
	for _, evt := range events {
		if evt.Type != EventStoryCreated || evt.Payload == nil {
			continue
		}
		payload := s.decodePayload(evt)
		ac := payloadStr(payload, "acceptance_criteria")
		storyID := payloadStr(payload, "id")
		if ac != "" && storyID != "" {
			s.db.Exec(
				`UPDATE stories SET acceptance_criteria = ? WHERE id = ? AND acceptance_criteria = ''`,
				ac, storyID,
			)
		}
	}
}

// ArchiveRequirement sets a requirement's status to "archived".
func (s *SQLiteStore) ArchiveRequirement(reqID string) error {
	_, err := s.db.Exec(
		`UPDATE requirements SET status = 'archived', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		reqID,
	)
	return err
}

// ArchiveStoriesByReq sets all stories for a given requirement to "archived".
func (s *SQLiteStore) ArchiveStoriesByReq(reqID string) error {
	_, err := s.db.Exec(
		`UPDATE stories SET status = 'archived', updated_at = CURRENT_TIMESTAMP WHERE req_id = ?`,
		reqID,
	)
	return err
}

func (s *SQLiteStore) projectStoryEscalated(evt Event, payload map[string]any) error {
	fromTier := payloadInt(payload, "from_tier")
	toTier := payloadInt(payload, "to_tier")
	reason := payloadStr(payload, "reason")

	if _, err := s.db.Exec(
		`UPDATE stories SET escalation_tier = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		toTier, evt.StoryID,
	); err != nil {
		return fmt.Errorf("update story escalation_tier: %w", err)
	}

	id := ulid.MustNew(ulid.Timestamp(evt.Timestamp), rand.Reader)
	_, err := s.db.Exec(
		`INSERT INTO escalations (id, story_id, from_agent, reason, status, from_tier, to_tier, created_at)
		 VALUES (?, ?, ?, ?, 'pending', ?, ?, ?)`,
		id.String(), evt.StoryID, evt.AgentID, reason, fromTier, toTier, evt.Timestamp,
	)
	return err
}

func (s *SQLiteStore) projectStoryRewritten(storyID string, payload map[string]any) error {
	changes := payloadMap(payload, "changes")

	if title, ok := changes["title"].(string); ok && title != "" {
		s.db.Exec(`UPDATE stories SET title = ? WHERE id = ?`, title, storyID)
	}
	if desc, ok := changes["description"].(string); ok && desc != "" {
		s.db.Exec(`UPDATE stories SET description = ? WHERE id = ?`, desc, storyID)
	}
	if ac, ok := changes["acceptance_criteria"].(string); ok && ac != "" {
		s.db.Exec(`UPDATE stories SET acceptance_criteria = ? WHERE id = ?`, ac, storyID)
	}
	if complexity, ok := changes["complexity"]; ok {
		if c, ok := complexity.(float64); ok {
			s.db.Exec(`UPDATE stories SET complexity = ? WHERE id = ?`, int(c), storyID)
		} else if c, ok := complexity.(int); ok {
			s.db.Exec(`UPDATE stories SET complexity = ? WHERE id = ?`, c, storyID)
		}
	}

	_, err := s.db.Exec(
		`UPDATE stories SET escalation_tier = 0, status = 'draft', updated_at = CURRENT_TIMESTAMP WHERE id = ?`,
		storyID,
	)
	return err
}

// --- payload extraction helpers ---

func payloadStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return s
}

func payloadInt(m map[string]any, key string) int {
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

func payloadMap(m map[string]any, key string) map[string]any {
	v, ok := m[key]
	if !ok {
		return map[string]any{}
	}
	sub, ok := v.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return sub
}

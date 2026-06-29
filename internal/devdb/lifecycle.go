package devdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Config is the devdb-side view of nxd.yaml's `devdb:` block, supplied by
// callers (engine, preflight). The full config struct lives in
// internal/config.DevDBConfig; this is the slice the Lifecycle needs.
type Config struct {
	Provider     string
	Template     string
	KeepDBOnFail bool
	RetainHours  time.Duration
}

// EventAppender is the subset of state.EventStore the Lifecycle needs.
// Decouples Lifecycle from the concrete event store for testability.
type EventAppender interface {
	Append(state.Event) error
}

// EventProjector projects an emitted event into the read model. The Lifecycle
// emits STORY_DB_* events directly (callers never see them), so it must drive
// the projection itself — otherwise the story_databases table stays empty in
// production even though the projection switch handles those event types.
type EventProjector interface {
	Project(state.Event) error
}

// Lifecycle orchestrates a Provider + event emission + worktree file writes.
// Engine code uses Lifecycle, not Provider directly.
type Lifecycle struct {
	provider  Provider
	events    EventAppender
	projector EventProjector
	cfg       Config
	clock     func() time.Time
}

// NewLifecycle wires a Lifecycle with the supplied Provider, event appender,
// and config.
func NewLifecycle(p Provider, ea EventAppender, cfg Config) *Lifecycle {
	return &Lifecycle{
		provider: p,
		events:   ea,
		cfg:      cfg,
		clock:    func() time.Time { return time.Now().UTC() },
	}
}

// WithProjector attaches a projector so emitted STORY_DB_* events also update
// the read model. Returns the receiver for chaining. When nil (the default),
// events are appended only.
func (l *Lifecycle) WithProjector(p EventProjector) *Lifecycle {
	l.projector = p
	return l
}

// emit appends an event and, when a projector is wired, projects it so the
// read model reflects the DB lifecycle. Append failures are swallowed (events
// are best-effort telemetry, matching the prior behaviour); a projection
// failure is non-fatal too — the append already durably recorded the event.
func (l *Lifecycle) emit(evt state.Event) {
	_ = l.events.Append(evt)
	if l.projector != nil {
		_ = l.projector.Project(evt)
	}
}

// Provider exposes the underlying provider (used for orphan recovery, ping).
func (l *Lifecycle) Provider() Provider { return l.provider }

// Provision creates or forks a DB for the given story, writes .nxd-db/ files
// into worktreeDir, and emits STORY_DB_CREATED. On failure emits
// STORY_DB_FAILED and returns the wrapped error.
func (l *Lifecycle) Provision(ctx context.Context, storyID, project, worktreeDir string) (DB, error) {
	name := FormatDBName(PrefixNXD, project, storyID)

	var (
		db  DB
		err error
	)
	if l.cfg.Template != "" {
		db, err = l.provider.Fork(ctx, l.cfg.Template, CreateOpts{Name: name, WaitReady: true})
	} else {
		db, err = l.provider.Create(ctx, CreateOpts{Name: name, WaitReady: true})
	}
	if err != nil {
		l.emitFailed(storyID, name, fmt.Sprintf("provision: %v", err))
		return DB{}, fmt.Errorf("devdb provision: %w", err)
	}
	db.Provider = l.provider.Name()

	if err := WriteEnvFiles(worktreeDir, db); err != nil {
		l.emitFailed(storyID, name, fmt.Sprintf("envfile: %v", err))
		return DB{}, fmt.Errorf("devdb write envfile: %w", err)
	}

	l.emitCreated(storyID, db)
	return db, nil
}

// Release deletes the DB and emits STORY_DB_DELETED.
// Honours cfg.KeepDBOnFail: if the story failed and KeepDBOnFail is true,
// skips the delete call and emits STORY_DB_DELETED with status="retained".
func (l *Lifecycle) Release(ctx context.Context, db DB, outcome StoryOutcome) error {
	status := "deleted"
	keep := outcome != OutcomeSuccess && l.cfg.KeepDBOnFail
	if keep {
		status = "retained"
	}

	// The DB name encodes the story ID (FormatDBName). Recover it so the
	// emitted events carry StoryID — projectStoryDBDeleted/Failed key their
	// UPDATE on story_id, so an empty value would silently match zero rows.
	storyID := ParseStoryID(PrefixNXD, db.Name)

	if !keep {
		if err := l.provider.Delete(ctx, db.ID); err != nil {
			// Emit a failed-release event so GC can pick up later. We do not
			// return the error after the event is emitted — callers don't
			// need to block pipeline progress on release failures.
			l.emitFailed(storyID, db.Name, fmt.Sprintf("release: %v", err))
			return fmt.Errorf("devdb release: %w", err)
		}
	}

	duration := 0.0
	if !db.CreatedAt.IsZero() {
		duration = l.clock().Sub(db.CreatedAt).Seconds()
	}
	payload := map[string]any{
		"db_id":            db.ID,
		"duration_seconds": duration,
		"bytes_used":       0,
		"status":           status,
	}
	data, _ := json.Marshal(payload)
	l.emit(state.Event{
		Type:      state.EventStoryDBDeleted,
		StoryID:   storyID,
		Timestamp: l.clock(),
		Payload:   data,
	})
	return nil
}

func (l *Lifecycle) emitCreated(storyID string, db DB) {
	h := sha256.Sum256([]byte(db.ConnectionString))
	payload := map[string]any{
		"db_id":            db.ID,
		"db_name":          db.Name,
		"provider":         db.Provider,
		"template":         l.cfg.Template,
		"conn_string_hash": "sha256:" + hex.EncodeToString(h[:]),
	}
	data, _ := json.Marshal(payload)
	l.emit(state.Event{
		Type:      state.EventStoryDBCreated,
		StoryID:   storyID,
		Timestamp: l.clock(),
		Payload:   data,
	})
}

func (l *Lifecycle) emitFailed(storyID, name, errMsg string) {
	payload := map[string]any{
		"db_id":    name,
		"db_name":  name,
		"provider": l.provider.Name(),
		"error":    errMsg,
	}
	data, _ := json.Marshal(payload)
	l.emit(state.Event{
		Type:      state.EventStoryDBFailed,
		StoryID:   storyID,
		Timestamp: l.clock(),
		Payload:   data,
	})
}

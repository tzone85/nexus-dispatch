// Package devdb provides per-story ephemeral Postgres database provisioning.
// Providers (docker, null) implement the Provider interface; the
// Lifecycle helper orchestrates Provider calls + event emission for NXD's
// pipeline.
package devdb

import (
	"context"
	"errors"
	"time"
)

// Provider provisions ephemeral databases for stories.
// Implementations live under subpackages: docker (local), null (no-op).
type Provider interface {
	// Name returns the provider identifier ("docker", "null").
	Name() string

	// Create provisions a new empty database.
	Create(ctx context.Context, opts CreateOpts) (DB, error)

	// Fork creates a copy of a template database.
	Fork(ctx context.Context, template string, opts CreateOpts) (DB, error)

	// Delete removes a database permanently.
	Delete(ctx context.Context, dbID string) error

	// List returns all DBs managed by this provider in the current space/host.
	List(ctx context.Context) ([]DB, error)

	// Schema returns an agent-friendly text dump of the DB's schema.
	Schema(ctx context.Context, dbID string) (string, error)

	// Ping verifies the provider is reachable. Used by preflight.
	Ping(ctx context.Context) error
}

// CreateOpts controls Provider.Create / Provider.Fork behaviour.
type CreateOpts struct {
	// Name is the canonical DB name. Must be a valid Postgres identifier
	// (validated by the naming package; see naming.IsValid).
	Name string

	// Labels are provider-specific metadata; e.g. story_id, requirement_id.
	Labels map[string]string

	// ReadOnly requests a read-only DSN if the provider supports it.
	ReadOnly bool

	// WaitReady blocks until the DB accepts connections.
	WaitReady bool

	// WaitTimeout caps WaitReady. Zero means no wait or provider default.
	WaitTimeout time.Duration
}

// DB describes a provisioned database returned to callers.
type DB struct {
	ID               string
	Name             string
	Provider         string
	ConnectionString string
	ReadOnlyDSN      string
	CreatedAt        time.Time
	Labels           map[string]string
}

// StoryOutcome enumerates how a story finished, controlling Lifecycle.Release.
type StoryOutcome int

const (
	OutcomeSuccess StoryOutcome = iota
	OutcomeFailed
	OutcomePaused
)

// String returns the lowercase canonical name of the outcome.
func (s StoryOutcome) String() string {
	switch s {
	case OutcomeSuccess:
		return "success"
	case OutcomeFailed:
		return "failed"
	case OutcomePaused:
		return "paused"
	default:
		return "unknown"
	}
}

// Sentinel errors. Provider implementations wrap underlying errors using
// fmt.Errorf("...: %w", ErrXxx) so callers can errors.Is.
var (
	ErrNotFound      = errors.New("devdb: database not found")
	ErrAlreadyExists = errors.New("devdb: database already exists")
	ErrProviderDown  = errors.New("devdb: provider unreachable")
	ErrInvalidName   = errors.New("devdb: invalid database name")
	ErrTemplateMiss  = errors.New("devdb: template database not found")
	ErrUnsupported   = errors.New("devdb: operation not supported by provider")
)

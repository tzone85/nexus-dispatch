// Package null implements a no-op devdb.Provider. Used as the default
// (provider: null in config) and as a fake in tests where a real Provider
// is not needed.
package null

import (
	"context"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/devdb"
)

// Provider is the no-op devdb.Provider.
type Provider struct{}

// New returns a ready-to-use null provider.
func New() *Provider { return &Provider{} }

// Name returns "null".
func (p *Provider) Name() string { return "null" }

// Create returns a deterministic fake DB.
func (p *Provider) Create(ctx context.Context, opts devdb.CreateOpts) (devdb.DB, error) {
	return devdb.DB{
		ID:               "null-" + opts.Name,
		Name:             opts.Name,
		Provider:         "null",
		ConnectionString: "postgres://null@localhost:0/" + opts.Name,
		CreatedAt:        time.Now().UTC(),
		Labels:           opts.Labels,
	}, nil
}

// Fork ignores the template and behaves like Create.
func (p *Provider) Fork(ctx context.Context, template string, opts devdb.CreateOpts) (devdb.DB, error) {
	return p.Create(ctx, opts)
}

// Delete always succeeds.
func (p *Provider) Delete(ctx context.Context, dbID string) error { return nil }

// List returns an empty (non-nil) slice (the null provider tracks nothing).
func (p *Provider) List(ctx context.Context) ([]devdb.DB, error) {
	return []devdb.DB{}, nil
}

// Schema returns empty for null DBs.
func (p *Provider) Schema(ctx context.Context, dbID string) (string, error) { return "", nil }

// Ping always succeeds.
func (p *Provider) Ping(ctx context.Context) error { return nil }

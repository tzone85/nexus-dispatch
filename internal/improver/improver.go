// Package improver is NXD's self-improvement module.
//
// The improver runs a set of small, offline-first analyzers over the
// project state (metrics.jsonl, event store, source tree) and emits
// Suggestions that highlight concrete things the operator can do to
// improve performance, cost, UX, or reliability. An optional online
// feed can supply curated tips on top of the local heuristics.
//
// Design goals:
//
//   - Offline by default. The CLI works on a laptop with no network.
//   - Cheap. Each analyzer scans data already on disk; no LLM calls.
//   - Surface, don't act. Suggestions are advisory — the dashboard
//     shows them as a popup, but the operator decides whether to act.
//   - Pluggable. Analyzers and the online feed are interface-typed so
//     plugins can extend the catalogue without forking core.
package improver

import (
	"context"
	"sort"
	"time"
)

// Severity ranks a Suggestion. Higher severities surface first in the
// dashboard and are emitted as toasts.
type Severity string

const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// Source records where a Suggestion came from. "local" means an offline
// analyzer in this package; "online" means a curated tip pulled from the
// configured online feed.
type Source string

const (
	SourceLocal  Source = "local"
	SourceOnline Source = "online"
)

// Suggestion is one improvement recommendation surfaced to the operator.
type Suggestion struct {
	// ID is a stable identifier (e.g. "metrics.high_failure_rate") so
	// the dashboard can suppress already-acknowledged suggestions
	// across runs.
	ID string `json:"id"`

	// Title is a short headline (<=80 chars). Renders as the popup
	// header.
	Title string `json:"title"`

	// Description is a 1-2 sentence explanation that includes the
	// triggering numbers ("avg latency 4.2s, target <2s").
	Description string `json:"description"`

	// Category is a coarse bucket: "performance", "ux", "cost",
	// "reliability", "code_quality".
	Category string `json:"category"`

	// Severity controls dashboard ordering and toast emission.
	Severity Severity `json:"severity"`

	// Evidence lists the raw data points the analyzer used. Two or
	// three short strings is plenty.
	Evidence []string `json:"evidence,omitempty"`

	// Action is the next step the operator should take ("set
	// runtimes.gemma.concurrency: 2"). May be empty when the
	// recommendation is exploratory.
	Action string `json:"action,omitempty"`

	// Source distinguishes local heuristics from online curated tips.
	Source Source `json:"source"`

	// CreatedAt is when the suggestion was generated. Dashboard uses
	// this to dedupe consecutive runs.
	CreatedAt time.Time `json:"created_at"`
}

// ProjectInfo holds inputs the analyzers may need. Each field is
// optional — analyzers that don't have what they need return nil to
// signal "skip".
type ProjectInfo struct {
	// StateDir is the resolved NXD state directory (typically
	// ~/.nxd). Analyzers find metrics.jsonl, events.jsonl, etc. here.
	StateDir string

	// ProjectDir is the user's repo root, used by analyzers that
	// inspect source code (coverage, lint output).
	ProjectDir string

	// Now is injected for testability. When zero, analyzers fall
	// back to time.Now().
	Now time.Time
}

// Analyzer scans ProjectInfo and emits zero or more Suggestions.
// Analyzers MUST honour ctx cancellation: they should bail early if
// ctx.Err() != nil and never block on long IO without checking.
type Analyzer interface {
	Name() string
	Run(ctx context.Context, info ProjectInfo) ([]Suggestion, error)
}

// AnalyzerFunc adapts a plain function into an Analyzer. Useful for
// tests and small one-off heuristics that don't need state.
type AnalyzerFunc struct {
	Label string
	Fn    func(ctx context.Context, info ProjectInfo) ([]Suggestion, error)
}

func (a AnalyzerFunc) Name() string { return a.Label }
func (a AnalyzerFunc) Run(ctx context.Context, info ProjectInfo) ([]Suggestion, error) {
	return a.Fn(ctx, info)
}

// Improver bundles a list of Analyzers. The default Improver registered
// by NewImprover wires the built-in heuristics; plugins can add their
// own with WithAnalyzer.
type Improver struct {
	analyzers []Analyzer
	online    OnlineFetcher
}

// Option mutates an Improver during construction.
type Option func(*Improver)

// WithAnalyzer adds an extra analyzer to the run.
func WithAnalyzer(a Analyzer) Option {
	return func(i *Improver) { i.analyzers = append(i.analyzers, a) }
}

// WithOnline wires an online tips fetcher. Pass nil to disable.
func WithOnline(f OnlineFetcher) Option {
	return func(i *Improver) { i.online = f }
}

// NewImprover returns an Improver seeded with the built-in analyzers.
func NewImprover(opts ...Option) *Improver {
	i := &Improver{
		analyzers: defaultAnalyzers(),
	}
	for _, o := range opts {
		o(i)
	}
	return i
}

// Run executes every analyzer and merges the suggestions. Analyzer
// errors are returned alongside the partial result so the caller can
// surface them but still see whatever suggestions did succeed.
func (i *Improver) Run(ctx context.Context, info ProjectInfo) ([]Suggestion, []error) {
	if info.Now.IsZero() {
		info.Now = time.Now()
	}
	var suggestions []Suggestion
	var errs []error

	for _, a := range i.analyzers {
		select {
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return suggestions, errs
		default:
		}
		out, err := a.Run(ctx, info)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for j := range out {
			if out[j].Source == "" {
				out[j].Source = SourceLocal
			}
			if out[j].CreatedAt.IsZero() {
				out[j].CreatedAt = info.Now
			}
		}
		suggestions = append(suggestions, out...)
	}

	if i.online != nil {
		online, err := i.online.Fetch(ctx, info)
		if err != nil {
			errs = append(errs, err)
		} else {
			for j := range online {
				online[j].Source = SourceOnline
				if online[j].CreatedAt.IsZero() {
					online[j].CreatedAt = info.Now
				}
			}
			suggestions = append(suggestions, online...)
		}
	}

	sort.SliceStable(suggestions, func(a, b int) bool {
		return severityRank(suggestions[a].Severity) > severityRank(suggestions[b].Severity)
	})
	return suggestions, errs
}

// severityRank assigns numeric weights so SortStable orders critical >
// warning > info, with unknown values falling to the bottom.
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

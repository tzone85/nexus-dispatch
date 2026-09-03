// Package approvals is the human approval queue: pipeline decisions that need
// a person (conflict resolutions, post-merge integration failures, security
// findings, gated merges) are recorded as items, persisted as events, and
// resolved from the CLI (`nxd approvals`) or the dashboard.
//
// The Queue is the only writer. It keeps an in-memory index that is rebuilt
// from APPROVAL_REQUESTED / APPROVAL_RESOLVED events on Load, so every process
// (resume loop, CLI, dashboard) sees the same state through the event store.
package approvals

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Kind classifies what a human is being asked to approve.
type Kind string

const (
	KindConflictResolution Kind = "conflict_resolution"
	KindIntegrationFailure Kind = "integration_failure"
	KindSecurityFinding    Kind = "security_finding"
	KindMerge              Kind = "merge"
)

// Kinds lists every valid Kind in display order.
func Kinds() []Kind {
	return []Kind{KindConflictResolution, KindIntegrationFailure, KindSecurityFinding, KindMerge}
}

// ValidKind reports whether k is a known Kind.
func ValidKind(k Kind) bool {
	for _, known := range Kinds() {
		if k == known {
			return true
		}
	}
	return false
}

// Status is the lifecycle state of an Item.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusRejected Status = "rejected"
)

// Item is one approval request.
type Item struct {
	ID        string    `json:"id"`
	ReqID     string    `json:"req_id"`
	StoryID   string    `json:"story_id,omitempty"`
	Kind      Kind      `json:"kind"`
	Summary   string    `json:"summary"`
	Details   string    `json:"details,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	Status    Status    `json:"status"`
	DecidedBy string    `json:"decided_by,omitempty"`
	DecidedAt time.Time `json:"decided_at,omitempty"`
	Note      string    `json:"note,omitempty"`
}

// Pending reports whether the item still awaits a decision.
func (it Item) Pending() bool { return it.Status == StatusPending }

// Sentinel errors.
var (
	ErrNotFound        = errors.New("approval not found")
	ErrAlreadyResolved = errors.New("approval already resolved")
	ErrInvalidKind     = errors.New("invalid approval kind")
	ErrInvalidStatus   = errors.New("resolution must be approved or rejected")
	ErrMissingReq      = errors.New("approval requires a req_id")
)

// Clock is injectable for deterministic tests.
type Clock func() time.Time

// Queue is the in-memory index over the event store.
type Queue struct {
	mu    sync.RWMutex
	store state.EventStore
	now   Clock
	items map[string]*Item
	order []string // insertion order of IDs (oldest first)
}

// New builds an empty Queue over store. Call Load to rebuild from events.
func New(store state.EventStore) *Queue {
	return &Queue{store: store, now: time.Now, items: map[string]*Item{}}
}

// WithClock replaces the clock (tests).
func (q *Queue) WithClock(c Clock) *Queue {
	q.now = c
	return q
}

// Load rebuilds the queue from APPROVAL_REQUESTED / APPROVAL_RESOLVED events.
// Idempotent: the index is reset before replay.
func Load(store state.EventStore) (*Queue, error) {
	q := New(store)
	if err := q.Load(); err != nil {
		return nil, err
	}
	return q, nil
}

// Load replays the store into the in-memory index.
func (q *Queue) Load() error {
	requested, err := q.store.List(state.EventFilter{Type: state.EventApprovalRequested})
	if err != nil {
		return fmt.Errorf("list %s: %w", state.EventApprovalRequested, err)
	}
	resolved, err := q.store.List(state.EventFilter{Type: state.EventApprovalResolved})
	if err != nil {
		return fmt.Errorf("list %s: %w", state.EventApprovalResolved, err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	q.items = map[string]*Item{}
	q.order = nil
	for _, evt := range requested {
		it := itemFromRequested(evt)
		if it.ID == "" {
			continue
		}
		q.index(it)
	}
	for _, evt := range resolved {
		applyResolved(q.items, evt)
	}
	return nil
}

// index inserts an item (lock held).
func (q *Queue) index(it Item) {
	if _, exists := q.items[it.ID]; !exists {
		q.order = append(q.order, it.ID)
	}
	copyIt := it
	q.items[it.ID] = &copyIt
}

// Request records a new pending approval and persists APPROVAL_REQUESTED.
// The ID is a ULID derived from the queue clock, so items sort by creation
// time. Returns the stored item.
func (q *Queue) Request(reqID, storyID string, kind Kind, summary, details string) (Item, error) {
	if strings.TrimSpace(reqID) == "" {
		return Item{}, ErrMissingReq
	}
	if !ValidKind(kind) {
		return Item{}, fmt.Errorf("%w: %q", ErrInvalidKind, kind)
	}
	now := q.now().UTC()
	it := Item{
		ID:        newID(now),
		ReqID:     reqID,
		StoryID:   storyID,
		Kind:      kind,
		Summary:   strings.TrimSpace(summary),
		Details:   details,
		CreatedAt: now,
		Status:    StatusPending,
	}
	evt := state.NewEvent(state.EventApprovalRequested, "approvals", storyID, map[string]any{
		"id":         it.ID,
		"req_id":     it.ReqID,
		"story_id":   it.StoryID,
		"kind":       string(it.Kind),
		"summary":    it.Summary,
		"details":    it.Details,
		"created_at": it.CreatedAt.Format(time.RFC3339Nano),
	})
	if err := q.store.Append(evt); err != nil {
		return Item{}, fmt.Errorf("append %s: %w", state.EventApprovalRequested, err)
	}
	q.mu.Lock()
	q.index(it)
	q.mu.Unlock()
	return it, nil
}

// Resolve marks a pending item approved or rejected and persists
// APPROVAL_RESOLVED. Resolving twice returns ErrAlreadyResolved.
func (q *Queue) Resolve(id string, status Status, decidedBy, note string) (Item, error) {
	if status != StatusApproved && status != StatusRejected {
		return Item{}, fmt.Errorf("%w: %q", ErrInvalidStatus, status)
	}
	q.mu.RLock()
	cur, ok := q.items[id]
	var snapshot Item
	if ok {
		snapshot = *cur
	}
	q.mu.RUnlock()
	if !ok {
		return Item{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if !snapshot.Pending() {
		return snapshot, fmt.Errorf("%w: %s is %s", ErrAlreadyResolved, id, snapshot.Status)
	}
	now := q.now().UTC()
	evt := state.NewEvent(state.EventApprovalResolved, "approvals", snapshot.StoryID, map[string]any{
		"id":         id,
		"req_id":     snapshot.ReqID,
		"kind":       string(snapshot.Kind),
		"status":     string(status),
		"decided_by": decidedBy,
		"decided_at": now.Format(time.RFC3339Nano),
		"note":       note,
	})
	if err := q.store.Append(evt); err != nil {
		return Item{}, fmt.Errorf("append %s: %w", state.EventApprovalResolved, err)
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	it := q.items[id]
	it.Status = status
	it.DecidedBy = decidedBy
	it.DecidedAt = now
	it.Note = note
	return *it, nil
}

// Get returns the item by ID.
func (q *Queue) Get(id string) (Item, error) {
	q.mu.RLock()
	defer q.mu.RUnlock()
	it, ok := q.items[id]
	if !ok {
		return Item{}, fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	return *it, nil
}

// Pending returns pending items, oldest first. An empty reqID returns every
// pending item; otherwise only that requirement's.
func (q *Queue) Pending(reqID string) []Item {
	return q.filter(func(it *Item) bool {
		return it.Pending() && (reqID == "" || it.ReqID == reqID)
	})
}

// PendingForStory returns pending items for one story (optionally of one kind;
// empty kind matches all), oldest first.
func (q *Queue) PendingForStory(reqID, storyID string, kind Kind) []Item {
	return q.filter(func(it *Item) bool {
		return it.Pending() &&
			(reqID == "" || it.ReqID == reqID) &&
			it.StoryID == storyID &&
			(kind == "" || it.Kind == kind)
	})
}

// All returns every item (any status), oldest first, optionally filtered by
// requirement.
func (q *Queue) All(reqID string) []Item {
	return q.filter(func(it *Item) bool { return reqID == "" || it.ReqID == reqID })
}

func (q *Queue) filter(keep func(*Item) bool) []Item {
	q.mu.RLock()
	defer q.mu.RUnlock()
	out := make([]Item, 0, len(q.order))
	for _, id := range q.order {
		if it := q.items[id]; keep(it) {
			out = append(out, *it)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// newID returns a ULID whose time component is now, so IDs are sortable and
// unique even for items created in the same millisecond.
func newID(now time.Time) string {
	return ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
}

// itemFromRequested decodes an APPROVAL_REQUESTED event.
func itemFromRequested(evt state.Event) Item {
	p := state.DecodePayload(evt.Payload)
	it := Item{
		ID:        str(p, "id"),
		ReqID:     str(p, "req_id"),
		StoryID:   str(p, "story_id"),
		Kind:      Kind(str(p, "kind")),
		Summary:   str(p, "summary"),
		Details:   str(p, "details"),
		CreatedAt: evt.Timestamp,
		Status:    StatusPending,
	}
	if ts, err := time.Parse(time.RFC3339Nano, str(p, "created_at")); err == nil {
		it.CreatedAt = ts
	}
	return it
}

// applyResolved folds an APPROVAL_RESOLVED event into items. Unknown IDs and
// invalid statuses are ignored (the log may be partially replayed).
func applyResolved(items map[string]*Item, evt state.Event) {
	p := state.DecodePayload(evt.Payload)
	it, ok := items[str(p, "id")]
	if !ok {
		return
	}
	status := Status(str(p, "status"))
	if status != StatusApproved && status != StatusRejected {
		return
	}
	it.Status = status
	it.DecidedBy = str(p, "decided_by")
	it.Note = str(p, "note")
	it.DecidedAt = evt.Timestamp
	if ts, err := time.Parse(time.RFC3339Nano, str(p, "decided_at")); err == nil {
		it.DecidedAt = ts
	}
}

func str(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

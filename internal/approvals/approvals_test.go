package approvals

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// memStore is a minimal in-memory EventStore with per-type filtering and
// injectable failures.
type memStore struct {
	mu        sync.Mutex
	events    []state.Event
	appendErr error
	listErr   map[state.EventType]error
}

func (m *memStore) Append(e state.Event) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, e)
	return nil
}

func (m *memStore) List(f state.EventFilter) ([]state.Event, error) {
	if err := m.listErr[f.Type]; err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []state.Event
	for _, e := range m.events {
		if f.Type == "" || e.Type == f.Type {
			out = append(out, e)
		}
	}
	return out, nil
}

func (m *memStore) Count(f state.EventFilter) (int, error) {
	evts, err := m.List(f)
	return len(evts), err
}
func (m *memStore) Close() error { return nil }

// fixedClock ticks one second per call; safe for concurrent use.
func fixedClock(start time.Time) Clock {
	var mu sync.Mutex
	t := start
	return func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		t = t.Add(time.Second)
		return t
	}
}

func newTestQueue(t *testing.T) (*Queue, *memStore) {
	t.Helper()
	store := &memStore{}
	q := New(store).WithClock(fixedClock(time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)))
	return q, store
}

func TestKinds(t *testing.T) {
	for _, k := range Kinds() {
		if !ValidKind(k) {
			t.Errorf("%s must be valid", k)
		}
	}
	if ValidKind("coffee") || ValidKind("") {
		t.Error("unknown kinds must be invalid")
	}
	if len(Kinds()) != 4 {
		t.Errorf("Kinds = %v", Kinds())
	}
}

func TestRequest_PersistsEventAndIndexes(t *testing.T) {
	q, store := newTestQueue(t)
	it, err := q.Request("req-1", "story-7", KindConflictResolution, "  3 files conflict  ", "a.go\nb.go")
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	if it.ID == "" || len(it.ID) != 26 {
		t.Errorf("ID must be a ULID, got %q", it.ID)
	}
	if it.Status != StatusPending || !it.Pending() || it.Summary != "3 files conflict" || it.Details != "a.go\nb.go" {
		t.Errorf("item = %+v", it)
	}
	if it.CreatedAt != time.Date(2026, 9, 3, 12, 0, 1, 0, time.UTC) {
		t.Errorf("CreatedAt = %v", it.CreatedAt)
	}
	if len(store.events) != 1 || store.events[0].Type != state.EventApprovalRequested || store.events[0].StoryID != "story-7" {
		t.Fatalf("events = %+v", store.events)
	}
	p := state.DecodePayload(store.events[0].Payload)
	for k, want := range map[string]string{"id": it.ID, "req_id": "req-1", "story_id": "story-7", "kind": "conflict_resolution", "summary": "3 files conflict", "details": "a.go\nb.go"} {
		if p[k] != want {
			t.Errorf("payload[%s] = %v, want %q", k, p[k], want)
		}
	}
	got, err := q.Get(it.ID)
	if err != nil || got != it {
		t.Errorf("Get = %+v, %v", got, err)
	}
}

func TestRequest_Validation(t *testing.T) {
	q, store := newTestQueue(t)
	if _, err := q.Request("", "s", KindMerge, "x", ""); !errors.Is(err, ErrMissingReq) {
		t.Errorf("empty req: %v", err)
	}
	if _, err := q.Request("r", "s", Kind("nope"), "x", ""); !errors.Is(err, ErrInvalidKind) {
		t.Errorf("bad kind: %v", err)
	}
	store.appendErr = errors.New("disk full")
	if _, err := q.Request("r", "s", KindMerge, "x", ""); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("append error: %v", err)
	}
	if len(q.Pending("")) != 0 {
		t.Error("failed append must not index the item")
	}
}

func TestIDsAreTimeOrderedAndUnique(t *testing.T) {
	q, _ := newTestQueue(t)
	var ids []string
	for i := 0; i < 5; i++ {
		it, _ := q.Request("r", "s", KindMerge, "m", "")
		ids = append(ids, it.ID)
	}
	seen := map[string]bool{}
	for i, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate id %s", id)
		}
		seen[id] = true
		if i > 0 && !(ids[i-1] < id) {
			t.Errorf("ids must sort by creation: %s !< %s", ids[i-1], id)
		}
	}
	// Same-millisecond IDs (fixed clock) must still be unique.
	same := New(&memStore{}).WithClock(func() time.Time { return time.Unix(1_700_000_000, 0) })
	a, _ := same.Request("r", "", KindMerge, "m", "")
	b, _ := same.Request("r", "", KindMerge, "m", "")
	if a.ID == b.ID {
		t.Error("ULID random component must keep same-ms IDs unique")
	}
}

func TestResolve(t *testing.T) {
	q, store := newTestQueue(t)
	it, _ := q.Request("req-1", "s1", KindSecurityFinding, "gitleaks: AWS key", "")

	got, err := q.Resolve(it.ID, StatusRejected, "thando", "rotate the key first")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Status != StatusRejected || got.DecidedBy != "thando" || got.Note != "rotate the key first" || got.Pending() {
		t.Errorf("resolved = %+v", got)
	}
	if got.DecidedAt != time.Date(2026, 9, 3, 12, 0, 2, 0, time.UTC) {
		t.Errorf("DecidedAt = %v", got.DecidedAt)
	}
	if len(store.events) != 2 || store.events[1].Type != state.EventApprovalResolved {
		t.Fatalf("events = %+v", store.events)
	}
	p := state.DecodePayload(store.events[1].Payload)
	if p["id"] != it.ID || p["status"] != "rejected" || p["decided_by"] != "thando" || p["note"] != "rotate the key first" || p["req_id"] != "req-1" || p["kind"] != "security_finding" {
		t.Errorf("payload = %v", p)
	}
	if len(q.Pending("req-1")) != 0 {
		t.Error("resolved item must leave the pending list")
	}
	if _, err := q.Resolve(it.ID, StatusApproved, "x", ""); !errors.Is(err, ErrAlreadyResolved) {
		t.Errorf("second resolve: %v", err)
	}
}

func TestResolve_Errors(t *testing.T) {
	q, store := newTestQueue(t)
	if _, err := q.Resolve("missing", StatusApproved, "x", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing: %v", err)
	}
	it, _ := q.Request("r", "s", KindMerge, "m", "")
	for _, bad := range []Status{StatusPending, Status("maybe"), ""} {
		if _, err := q.Resolve(it.ID, bad, "x", ""); !errors.Is(err, ErrInvalidStatus) {
			t.Errorf("status %q: %v", bad, err)
		}
	}
	store.appendErr = errors.New("ro")
	if _, err := q.Resolve(it.ID, StatusApproved, "x", ""); err == nil || !strings.Contains(err.Error(), "ro") {
		t.Errorf("append error: %v", err)
	}
	if got, _ := q.Get(it.ID); !got.Pending() {
		t.Error("failed append must not change in-memory state")
	}
	if _, err := q.Get("nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing: %v", err)
	}
}

func TestPending_Filters(t *testing.T) {
	q, _ := newTestQueue(t)
	_, _ = q.Request("req-A", "s1", KindConflictResolution, "a", "")
	_, _ = q.Request("req-B", "s2", KindIntegrationFailure, "b", "")
	_, _ = q.Request("req-A", "s1", KindMerge, "c", "")
	d, _ := q.Request("req-A", "s3", KindSecurityFinding, "d", "")
	_, _ = q.Resolve(d.ID, StatusApproved, "op", "")

	ids := func(items []Item) string {
		var out []string
		for _, it := range items {
			out = append(out, it.Summary)
		}
		return strings.Join(out, ",")
	}
	if got := ids(q.Pending("")); got != "a,b,c" {
		t.Errorf("Pending(all) = %s", got)
	}
	if got := ids(q.Pending("req-A")); got != "a,c" {
		t.Errorf("Pending(req-A) = %s", got)
	}
	if got := ids(q.Pending("req-Z")); got != "" {
		t.Errorf("Pending(req-Z) = %s", got)
	}
	if got := ids(q.All("")); got != "a,b,c,d" {
		t.Errorf("All = %s", got)
	}
	if got := ids(q.All("req-A")); got != "a,c,d" {
		t.Errorf("All(req-A) = %s", got)
	}
	if got := ids(q.PendingForStory("req-A", "s1", "")); got != "a,c" {
		t.Errorf("PendingForStory(s1) = %s", got)
	}
	if got := ids(q.PendingForStory("", "s1", KindMerge)); got != "c" {
		t.Errorf("PendingForStory(s1, merge) = %s", got)
	}
	if got := ids(q.PendingForStory("req-B", "s1", "")); got != "" {
		t.Errorf("PendingForStory(wrong req) = %s", got)
	}
}

func TestLoad_RebuildsFromEvents(t *testing.T) {
	q1, store := newTestQueue(t)
	a, _ := q1.Request("req-1", "s1", KindConflictResolution, "conflict", "files")
	b, _ := q1.Request("req-1", "s2", KindIntegrationFailure, "build red", "")
	_, _ = q1.Resolve(a.ID, StatusApproved, "thando", "go")

	// Noise the loader must ignore: unrelated events, a resolution for an
	// unknown id, a resolution with an invalid status, a request without id.
	_ = store.Append(state.NewEvent(state.EventStoryMerged, "m", "s1", nil))
	_ = store.Append(state.NewEvent(state.EventApprovalResolved, "x", "", map[string]any{"id": "ghost", "status": "approved"}))
	_ = store.Append(state.NewEvent(state.EventApprovalResolved, "x", "", map[string]any{"id": b.ID, "status": "maybe"}))
	_ = store.Append(state.NewEvent(state.EventApprovalRequested, "x", "", map[string]any{"summary": "no id"}))

	q2, err := Load(store)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	gotA, err := q2.Get(a.ID)
	if err != nil {
		t.Fatalf("Get a: %v", err)
	}
	if gotA != a.withDecision(StatusApproved, "thando", "go", time.Date(2026, 9, 3, 12, 0, 3, 0, time.UTC)) {
		t.Errorf("rebuilt a = %+v", gotA)
	}
	gotB, _ := q2.Get(b.ID)
	if gotB != b {
		t.Errorf("rebuilt b = %+v, want %+v", gotB, b)
	}
	if pend := q2.Pending("req-1"); len(pend) != 1 || pend[0].ID != b.ID {
		t.Errorf("pending after load = %+v", pend)
	}
	if len(q2.All("")) != 2 {
		t.Errorf("All after load = %+v", q2.All(""))
	}
	// Load is idempotent.
	if err := q2.Load(); err != nil || len(q2.All("")) != 2 {
		t.Errorf("second Load: %v, %d items", err, len(q2.All("")))
	}
}

// withDecision returns a copy of it as Resolve would leave it.
func (it Item) withDecision(s Status, by, note string, at time.Time) Item {
	it.Status, it.DecidedBy, it.Note, it.DecidedAt = s, by, note, at
	return it
}

func TestLoad_FallsBackToEventTimestamps(t *testing.T) {
	store := &memStore{}
	req := state.NewEvent(state.EventApprovalRequested, "x", "s", map[string]any{
		"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "req_id": "r", "kind": "merge", "summary": "m", "created_at": "not-a-time",
	})
	res := state.NewEvent(state.EventApprovalResolved, "x", "s", map[string]any{
		"id": "01ARZ3NDEKTSV4RRFFQ69G5FAV", "status": "approved", "decided_by": "op", "decided_at": "garbage",
	})
	_ = store.Append(req)
	_ = store.Append(res)
	q, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	it, _ := q.Get("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if !it.CreatedAt.Equal(req.Timestamp) || !it.DecidedAt.Equal(res.Timestamp) || it.Status != StatusApproved {
		t.Errorf("item = %+v", it)
	}
}

func TestLoad_StoreErrors(t *testing.T) {
	store := &memStore{listErr: map[state.EventType]error{state.EventApprovalRequested: errors.New("boom")}}
	if _, err := Load(store); err == nil || !strings.Contains(err.Error(), "APPROVAL_REQUESTED") {
		t.Errorf("requested list error: %v", err)
	}
	store = &memStore{listErr: map[state.EventType]error{state.EventApprovalResolved: errors.New("boom")}}
	if _, err := Load(store); err == nil || !strings.Contains(err.Error(), "APPROVAL_RESOLVED") {
		t.Errorf("resolved list error: %v", err)
	}
}

func TestStr(t *testing.T) {
	m := map[string]any{"a": "x", "n": 3}
	if str(m, "a") != "x" || str(m, "n") != "" || str(m, "missing") != "" {
		t.Error("str helper mismatch")
	}
}

func TestQueue_ConcurrentAccess(t *testing.T) {
	q, _ := newTestQueue(t)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			it, err := q.Request("r", "s", KindMerge, "m", "")
			if err != nil {
				t.Error(err)
				return
			}
			_ = q.Pending("r")
			if _, err := q.Resolve(it.ID, StatusApproved, "op", ""); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if len(q.All("")) != 20 || len(q.Pending("")) != 0 {
		t.Errorf("all=%d pending=%d", len(q.All("")), len(q.Pending("")))
	}
}

package engine

import (
	"errors"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// approvalMemStore is an in-memory EventStore for the approval queue.
type approvalMemStore struct {
	events    []state.Event
	appendErr error
}

func (m *approvalMemStore) Append(e state.Event) error {
	if m.appendErr != nil {
		return m.appendErr
	}
	m.events = append(m.events, e)
	return nil
}
func (m *approvalMemStore) List(f state.EventFilter) ([]state.Event, error) {
	var out []state.Event
	for _, e := range m.events {
		if f.Type == "" || e.Type == f.Type {
			out = append(out, e)
		}
	}
	return out, nil
}
func (m *approvalMemStore) Count(f state.EventFilter) (int, error) {
	e, err := m.List(f)
	return len(e), err
}
func (m *approvalMemStore) Close() error { return nil }

func newApprovalQueue(t *testing.T) (*approvals.Queue, *approvalMemStore) {
	t.Helper()
	store := &approvalMemStore{}
	q, err := approvals.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	return q, store
}

func TestApprovalRequired(t *testing.T) {
	cfg := config.DefaultConfig().Approvals
	for _, k := range []approvals.Kind{approvals.KindConflictResolution, approvals.KindIntegrationFailure, approvals.KindSecurityFinding} {
		if !ApprovalRequired(cfg, k) {
			t.Errorf("default config must require %s", k)
		}
	}
	if ApprovalRequired(cfg, approvals.KindMerge) {
		t.Error("merge is opt-in")
	}
	if ApprovalRequired(config.ApprovalsConfig{}, approvals.KindSecurityFinding) {
		t.Error("empty require_for requires nothing")
	}
}

func TestRequestHelpers_NilQueueIsNoop(t *testing.T) {
	it, created, err := RequestConflictApproval(nil, "r", "s", []string{"a.go"})
	if err != nil || created || it.ID != "" {
		t.Errorf("nil queue: %+v %v %v", it, created, err)
	}
	if _, created, _ := RequestIntegrationApproval(nil, "r", "s", "boom"); created {
		t.Error("nil queue must not create")
	}
	if _, created, _ := RequestSecurityApproval(nil, "r", "s", "x"); created {
		t.Error("nil queue must not create")
	}
	if _, created, _ := RequestMergeApproval(nil, "r", "s", "b"); created {
		t.Error("nil queue must not create")
	}
	if blocked, reason := MergeBlockedByApproval(nil, "r", "s"); blocked || reason != "" {
		t.Error("nil queue never blocks")
	}
	if rejected, _ := ApprovalRejected(nil, "r", "s", ""); rejected {
		t.Error("nil queue never rejects")
	}
}

func TestRequestConflictApproval(t *testing.T) {
	q, store := newApprovalQueue(t)
	it, created, err := RequestConflictApproval(q, "req-1", "s1", []string{"a.go", "b.go"})
	if err != nil || !created {
		t.Fatalf("err=%v created=%v", err, created)
	}
	if it.Kind != approvals.KindConflictResolution || it.Summary != "conflict resolution on 2 file(s) needs review" || it.Details != "a.go\nb.go" {
		t.Errorf("item = %+v", it)
	}
	if it.ReqID != "req-1" || it.StoryID != "s1" || !it.Pending() {
		t.Errorf("item = %+v", it)
	}
	if len(store.events) != 1 || store.events[0].Type != state.EventApprovalRequested {
		t.Errorf("events = %+v", store.events)
	}
	// Dedup: a second request for the same story+kind returns the pending item.
	again, created, err := RequestConflictApproval(q, "req-1", "s1", []string{"c.go"})
	if err != nil || created || again.ID != it.ID {
		t.Errorf("dedup failed: %+v created=%v err=%v", again, created, err)
	}
	if len(q.Pending("req-1")) != 1 {
		t.Errorf("pending = %d, want 1", len(q.Pending("req-1")))
	}
	// Single file gets a specific summary.
	one, _, _ := RequestConflictApproval(q, "req-1", "s2", []string{"only.go"})
	if one.Summary != "conflict resolution on only.go needs review" {
		t.Errorf("summary = %q", one.Summary)
	}
}

func TestRequestIntegrationAndSecurityAndMerge(t *testing.T) {
	q, _ := newApprovalQueue(t)
	integ, created, err := RequestIntegrationApproval(q, "req-1", "s1", "pkg x: undefined: Foo")
	if err != nil || !created || integ.Kind != approvals.KindIntegrationFailure || integ.Details != "pkg x: undefined: Foo" {
		t.Errorf("integration = %+v %v %v", integ, created, err)
	}
	sec, created, err := RequestSecurityApproval(q, "req-1", "s1", "gitleaks: AWS key in config.go")
	if err != nil || !created || sec.Kind != approvals.KindSecurityFinding || sec.Details != "gitleaks: AWS key in config.go" {
		t.Errorf("security = %+v %v %v", sec, created, err)
	}
	mg, created, err := RequestMergeApproval(q, "req-1", "s1", "nxd/s1")
	if err != nil || !created || mg.Kind != approvals.KindMerge || !strings.Contains(mg.Summary, "nxd/s1") {
		t.Errorf("merge = %+v %v %v", mg, created, err)
	}
	// Different kinds for the same story coexist; same kind dedups.
	if len(q.PendingForStory("req-1", "s1", "")) != 3 {
		t.Errorf("pending kinds = %+v", q.PendingForStory("req-1", "s1", ""))
	}
	if _, created, _ := RequestSecurityApproval(q, "req-1", "s1", "another"); created {
		t.Error("same kind must dedup")
	}
}

func TestRequestHelpers_PropagateStoreErrors(t *testing.T) {
	q, store := newApprovalQueue(t)
	store.appendErr = errors.New("disk full")
	if _, _, err := RequestIntegrationApproval(q, "req-1", "s1", "x"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := RequestConflictApproval(q, "", "s1", nil); err == nil {
		t.Error("missing req id must error")
	}
}

func TestMergeBlockedByApproval(t *testing.T) {
	q, _ := newApprovalQueue(t)
	if blocked, _ := MergeBlockedByApproval(q, "req-1", "s1"); blocked {
		t.Error("empty queue must not block")
	}
	a, _, _ := RequestConflictApproval(q, "req-1", "s1", []string{"a.go"})
	b, _, _ := RequestSecurityApproval(q, "req-1", "s1", "finding")
	_, _, _ = RequestIntegrationApproval(q, "req-1", "s2", "other story")

	blocked, reason := MergeBlockedByApproval(q, "req-1", "s1")
	if !blocked {
		t.Fatal("pending approvals must block the merge")
	}
	for _, want := range []string{"2 approval(s) pending", a.ID + " (conflict_resolution)", b.ID + " (security_finding)", "nxd approvals approve|reject"} {
		if !strings.Contains(reason, want) {
			t.Errorf("reason %q missing %q", reason, want)
		}
	}
	if blocked, _ := MergeBlockedByApproval(q, "req-1", "s3"); blocked {
		t.Error("story without approvals must not block")
	}
	if blocked, reason := MergeBlockedByApproval(q, "req-1", ""); !blocked || !strings.Contains(reason, "3 approval(s)") {
		t.Errorf("requirement-wide check: %v %q", blocked, reason)
	}
	if blocked, _ := MergeBlockedByApproval(q, "req-other", ""); blocked {
		t.Error("other requirement must not block")
	}

	// Approving clears the block; rejecting also clears the BLOCK but is
	// reported by ApprovalRejected.
	if _, err := q.Resolve(a.ID, approvals.StatusApproved, "op", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Resolve(b.ID, approvals.StatusRejected, "op", "fix it"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := MergeBlockedByApproval(q, "req-1", "s1"); blocked {
		t.Error("resolved approvals must not block")
	}
	rejected, item := ApprovalRejected(q, "req-1", "s1", "")
	if !rejected || item.ID != b.ID || item.Note != "fix it" {
		t.Errorf("ApprovalRejected = %v %+v", rejected, item)
	}
	if rejected, _ := ApprovalRejected(q, "req-1", "s1", approvals.KindConflictResolution); rejected {
		t.Error("approved kind must not report rejected")
	}
	if rejected, _ := ApprovalRejected(q, "req-1", "s2", ""); rejected {
		t.Error("pending story must not report rejected")
	}
}

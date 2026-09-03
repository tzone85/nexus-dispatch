package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

func seedApprovalItem(t *testing.T, s *Server, reqID, storyID string, kind approvals.Kind, summary, details string) approvals.Item {
	t.Helper()
	q, err := approvals.Load(s.eventStore)
	if err != nil {
		t.Fatal(err)
	}
	it, err := q.Request(reqID, storyID, kind, summary, details)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestApprovalsAPI_RequiresAuthAndGet(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleApprovalsAPI(rr, httptest.NewRequest(http.MethodGet, "/api/approvals", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: status %d, want 401", rr.Code)
	}

	rr = httptest.NewRecorder()
	s.handleApprovalsAPI(rr, httptest.NewRequest(http.MethodPost, "/api/approvals?token="+s.AuthToken(), nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: status %d, want 405", rr.Code)
	}
}

func TestApprovalsAPI_ListsPendingOnly(t *testing.T) {
	s := newTestServer(t)
	a := seedApprovalItem(t, s, "req-1", "s1", approvals.KindConflictResolution, "conflict <b>x</b>", "a.go")
	b := seedApprovalItem(t, s, "req-2", "s2", approvals.KindSecurityFinding, "finding", "")
	q, _ := approvals.Load(s.eventStore)
	if _, err := q.Resolve(b.ID, approvals.StatusApproved, "op", ""); err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	s.handleApprovalsAPI(rr, httptest.NewRequest(http.MethodGet, "/api/approvals?token="+s.AuthToken(), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}
	var body struct {
		Approvals []approvals.Item `json:"approvals"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Approvals) != 1 || body.Approvals[0].ID != a.ID || body.Approvals[0].Summary != "conflict <b>x</b>" {
		t.Errorf("approvals = %+v", body.Approvals)
	}
	// JSON encoding escapes HTML-significant characters so the payload is
	// inert even if a consumer ever inlined it.
	if strings.Contains(rr.Body.String(), "<b>") {
		t.Errorf("HTML must be escaped in JSON body: %s", rr.Body.String())
	}
}

func TestApprovalsAPI_EmptyIsArrayNotNull(t *testing.T) {
	s := newTestServer(t)
	rr := httptest.NewRecorder()
	s.handleApprovalsAPI(rr, httptest.NewRequest(http.MethodGet, "/api/approvals?token="+s.AuthToken(), nil))
	if strings.TrimSpace(rr.Body.String()) != `{"approvals":[]}` {
		t.Errorf("body = %q", rr.Body.String())
	}
}

func TestApprovalsAPI_StoreError(t *testing.T) {
	s := newTestServer(t)
	// Make the event log unreadable so approvals.Load fails.
	fs := s.eventStore.(*state.FileStore)
	if err := fs.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(eventLogPath(t, s)); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	s.handleApprovalsAPI(rr, httptest.NewRequest(http.MethodGet, "/api/approvals?token="+s.AuthToken(), nil))
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rr.Code)
	}
	// The snapshot must survive the same failure with an empty panel.
	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if snap.Approvals == nil || len(snap.Approvals) != 0 {
		t.Errorf("snapshot approvals = %v, want empty slice", snap.Approvals)
	}
}

// eventLogPath finds the FileStore path from the server's state dir.
func eventLogPath(t *testing.T, s *Server) string {
	t.Helper()
	return s.stateDir + "/events.jsonl"
}

func TestSnapshot_IncludesPendingApprovals(t *testing.T) {
	s := newTestServer(t)
	a := seedApprovalItem(t, s, "req-1", "s1", approvals.KindIntegrationFailure, "build red", "undefined: Foo")
	snap, err := s.BuildSnapshot()
	if err != nil {
		t.Fatalf("BuildSnapshot: %v", err)
	}
	if len(snap.Approvals) != 1 || snap.Approvals[0].ID != a.ID || snap.Approvals[0].Details != "undefined: Foo" {
		t.Errorf("snapshot approvals = %+v", snap.Approvals)
	}
	raw, err := s.SnapshotJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"approvals":[{`) {
		t.Errorf("snapshot JSON missing approvals: %s", raw)
	}
}

func TestHandleCommand_ApproveAndRejectApproval(t *testing.T) {
	s := newTestServer(t)
	a := seedApprovalItem(t, s, "req-1", "s1", approvals.KindConflictResolution, "c", "")
	b := seedApprovalItem(t, s, "req-1", "s2", approvals.KindSecurityFinding, "f", "")

	resp := s.HandleCommand("approve_approval", mustMarshal(t, approvalPayload{ItemID: a.ID, Note: "ok by me"}))
	if !resp.Success || resp.Action != "approve_approval" || !strings.Contains(resp.Message, "approved") || !strings.Contains(resp.Message, "nxd resume req-1") {
		t.Errorf("approve resp = %+v", resp)
	}
	resp = s.HandleCommand("reject_approval", mustMarshal(t, approvalPayload{ItemID: b.ID}))
	if !resp.Success || !strings.Contains(resp.Message, "rejected") {
		t.Errorf("reject resp = %+v", resp)
	}

	q, _ := approvals.Load(s.eventStore)
	gotA, _ := q.Get(a.ID)
	gotB, _ := q.Get(b.ID)
	if gotA.Status != approvals.StatusApproved || gotA.DecidedBy != "dashboard" || gotA.Note != "ok by me" {
		t.Errorf("a = %+v", gotA)
	}
	if gotB.Status != approvals.StatusRejected || gotB.DecidedBy != "dashboard" {
		t.Errorf("b = %+v", gotB)
	}
	if len(q.Pending("")) != 0 {
		t.Error("nothing should remain pending")
	}

	// Second decision on the same item is refused.
	resp = s.HandleCommand("reject_approval", mustMarshal(t, approvalPayload{ItemID: a.ID}))
	if resp.Success || !strings.Contains(resp.Message, "already approved") {
		t.Errorf("double decision resp = %+v", resp)
	}
}

func TestHandleDecideApproval_Errors(t *testing.T) {
	s := newTestServer(t)
	cases := map[string]json.RawMessage{
		"invalid item_id":    mustMarshal(t, approvalPayload{}),
		"invalid item_id ":   json.RawMessage(`{bad json`),
		"approval not found": mustMarshal(t, approvalPayload{ItemID: "01NOPE"}),
		"note too long":      mustMarshal(t, approvalPayload{ItemID: "x", Note: strings.Repeat("n", 2001)}),
	}
	for want, payload := range cases {
		resp := s.HandleCommand("approve_approval", payload)
		if resp.Success || !strings.Contains(resp.Message, strings.TrimSpace(want)) {
			t.Errorf("payload %s: resp = %+v, want message containing %q", payload, resp, want)
		}
	}

	// Store failure surfaces as a generic error.
	a := seedApprovalItem(t, s, "req-1", "s1", approvals.KindMerge, "m", "")
	fs := s.eventStore.(*state.FileStore)
	_ = fs.Close()
	_ = os.Remove(eventLogPath(t, s))
	resp := s.HandleCommand("approve_approval", mustMarshal(t, approvalPayload{ItemID: a.ID}))
	if resp.Success || resp.Message != "store error" {
		t.Errorf("store error resp = %+v", resp)
	}
}

// The static panel must exist and wire buttons without inline handlers (CSP).
func TestStatic_ApprovalsPanelWiring(t *testing.T) {
	html, err := os.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`id="approvals"`, `id="approvals-list"`} {
		if !strings.Contains(string(html), want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	js, err := os.ReadFile("static/app.js")
	if err != nil {
		t.Fatal(err)
	}
	code := string(js)
	start := strings.Index(code, "function renderApprovals(")
	end := strings.Index(code, "function renderInvestigations(")
	if start < 0 || end < start {
		t.Fatal("renderApprovals must be defined before renderInvestigations")
	}
	panel := code[start:end]
	for _, want := range []string{
		"renderApprovals(data.approvals)",
		`sendCommand("approve_approval", { item_id: a.id, note: note.value })`,
		`sendCommand("reject_approval", { item_id: a.id, note: note.value })`,
		"addEventListener(\"click\"",
	} {
		if !strings.Contains(code, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
	for _, forbidden := range []string{"innerHTML", "onclick"} {
		if strings.Contains(panel, forbidden) {
			t.Errorf("renderApprovals must not use %s (CSP / XSS)", forbidden)
		}
	}
}

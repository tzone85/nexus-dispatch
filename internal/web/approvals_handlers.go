// internal/web/approvals_handlers.go — human approval queue on the dashboard.
package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"github.com/tzone85/nexus-dispatch/internal/approvals"
)

// approvalPayload is the WebSocket payload for approve_approval /
// reject_approval commands.
type approvalPayload struct {
	ItemID string `json:"item_id"`
	Note   string `json:"note,omitempty"`
}

// loadApprovals rebuilds the queue from the event store. The dashboard
// process is not the writer of APPROVAL_REQUESTED events (the resume loop
// is), so every read replays the log instead of caching.
func (s *Server) loadApprovals() (*approvals.Queue, error) {
	return approvals.Load(s.eventStore)
}

// pendingApprovals returns every pending item, oldest first.
func (s *Server) pendingApprovals() ([]approvals.Item, error) {
	q, err := s.loadApprovals()
	if err != nil {
		return nil, err
	}
	items := q.Pending("")
	if items == nil {
		items = []approvals.Item{}
	}
	return items, nil
}

// handleApprovalsAPI serves GET /api/approvals: the pending approval items as
// JSON. Auth-gated like every dashboard endpoint (route registered in
// server.go next to /healthz).
func (s *Server) handleApprovalsAPI(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(r) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	items, err := s.pendingApprovals()
	if err != nil {
		log.Printf("[web] load approvals: %v", err)
		http.Error(w, "approvals unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := json.NewEncoder(w).Encode(map[string]any{"approvals": items}); err != nil {
		log.Printf("[web] encode approvals: %v", err)
	}
}

// handleDecideApproval backs the approve_approval / reject_approval WebSocket
// commands. The decision is persisted as APPROVAL_RESOLVED; the caller is
// expected to `nxd resume` (or the monitor's next poll picks it up).
func (s *Server) handleDecideApproval(action string, status approvals.Status, payload json.RawMessage) WSResponse {
	fail := func(msg string) WSResponse {
		return WSResponse{Type: "command_result", Action: action, Success: false, Message: msg}
	}
	var p approvalPayload
	if err := json.Unmarshal(payload, &p); err != nil || p.ItemID == "" {
		return fail("invalid item_id")
	}
	if len(p.Note) > 2000 {
		return fail("note too long (max 2000 chars)")
	}
	q, err := s.loadApprovals()
	if err != nil {
		log.Printf("[ws] load approvals: %v", err)
		return fail("store error")
	}
	it, err := q.Resolve(p.ItemID, status, "dashboard", p.Note)
	switch {
	case errors.Is(err, approvals.ErrNotFound):
		return fail("approval not found")
	case errors.Is(err, approvals.ErrAlreadyResolved):
		return fail(fmt.Sprintf("approval already %s", it.Status))
	case err != nil:
		log.Printf("[ws] resolve approval %s: %v", p.ItemID, err)
		return fail("store error")
	}
	return WSResponse{
		Type: "command_result", Action: action, Success: true,
		Message: fmt.Sprintf("Approval %s: %s (%s) — run nxd resume %s", it.Status, it.ID, it.Kind, it.ReqID),
	}
}

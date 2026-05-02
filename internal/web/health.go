// internal/web/health.go — /healthz + /readyz endpoints.
//
// /healthz is a liveness probe: returns 200 as long as the HTTP server is
// responsive. Used by container orchestrators to decide whether to
// restart the process.
//
// /readyz is a readiness probe: returns 200 only when the dependent
// stores (event log + projection DB) are responsive. Used by load
// balancers to decide whether to send traffic.
//
// Both endpoints are intentionally unauthenticated — k8s probes don't
// carry tokens. They expose only uptime + dependency status, never
// requirement / story data.
package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// healthResponse is the JSON shape returned by both endpoints.
type healthResponse struct {
	Status        string            `json:"status"`         // "ok" | "degraded"
	UptimeSeconds int64             `json:"uptime_seconds"` // since server start
	Version       string            `json:"version,omitempty"`
	Components    map[string]string `json:"components,omitempty"` // component → "ok" | error message
}

// handleHealthz answers liveness probes. Always 200 unless the process is
// hung at the HTTP layer (in which case the probe times out).
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
	}
	writeHealth(w, http.StatusOK, resp)
}

// handleReadyz answers readiness probes by sanity-checking the stores.
// Returns 503 with degraded status when any dependency fails.
func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	resp := healthResponse{
		Status:        "ok",
		UptimeSeconds: int64(time.Since(s.startedAt).Seconds()),
		Components:    map[string]string{},
	}
	code := http.StatusOK

	// Event store: a List with Limit 1 is the cheapest non-trivial probe.
	if s.eventStore != nil {
		if _, err := s.eventStore.List(state.EventFilter{Limit: 1}); err != nil {
			resp.Components["event_store"] = "error: " + err.Error()
			resp.Status = "degraded"
			code = http.StatusServiceUnavailable
		} else {
			resp.Components["event_store"] = "ok"
		}
	}

	// Projection store: a cheap list call surfaces SQLite-locked / corrupt states.
	if s.projStore != nil {
		if _, err := s.projStore.ListRequirements(); err != nil {
			resp.Components["projection_store"] = "error: " + err.Error()
			resp.Status = "degraded"
			code = http.StatusServiceUnavailable
		} else {
			resp.Components["projection_store"] = "ok"
		}
	}

	writeHealth(w, code, resp)
}

func writeHealth(w http.ResponseWriter, code int, resp healthResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(resp)
}

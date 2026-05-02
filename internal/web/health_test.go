package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newHealthTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(dir + "/events.jsonl")
	if err != nil {
		t.Fatalf("event store: %v", err)
	}
	t.Cleanup(func() { es.Close() })

	ps, err := state.NewSQLiteStore(dir + "/proj.db")
	if err != nil {
		t.Fatalf("proj store: %v", err)
	}
	t.Cleanup(func() { ps.Close() })

	return &Server{
		eventStore: es,
		projStore:  ps,
		startedAt:  time.Now().Add(-10 * time.Second),
	}
}

func decodeHealth(t *testing.T, rr *httptest.ResponseRecorder) healthResponse {
	t.Helper()
	var got healthResponse
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return got
}

func TestHealthz_AlwaysOK(t *testing.T) {
	s := newHealthTestServer(t)
	rr := httptest.NewRecorder()
	s.handleHealthz(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	got := decodeHealth(t, rr)
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.UptimeSeconds < 9 {
		t.Errorf("uptime = %d, want >= 9", got.UptimeSeconds)
	}
}

func TestHealthz_NoAuth(t *testing.T) {
	// /healthz must work WITHOUT a token. Probes don't carry auth.
	s := newHealthTestServer(t)
	s.authToken = "secret"
	rr := httptest.NewRecorder()
	s.handleHealthz(rr, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rr.Code != http.StatusOK {
		t.Errorf("/healthz should be public, got %d", rr.Code)
	}
}

func TestReadyz_OK(t *testing.T) {
	s := newHealthTestServer(t)
	rr := httptest.NewRecorder()
	s.handleReadyz(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	got := decodeHealth(t, rr)
	if got.Status != "ok" {
		t.Errorf("status = %q, want ok", got.Status)
	}
	if got.Components["event_store"] != "ok" {
		t.Errorf("event_store component = %q, want ok", got.Components["event_store"])
	}
	if got.Components["projection_store"] != "ok" {
		t.Errorf("projection_store component = %q, want ok", got.Components["projection_store"])
	}
}

func TestReadyz_DegradedOnClosedProjStore(t *testing.T) {
	s := newHealthTestServer(t)
	// Closing the SQLite handle makes ListRequirements fail with
	// "database is closed", which the readiness probe surfaces.
	s.projStore.Close()

	rr := httptest.NewRecorder()
	s.handleReadyz(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rr.Code)
	}
	got := decodeHealth(t, rr)
	if got.Status != "degraded" {
		t.Errorf("status = %q, want degraded", got.Status)
	}
	if got.Components["projection_store"] == "ok" || got.Components["projection_store"] == "" {
		t.Errorf("projection_store component should report error, got %q", got.Components["projection_store"])
	}
}

func TestHealth_ContentTypeJSON(t *testing.T) {
	s := newHealthTestServer(t)
	for _, tc := range []struct {
		name string
		fn   http.HandlerFunc
	}{
		{"healthz", s.handleHealthz},
		{"readyz", s.handleReadyz},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tc.fn(rr, httptest.NewRequest(http.MethodGet, "/"+tc.name, nil))
			if got := rr.Header().Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if got := rr.Header().Get("Cache-Control"); got != "no-store" {
				t.Errorf("Cache-Control = %q, want no-store", got)
			}
		})
	}
}

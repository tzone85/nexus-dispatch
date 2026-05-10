package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestAuthToken_NotEmpty makes sure NewServer mints a non-empty token.
// The dashboard URL printed by `nxd dashboard --web` carries this
// token; an empty value would render the auth check trivially
// satisfied and break the cross-process CSRF protection.
func TestAuthToken_NotEmpty(t *testing.T) {
	s := newTestServer(t)
	tok := s.AuthToken()
	if tok == "" {
		t.Fatal("AuthToken returned empty string — CSRF protection broken")
	}
	// Hex-encoded 16-byte random → 32 chars.
	if len(tok) != 32 {
		t.Errorf("token length = %d, want 32 (hex of 16 bytes)", len(tok))
	}
}

// TestHub_ReturnsConfiguredHub confirms the Hub() accessor returns the
// instance NewServer wired up. Used by callers that need to attach an
// EventBus for instant push.
func TestHub_ReturnsConfiguredHub(t *testing.T) {
	s := newTestServer(t)
	if s.Hub() == nil {
		t.Fatal("Hub() returned nil; new server should always have a hub")
	}
}

// TestBindAddr_DefaultEmpty verifies the accessor reflects the bound
// address (zero before Start is called).
func TestBindAddr_DefaultEmpty(t *testing.T) {
	s := newTestServer(t)
	if s.BindAddr() != "" {
		t.Errorf("expected empty bindAddr before Start, got %q", s.BindAddr())
	}
}

// TestCheckAuth_AcceptsCorrectToken proves the constant-time
// comparison admits the right token. Without this test, a typo in
// subtle.ConstantTimeCompare's argument order would only show up at
// runtime in the dashboard.
func TestCheckAuth_AcceptsCorrectToken(t *testing.T) {
	s := newTestServer(t)
	r := buildAuthRequest(t, "/?token="+s.AuthToken())
	if !s.checkAuth(r) {
		t.Error("checkAuth rejected its own token")
	}
}

// TestCheckAuth_RejectsWrongToken locks down the negative path: an
// empty, mismatched, or truncated token must NOT pass.
func TestCheckAuth_RejectsWrongToken(t *testing.T) {
	s := newTestServer(t)
	cases := map[string]string{
		"empty":      "",
		"different":  "deadbeefdeadbeefdeadbeefdeadbeef",
		"truncated":  s.AuthToken()[:8],
		"with_extra": s.AuthToken() + "x",
	}
	for name, tok := range cases {
		t.Run(name, func(t *testing.T) {
			r := buildAuthRequest(t, "/?token="+tok)
			if s.checkAuth(r) {
				t.Errorf("checkAuth accepted bogus token %q", tok)
			}
		})
	}
}

// buildAuthRequest creates a minimal *http.Request whose URL has the
// query string parsed (checkAuth reads r.URL.Query()).
func buildAuthRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse target: %v", err)
	}
	return &http.Request{URL: u, Header: http.Header{}}
}

// TestAuthToken_StableAcrossCalls makes sure consecutive AuthToken
// reads do not refresh the value. The dashboard relies on a stable
// session token until the process exits.
func TestAuthToken_StableAcrossCalls(t *testing.T) {
	s := newTestServer(t)
	a := s.AuthToken()
	b := s.AuthToken()
	if a != b {
		t.Errorf("AuthToken changed between calls: %q vs %q", a, b)
	}
	if !strings.HasPrefix(a, b[:4]) { // sanity: same prefix
		t.Errorf("token prefix mismatch: %q vs %q", a, b)
	}
}

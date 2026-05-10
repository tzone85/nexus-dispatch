package improver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPFeed_FetchHappyPath proves the round-trip: server returns a JSON
// array of suggestions; Fetch decodes, the User-Agent is set, the Accept
// header is set, and timeout context wraps the call.
func TestHTTPFeed_FetchHappyPath(t *testing.T) {
	var gotUA, gotAccept string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]Suggestion{
			{ID: "online.test", Title: "from feed", Severity: SeverityInfo},
		})
	}))
	defer server.Close()

	feed := HTTPFeed{URL: server.URL}
	out, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if len(out) != 1 || out[0].ID != "online.test" {
		t.Fatalf("decoded suggestions wrong: %+v", out)
	}
	if !strings.Contains(gotUA, "nxd-improver") {
		t.Errorf("User-Agent = %q, want nxd-improver/*", gotUA)
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
}

// TestHTTPFeed_FetchEmptyURLNoOps confirms that omitting the URL is the
// "disabled" signal — Fetch must be a no-op (nil, nil) so the improver
// can be constructed with a zero-value HTTPFeed without making outbound
// calls. Supports the offline-first guarantee.
func TestHTTPFeed_FetchEmptyURLNoOps(t *testing.T) {
	feed := HTTPFeed{}
	out, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if out != nil {
		t.Errorf("expected nil suggestions, got %v", out)
	}
}

// TestHTTPFeed_FetchNon2xxIsError makes sure HTTP errors don't silently
// degrade to "no suggestions" — the operator should know the feed is
// misconfigured.
func TestHTTPFeed_FetchNon2xxIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	feed := HTTPFeed{URL: server.URL}
	_, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err == nil {
		t.Fatal("expected error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500, got %v", err)
	}
}

// TestHTTPFeed_FetchMalformedJSONIsError guards against the feed sending
// HTML or an unexpected envelope and the improver pretending nothing
// happened.
func TestHTTPFeed_FetchMalformedJSONIsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "<html>not json</html>")
	}))
	defer server.Close()

	feed := HTTPFeed{URL: server.URL}
	_, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err == nil {
		t.Fatal("expected JSON decode error")
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error should mention decode, got %v", err)
	}
}

// TestHTTPFeed_FetchTimeoutFires verifies the per-request timeout
// actually applies. A server that hangs longer than the configured
// timeout must produce an error rather than blocking the caller.
func TestHTTPFeed_FetchTimeoutFires(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	feed := HTTPFeed{URL: server.URL, Timeout: 50 * time.Millisecond}
	start := time.Now()
	_, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Errorf("Fetch took %s — timeout did not fire", elapsed)
	}
}

// TestHTTPFeed_FetchInvalidURLIsError exercises the request-build path.
// http.NewRequestWithContext errors on a malformed URL; we rely on that
// to surface configuration mistakes loudly.
func TestHTTPFeed_FetchInvalidURLIsError(t *testing.T) {
	feed := HTTPFeed{URL: "://not a url"}
	_, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
	if !strings.Contains(err.Error(), "build feed request") {
		t.Errorf("expected 'build feed request' in error, got %v", err)
	}
}

// TestHTTPFeed_FetchHonoursCustomClient proves that operators can
// override the http.Client (e.g. to inject a shared transport or a
// custom CA bundle) without changing the call site.
func TestHTTPFeed_FetchHonoursCustomClient(t *testing.T) {
	called := false
	rt := roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, errors.New("intercepted by custom client")
	})
	feed := HTTPFeed{URL: "http://example.invalid/", Client: &http.Client{Transport: rt}}

	_, err := feed.Fetch(context.Background(), ProjectInfo{})
	if err == nil {
		t.Fatal("expected error from custom transport")
	}
	if !called {
		t.Error("custom transport was not used")
	}
}

// roundTripperFunc lets tests pass a function as an http.RoundTripper
// without the boilerplate of declaring a struct just to attach a method.
type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

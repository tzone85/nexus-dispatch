package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// captureServer records every webhook body it receives.
type captureServer struct {
	mu     sync.Mutex
	bodies []string
	status int
}

func (c *captureServer) handler(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	c.mu.Lock()
	c.bodies = append(c.bodies, string(b))
	c.mu.Unlock()
	if c.status != 0 {
		w.WriteHeader(c.status)
	}
}

func (c *captureServer) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.bodies...)
}

func newTestNotifier(t *testing.T, opts Options) (*Notifier, *captureServer) {
	t.Helper()
	cap := &captureServer{}
	srv := httptest.NewServer(http.HandlerFunc(cap.handler))
	t.Cleanup(srv.Close)
	if opts.WebhookURL == "" {
		opts.WebhookURL = srv.URL
	}
	n := New(opts)
	n.desktopFn = func(string, string) error { return nil } // never touch osascript in tests
	return n, cap
}

func TestHandleEvent_PostsJSONWebhookForWatchedEvent(t *testing.T) {
	n, cap := newTestNotifier(t, Options{})
	evt := state.NewEvent(state.EventReqCompleted, "monitor", "", map[string]any{"id": "req-1"})

	n.HandleEvent(evt)
	n.Close()

	bodies := cap.all()
	if len(bodies) != 1 {
		t.Fatalf("want 1 webhook delivery, got %d", len(bodies))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatalf("webhook body is not JSON: %v", err)
	}
	if got["event"] != "REQ_COMPLETED" || got["source"] != "nexus-dispatch" {
		t.Errorf("unexpected envelope: %v", got)
	}
	if !strings.Contains(bodies[0], "req-1") {
		t.Errorf("payload should carry the requirement id, got %s", bodies[0])
	}
}

func TestHandleEvent_IgnoresUnwatchedEvents(t *testing.T) {
	n, cap := newTestNotifier(t, Options{})
	n.HandleEvent(state.NewEvent(state.EventStoryProgress, "a", "s", nil))
	n.HandleEvent(state.NewEvent(state.EventStoryCompleted, "a", "s", nil))
	n.Close()
	if got := cap.all(); len(got) != 0 {
		t.Fatalf("unwatched events must not notify, got %d deliveries", len(got))
	}
}

func TestHandleEvent_CustomEventListOverridesDefaults(t *testing.T) {
	n, cap := newTestNotifier(t, Options{Events: []string{"STORY_MERGED"}})
	n.HandleEvent(state.NewEvent(state.EventStoryMerged, "a", "s1", nil))
	n.HandleEvent(state.NewEvent(state.EventReqCompleted, "m", "", nil)) // default set no longer applies
	n.Close()
	bodies := cap.all()
	if len(bodies) != 1 || !strings.Contains(bodies[0], "STORY_MERGED") {
		t.Fatalf("want exactly the STORY_MERGED delivery, got %v", bodies)
	}
}

func TestHandleEvent_SlackFormat(t *testing.T) {
	n, cap := newTestNotifier(t, Options{Format: FormatSlack})
	n.HandleEvent(state.NewEvent(state.EventReqBlocked, "monitor", "", map[string]any{"id": "req-9"}))
	n.Close()

	bodies := cap.all()
	if len(bodies) != 1 {
		t.Fatalf("want 1 delivery, got %d", len(bodies))
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(bodies[0]), &got); err != nil {
		t.Fatalf("slack body is not JSON: %v", err)
	}
	text := got["text"]
	if !strings.Contains(text, "BLOCKED") || !strings.Contains(text, "req-9") {
		t.Errorf("slack text should mention the block and the req id, got %q", text)
	}
	if len(got) != 1 {
		t.Errorf("slack payload must be exactly {text}, got %v", got)
	}
}

func TestHandleEvent_WebhookFailureIsSwallowedAndLogged(t *testing.T) {
	var logged []string
	var mu sync.Mutex
	n, cap := newTestNotifier(t, Options{})
	cap.status = http.StatusInternalServerError
	n.logf = func(format string, args ...any) {
		mu.Lock()
		logged = append(logged, format)
		mu.Unlock()
	}

	// Must not panic or block the caller.
	n.HandleEvent(state.NewEvent(state.EventReqCompleted, "m", "", nil))
	n.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(logged) == 0 {
		t.Fatal("a failed delivery must be logged, not silently dropped")
	}
}

func TestHandleEvent_DesktopChannel(t *testing.T) {
	var mu sync.Mutex
	var titles []string
	n := New(Options{Desktop: true}) // no webhook URL — desktop only
	n.desktopFn = func(title, body string) error {
		mu.Lock()
		titles = append(titles, title)
		mu.Unlock()
		return nil
	}
	n.HandleEvent(state.NewEvent(state.EventReqPaused, "monitor", "", map[string]any{
		"id": "req-2", "reason": "capacity",
	}))
	n.Close()

	mu.Lock()
	defer mu.Unlock()
	if len(titles) != 1 || !strings.Contains(titles[0], "paused") {
		t.Fatalf("want one desktop notification about the pause, got %v", titles)
	}
}

func TestNew_Defaults(t *testing.T) {
	n := New(Options{})
	if n.opts.Timeout != defaultTimeout {
		t.Errorf("zero timeout should default to %v, got %v", defaultTimeout, n.opts.Timeout)
	}
	if n.opts.Format != FormatJSON {
		t.Errorf("empty format should default to json, got %q", n.opts.Format)
	}
	for _, e := range DefaultEvents() {
		if _, ok := n.watch[state.EventType(e)]; !ok {
			t.Errorf("default watch set missing %s", e)
		}
	}
}

func TestClose_DrainsInFlightDeliveries(t *testing.T) {
	slow := make(chan struct{})
	var delivered bool
	var mu sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-slow
		mu.Lock()
		delivered = true
		mu.Unlock()
	}))
	defer srv.Close()

	n := New(Options{WebhookURL: srv.URL, Timeout: 2 * time.Second})
	n.HandleEvent(state.NewEvent(state.EventReqCompleted, "m", "", nil))
	go func() {
		time.Sleep(50 * time.Millisecond)
		close(slow)
	}()
	n.Close() // must wait for the in-flight POST

	mu.Lock()
	defer mu.Unlock()
	if !delivered {
		t.Fatal("Close returned before the in-flight delivery finished")
	}
}

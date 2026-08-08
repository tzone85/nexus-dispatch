// Package notify pushes pipeline events to humans. NXD runs unattended for
// long stretches; the notifier tells the operator the moment a requirement
// completes, blocks, pauses, or trips a gate — via a webhook (generic JSON or
// Slack-compatible) and/or a macOS desktop notification — instead of them
// discovering it hours later in a terminal.
//
// Delivery is strictly best-effort and asynchronous: a slow or failing
// endpoint can never stall or fail the pipeline. Failures are logged and
// dropped.
package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// FormatJSON posts a structured JSON envelope; FormatSlack posts a
// Slack-compatible {"text": ...} payload (also accepted by Discord via
// /slack-compatible webhooks and by most chat bridges).
const (
	FormatJSON  = "json"
	FormatSlack = "slack"
)

const defaultTimeout = 5 * time.Second

// Options configures a Notifier. Zero-value fields fall back to defaults:
// empty Events watches DefaultEvents(), empty Format means FormatJSON, zero
// Timeout means 5s per delivery.
type Options struct {
	WebhookURL string
	Format     string
	Desktop    bool
	Events     []string
	Timeout    time.Duration
}

// Notifier fans events out to the configured channels. Safe for concurrent
// use; HandleEvent never blocks on delivery.
type Notifier struct {
	opts      Options
	watch     map[state.EventType]struct{}
	client    *http.Client
	desktopFn func(title, body string) error
	logf      func(format string, args ...any)
	wg        sync.WaitGroup
}

// DefaultEvents is the set of events a human almost always wants to hear
// about: terminal outcomes, pauses that need operator input, and gate trips.
func DefaultEvents() []string {
	return []string{
		string(state.EventReqCompleted),
		string(state.EventReqBlocked),
		string(state.EventReqPaused),
		string(state.EventHumanReviewNeeded),
		string(state.EventStorySecurityFailed),
		string(state.EventReqBudgetWarning),
		string(state.EventReqBudgetExceeded),
	}
}

// New builds a Notifier from opts. Delivery channels with no configuration
// (empty WebhookURL, Desktop=false) are simply skipped at send time.
func New(opts Options) *Notifier {
	if opts.Timeout <= 0 {
		opts.Timeout = defaultTimeout
	}
	if opts.Format == "" {
		opts.Format = FormatJSON
	}
	events := opts.Events
	if len(events) == 0 {
		events = DefaultEvents()
	}
	watch := make(map[state.EventType]struct{}, len(events))
	for _, e := range events {
		watch[state.EventType(strings.TrimSpace(e))] = struct{}{}
	}
	return &Notifier{
		opts:      opts,
		watch:     watch,
		client:    &http.Client{Timeout: opts.Timeout},
		desktopFn: desktopNotify,
		logf:      log.Printf,
	}
}

// HandleEvent is designed to hang off state.FileStore.OnAppend. It filters to
// the watched event set and dispatches delivery on a goroutine so the
// event-append hot path never waits on the network.
func (n *Notifier) HandleEvent(evt state.Event) {
	if _, ok := n.watch[evt.Type]; !ok {
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.deliver(evt)
	}()
}

// Close waits for in-flight deliveries to flush (bounded by the per-delivery
// HTTP timeout). Call it before process exit so a REQ_COMPLETED fired moments
// earlier is not lost.
func (n *Notifier) Close() {
	n.wg.Wait()
}

func (n *Notifier) deliver(evt state.Event) {
	title, body := Render(evt)
	if n.opts.WebhookURL != "" {
		if err := n.postWebhook(evt, title, body); err != nil {
			n.logf("[notify] webhook delivery failed for %s: %v", evt.Type, err)
		}
	}
	if n.opts.Desktop {
		if err := n.desktopFn(title, body); err != nil {
			n.logf("[notify] desktop notification failed for %s: %v", evt.Type, err)
		}
	}
}

func (n *Notifier) postWebhook(evt state.Event, title, body string) error {
	var payload any
	switch n.opts.Format {
	case FormatSlack:
		text := "*" + title + "*"
		if body != "" {
			text += "\n" + body
		}
		payload = map[string]string{"text": text}
	default: // FormatJSON
		payload = map[string]any{
			"source":    "nexus-dispatch",
			"event":     string(evt.Type),
			"title":     title,
			"body":      body,
			"story_id":  evt.StoryID,
			"agent_id":  evt.AgentID,
			"timestamp": evt.Timestamp.Format(time.RFC3339),
			"payload":   state.DecodePayload(evt.Payload),
		}
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification payload: %w", err)
	}
	resp, err := n.client.Post(n.opts.WebhookURL, "application/json", bytes.NewReader(buf))
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

// desktopNotify shows a native notification. macOS only (osascript); on other
// platforms it is a silent no-op so a shared nxd.yaml stays portable.
func desktopNotify(title, body string) error {
	if runtime.GOOS != "darwin" {
		return nil
	}
	script := fmt.Sprintf("display notification %q with title %q", body, title)
	return exec.Command("osascript", "-e", script).Run()
}

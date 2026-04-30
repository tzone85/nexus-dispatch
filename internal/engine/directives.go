package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// PendingForRuntime returns directives in the runtime.PendingDirective
// shape. Used as the runtime.DirectiveProvider adapter — the runtime
// package can't depend on engine, so engine satisfies the runtime
// interface here.
func (d *DirectiveStore) PendingForRuntime(reqID, storyID string) ([]runtime.PendingDirective, error) {
	pending, err := d.Pending(reqID, storyID)
	if err != nil {
		return nil, err
	}
	if len(pending) == 0 {
		return nil, nil
	}
	out := make([]runtime.PendingDirective, len(pending))
	for i, p := range pending {
		out[i] = runtime.PendingDirective{ID: p.ID, Instruction: p.Instruction}
	}
	return out, nil
}

// directiveAdapter wraps a *DirectiveStore so it satisfies
// runtime.DirectiveProvider without exposing the engine.Directive type
// across the package boundary.
type directiveAdapter struct{ store *DirectiveStore }

// AsRuntimeProvider returns a runtime.DirectiveProvider view of the store.
// Returns nil when store is nil so callers can chain unconditionally.
func (d *DirectiveStore) AsRuntimeProvider() runtime.DirectiveProvider {
	if d == nil {
		return nil
	}
	return &directiveAdapter{store: d}
}

func (a *directiveAdapter) Pending(reqID, storyID string) ([]runtime.PendingDirective, error) {
	return a.store.PendingForRuntime(reqID, storyID)
}

func (a *directiveAdapter) Ack(agentID, storyID string, ids []string) error {
	return a.store.Ack(agentID, storyID, ids)
}

// Directive is the canonical payload shape for USER_DIRECTIVE events.
//
// A directive is consumed by the agent that picks up the matching scope
// at its next iteration. Once consumed, the runtime emits a paired
// DIRECTIVE_ACKED event whose payload references the original directive ID
// so the dashboard can show delivery state.
type Directive struct {
	ID          string `json:"id"`
	ReqID       string `json:"req_id,omitempty"`
	StoryID     string `json:"story_id,omitempty"`
	Instruction string `json:"instruction"`
	Source      string `json:"source"` // "cli" | "dashboard" | "api"
}

// DirectiveStore reads pending directives from the event store. A
// directive is "pending" when no DIRECTIVE_ACKED event references its ID.
//
// The store is read-only (events are the source of truth); writes happen
// via state.EventStore.Append by the CLI or web layer.
type DirectiveStore struct {
	es state.EventStore
}

// NewDirectiveStore wires a store against the given event log.
func NewDirectiveStore(es state.EventStore) *DirectiveStore {
	return &DirectiveStore{es: es}
}

// Pending returns directives targeted at storyID OR at storyID's parent
// requirement (reqID), in chronological order. Acknowledged directives
// are filtered out. Empty slice when nothing is pending.
//
// scope semantics:
//   - directive with story_id == storyID         → matches
//   - directive with req_id == reqID, no story   → matches (broadcast)
//   - directive with story_id != storyID         → ignored
func (d *DirectiveStore) Pending(reqID, storyID string) ([]Directive, error) {
	if d == nil || d.es == nil {
		return nil, nil
	}
	dirEvents, err := d.es.List(state.EventFilter{Type: state.EventUserDirective})
	if err != nil {
		return nil, fmt.Errorf("list directives: %w", err)
	}
	if len(dirEvents) == 0 {
		return nil, nil
	}
	ackEvents, err := d.es.List(state.EventFilter{Type: state.EventDirectiveAcked})
	if err != nil {
		return nil, fmt.Errorf("list acks: %w", err)
	}
	acked := make(map[string]bool, len(ackEvents))
	for _, ev := range ackEvents {
		var p struct {
			DirectiveID string `json:"directive_id"`
		}
		_ = json.Unmarshal(ev.Payload, &p)
		if p.DirectiveID != "" {
			acked[p.DirectiveID] = true
		}
	}

	var out []Directive
	for _, ev := range dirEvents {
		var dir Directive
		if err := json.Unmarshal(ev.Payload, &dir); err != nil {
			continue
		}
		if dir.ID == "" {
			dir.ID = ev.ID
		}
		if acked[dir.ID] {
			continue
		}
		// Scope match.
		switch {
		case dir.StoryID != "" && dir.StoryID == storyID:
			out = append(out, dir)
		case dir.StoryID == "" && dir.ReqID != "" && dir.ReqID == reqID:
			out = append(out, dir)
		}
	}
	return out, nil
}

// Ack records that the given directive IDs have been consumed by an
// agent iteration. Emits a DIRECTIVE_ACKED event per directive so the
// dashboard / replay can match deliveries.
func (d *DirectiveStore) Ack(agentID, storyID string, ids []string) error {
	if d == nil || d.es == nil || len(ids) == 0 {
		return nil
	}
	for _, id := range ids {
		evt := state.NewEvent(state.EventDirectiveAcked, agentID, storyID, map[string]any{
			"directive_id": id,
		})
		if err := d.es.Append(evt); err != nil {
			return fmt.Errorf("append directive ack %s: %w", id, err)
		}
	}
	return nil
}

// FormatPrompt renders pending directives as a prompt addendum. Returns
// "" when empty so callers can `prompt += FormatPrompt(...)` safely.
func FormatPrompt(directives []Directive) string {
	if len(directives) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Operator directives (apply these before continuing)\n\n")
	for _, dir := range directives {
		b.WriteString("- ")
		b.WriteString(strings.TrimSpace(dir.Instruction))
		b.WriteString("\n")
	}
	return b.String()
}

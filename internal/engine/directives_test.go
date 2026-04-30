package engine

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

func newDirectiveTestStore(t *testing.T) state.EventStore {
	t.Helper()
	dir := t.TempDir()
	es, err := state.NewFileStore(filepath.Join(dir, "events.jsonl"))
	if err != nil {
		t.Fatalf("file store: %v", err)
	}
	t.Cleanup(func() { es.Close() })
	return es
}

func emitDirective(t *testing.T, es state.EventStore, reqID, storyID, instr string) string {
	t.Helper()
	evt := state.NewEvent(state.EventUserDirective, "user", storyID, map[string]any{
		"req_id":      reqID,
		"story_id":    storyID,
		"instruction": instr,
		"source":      "test",
	})
	if err := es.Append(evt); err != nil {
		t.Fatalf("append: %v", err)
	}
	return evt.ID
}

func TestDirectiveStore_StoryScope(t *testing.T) {
	es := newDirectiveTestStore(t)
	emitDirective(t, es, "req-1", "story-A", "use channels")
	emitDirective(t, es, "req-1", "story-B", "use mutex")

	store := NewDirectiveStore(es)

	gotA, err := store.Pending("req-1", "story-A")
	if err != nil {
		t.Fatal(err)
	}
	if len(gotA) != 1 || gotA[0].Instruction != "use channels" {
		t.Errorf("story-A directives = %+v", gotA)
	}

	gotB, _ := store.Pending("req-1", "story-B")
	if len(gotB) != 1 || gotB[0].Instruction != "use mutex" {
		t.Errorf("story-B directives = %+v", gotB)
	}
}

func TestDirectiveStore_ReqBroadcast(t *testing.T) {
	es := newDirectiveTestStore(t)
	emitDirective(t, es, "req-1", "", "prefer stdlib over deps")

	store := NewDirectiveStore(es)
	gotA, _ := store.Pending("req-1", "story-A")
	gotB, _ := store.Pending("req-1", "story-B")
	gotOther, _ := store.Pending("req-2", "story-C")

	if len(gotA) != 1 {
		t.Errorf("story-A should see broadcast, got %d", len(gotA))
	}
	if len(gotB) != 1 {
		t.Errorf("story-B should see broadcast, got %d", len(gotB))
	}
	if len(gotOther) != 0 {
		t.Errorf("story under different req should not see broadcast, got %d", len(gotOther))
	}
}

func TestDirectiveStore_AckSuppresses(t *testing.T) {
	es := newDirectiveTestStore(t)
	id := emitDirective(t, es, "req-1", "story-A", "no globals")

	store := NewDirectiveStore(es)
	got, _ := store.Pending("req-1", "story-A")
	if len(got) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(got))
	}

	if err := store.Ack("agent-1", "story-A", []string{id}); err != nil {
		t.Fatal(err)
	}
	got, _ = store.Pending("req-1", "story-A")
	if len(got) != 0 {
		t.Errorf("expected 0 pending after ack, got %d", len(got))
	}
}

func TestDirectiveStore_NilSafe(t *testing.T) {
	var d *DirectiveStore
	if got, err := d.Pending("r", "s"); err != nil || got != nil {
		t.Errorf("nil store should return (nil, nil), got (%v, %v)", got, err)
	}
	if err := d.Ack("a", "s", []string{"x"}); err != nil {
		t.Errorf("nil store ack should noop, got %v", err)
	}
}

func TestFormatPrompt_Empty(t *testing.T) {
	if got := FormatPrompt(nil); got != "" {
		t.Errorf("expected empty for nil, got %q", got)
	}
}

func TestFormatPrompt_Renders(t *testing.T) {
	out := FormatPrompt([]Directive{
		{Instruction: "use channels"},
		{Instruction: "  no globals  "},
	})
	if out == "" {
		t.Fatal("expected non-empty")
	}
	for _, w := range []string{"Operator directives", "use channels", "no globals"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in: %s", w, out)
		}
	}
}

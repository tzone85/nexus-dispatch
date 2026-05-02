package engine

import (
	"sort"
	"testing"
)

func TestManagerActions_DefaultsRegistered(t *testing.T) {
	got := ManagerActions()
	sort.Strings(got)
	want := []string{"escalate_to_techlead", "retry", "rewrite", "split"}
	if len(got) != len(want) {
		t.Fatalf("default actions = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("action[%d] = %q, want %q", i, got[i], w)
		}
	}
}

func TestManagerActions_RegisterAndLookup(t *testing.T) {
	defer ResetManagerActions(nil)

	called := false
	RegisterManagerAction("custom", func(ctx ManagerActionContext) {
		called = true
		if ctx.StoryID != "STORY-A" {
			t.Errorf("ctx.StoryID = %q, want STORY-A", ctx.StoryID)
		}
	})

	h := LookupManagerAction("custom")
	if h == nil {
		t.Fatal("custom handler not found after register")
	}
	h(ManagerActionContext{StoryID: "STORY-A"})
	if !called {
		t.Error("handler was not invoked")
	}
}

func TestManagerActions_ResetRestoresDefaults(t *testing.T) {
	ResetManagerActions(map[string]ManagerActionHandler{
		"only": func(ManagerActionContext) {},
	})
	if LookupManagerAction("retry") != nil {
		t.Error("expected retry handler to be cleared after Reset")
	}

	ResetManagerActions(nil)

	if LookupManagerAction("retry") == nil {
		t.Error("default retry handler not restored")
	}
}

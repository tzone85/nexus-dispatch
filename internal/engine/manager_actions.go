package engine

import (
	"context"
	"sync"
)

// ManagerActionContext bundles the inputs every manager-action handler
// needs: the Monitor that owns the projection / event stores, the
// pipeline RunContext (for split actions that have to mutate the DAG),
// and the originating story metadata. The handler picks out only what
// it needs.
type ManagerActionContext struct {
	Ctx          context.Context
	Monitor      *Monitor
	StoryID      string
	WorktreePath string
	Action       ManagerAction
	RunContext   *RunContext
	Story        PlannedStory
}

// ManagerActionHandler executes one of the actions returned by the
// Manager LLM (retry / rewrite / split / escalate_to_techlead, …) and
// emits the corresponding events. Handlers are best-effort — they do
// not return an error because the monitor cannot recover from one;
// instead, on bad input they reset the story to draft so the operator
// can intervene.
type ManagerActionHandler func(ManagerActionContext)

var (
	managerActionMu       sync.RWMutex
	managerActionHandlers map[string]ManagerActionHandler
)

func init() {
	managerActionHandlers = defaultManagerActionHandlers()
}

// defaultManagerActionHandlers returns a fresh map of the built-in
// action handlers. Returned map is owned by the caller so test code can
// mutate it without polluting the global registry.
func defaultManagerActionHandlers() map[string]ManagerActionHandler {
	return map[string]ManagerActionHandler{
		"retry": func(c ManagerActionContext) {
			c.Monitor.executeRetryAction(c.StoryID, c.Action, c.WorktreePath)
		},
		"rewrite": func(c ManagerActionContext) {
			c.Monitor.executeRewriteAction(c.StoryID, c.Action)
		},
		"split": func(c ManagerActionContext) {
			c.Monitor.executeSplitAction(c.Ctx, c.StoryID, c.Action, c.RunContext, c.Story)
		},
		"escalate_to_techlead": func(c ManagerActionContext) {
			c.Monitor.escalateToTier(c.StoryID, 3, "manager escalated: "+c.Action.Diagnosis)
		},
	}
}

// RegisterManagerAction installs / replaces a handler for the given
// action name. Use to support new manager-LLM verbs without editing the
// monitor's switch statement.
func RegisterManagerAction(name string, h ManagerActionHandler) {
	managerActionMu.Lock()
	defer managerActionMu.Unlock()
	managerActionHandlers[name] = h
}

// ResetManagerActions replaces the active handler map. Pass nil to
// restore defaults.
func ResetManagerActions(handlers map[string]ManagerActionHandler) {
	managerActionMu.Lock()
	defer managerActionMu.Unlock()
	if handlers == nil {
		managerActionHandlers = defaultManagerActionHandlers()
		return
	}
	managerActionHandlers = make(map[string]ManagerActionHandler, len(handlers))
	for k, v := range handlers {
		managerActionHandlers[k] = v
	}
}

// LookupManagerAction returns the registered handler for name, or nil.
func LookupManagerAction(name string) ManagerActionHandler {
	managerActionMu.RLock()
	defer managerActionMu.RUnlock()
	return managerActionHandlers[name]
}

// ManagerActions returns a snapshot of the registered handler names.
// Used by tests and the validator that double-checks parsed actions.
func ManagerActions() []string {
	managerActionMu.RLock()
	defer managerActionMu.RUnlock()
	out := make([]string, 0, len(managerActionHandlers))
	for k := range managerActionHandlers {
		out = append(out, k)
	}
	return out
}

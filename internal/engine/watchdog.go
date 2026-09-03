package engine

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Fingerprint captures a hash of session output at a point in time for stuck
// detection. Timestamp is when this hash was FIRST observed — it is only
// moved when the hash changes, so elapsed time keeps accumulating across
// polls while the output stays frozen.
type Fingerprint struct {
	Hash      string
	Timestamp time.Time
	// StuckReported is true once AGENT_STUCK has been emitted for the current
	// frozen-output episode; cleared when the output changes.
	StuckReported bool
}

// WatchdogConfig holds thresholds for the watchdog monitor.
type WatchdogConfig struct {
	StuckThresholdS int
}

// Watchdog monitors agent sessions for stuck states and permission prompts.
// It fingerprints pane output to detect when an agent stops making progress,
// and auto-bypasses permission prompts and plan mode.
type Watchdog struct {
	config       WatchdogConfig
	eventStore   state.EventStore
	fingerprints map[string]Fingerprint
	// now is the clock used for fingerprint timestamps; tests override it.
	now func() time.Time
}

// NewWatchdog creates a Watchdog with the given configuration and event store.
func NewWatchdog(cfg WatchdogConfig, es state.EventStore) *Watchdog {
	return &Watchdog{
		config:       cfg,
		eventStore:   es,
		fingerprints: make(map[string]Fingerprint),
		now:          time.Now,
	}
}

// AgentIdentity names the agent behind a tmux session so watchdog events can
// be attributed to a story/attempt (the controller and the dashboard key on
// story id, not session name). All fields are optional.
type AgentIdentity struct {
	AgentID   string
	StoryID   string
	AttemptID string
}

// CheckResult describes the outcome of a single watchdog check.
type CheckResult struct {
	SessionName string
	Status      runtime.AgentStatus
	Action      string // "none", "permission_bypass", "plan_escape", "stuck_detected"
	// OutputChanged is true when the pane output differs from the previous
	// poll — a cheap progress signal for agents that emit no events.
	OutputChanged bool
	// StuckFor is how long the output has been unchanged (zero when it just
	// changed or this is the first observation).
	StuckFor time.Duration
	// StuckEpisodeStarted is true on the single poll where the stuck
	// threshold is first crossed for the current frozen-output episode.
	StuckEpisodeStarted bool
}

// Check inspects a session's status and takes corrective action if needed.
// It detects permission prompts (auto-approves), plan mode (escapes), and
// stuck agents (via fingerprint comparison). Events it emits carry no
// agent/story identity; callers that know it should use CheckAgent.
func (w *Watchdog) Check(sessionName string, rt runtime.Runtime) CheckResult {
	return w.CheckAgent(sessionName, rt, AgentIdentity{})
}

// CheckAgent is Check with identity: an AGENT_STUCK emitted for the session
// is stamped with the agent, story and attempt so the controller (which
// looks up stuck state per story) and the dashboard can act on it. The event
// is emitted once per stuck episode, not once per poll.
func (w *Watchdog) CheckAgent(sessionName string, rt runtime.Runtime, id AgentIdentity) CheckResult {
	result := CheckResult{SessionName: sessionName, Action: "none"}

	status, err := rt.DetectStatus(sessionName)
	if err != nil {
		result.Status = runtime.StatusWorking
		return result
	}
	result.Status = status

	switch status {
	case runtime.StatusPermissionPrompt:
		_ = rt.SendInput(sessionName, "Y")
		result.Action = "permission_bypass"

	case runtime.StatusPlanMode:
		_ = rt.SendInput(sessionName, "Escape")
		result.Action = "plan_escape"

	case runtime.StatusTerminated, runtime.StatusDone:
		// No action needed
		return result

	case runtime.StatusWorking:
		output, err := rt.ReadOutput(sessionName, 30)
		if err != nil {
			return result
		}
		w.fingerprintWorking(sessionName, output, id, &result)
	}

	return result
}

// fingerprintWorking updates the session's output fingerprint and fills the
// stuck-related fields of result.
func (w *Watchdog) fingerprintWorking(sessionName, output string, id AgentIdentity, result *CheckResult) {
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(output)))
	now := w.now()

	prev, exists := w.fingerprints[sessionName]
	if !exists || prev.Hash != hash {
		// New baseline: the output changed (or this is the first look).
		w.fingerprints[sessionName] = Fingerprint{Hash: hash, Timestamp: now}
		result.OutputChanged = exists
		return
	}

	// Unchanged output: keep the ORIGINAL timestamp so elapsed accumulates.
	elapsed := now.Sub(prev.Timestamp)
	result.StuckFor = elapsed
	if elapsed.Seconds() < float64(w.config.StuckThresholdS) {
		return
	}

	result.Status = runtime.StatusStuck
	result.Action = "stuck_detected"
	if prev.StuckReported {
		return
	}
	result.StuckEpisodeStarted = true
	prev.StuckReported = true
	w.fingerprints[sessionName] = prev
	_ = w.eventStore.Append(state.NewEventForAttempt(state.EventAgentStuck, id.AgentID, id.StoryID, id.AttemptID, map[string]any{
		"session_name": sessionName,
		"stuck_for_s":  int(elapsed.Seconds()),
	}))
}

// ClearFingerprint removes tracked state for a session, used during cleanup.
func (w *Watchdog) ClearFingerprint(sessionName string) {
	delete(w.fingerprints, sessionName)
}

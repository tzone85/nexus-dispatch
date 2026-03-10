package engine

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/tzone85/nexus-dispatch/internal/runtime"
	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Fingerprint captures a hash of session output at a point in time for stuck
// detection.
type Fingerprint struct {
	Hash      string
	Timestamp time.Time
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
}

// NewWatchdog creates a Watchdog with the given configuration and event store.
func NewWatchdog(cfg WatchdogConfig, es state.EventStore) *Watchdog {
	return &Watchdog{
		config:       cfg,
		eventStore:   es,
		fingerprints: make(map[string]Fingerprint),
	}
}

// CheckResult describes the outcome of a single watchdog check.
type CheckResult struct {
	SessionName string
	Status      runtime.AgentStatus
	Action      string // "none", "permission_bypass", "plan_escape", "stuck_detected"
}

// Check inspects a session's status and takes corrective action if needed.
// It detects permission prompts (auto-approves), plan mode (escapes), and
// stuck agents (via fingerprint comparison).
func (w *Watchdog) Check(sessionName string, rt runtime.Runtime) CheckResult {
	result := CheckResult{SessionName: sessionName, Action: "none"}

	status, err := rt.DetectStatus(sessionName)
	if err != nil {
		result.Status = runtime.StatusWorking
		return result
	}
	result.Status = status

	switch status {
	case runtime.StatusPermissionPrompt:
		rt.SendInput(sessionName, "Y")
		result.Action = "permission_bypass"

	case runtime.StatusPlanMode:
		rt.SendInput(sessionName, "Escape")
		result.Action = "plan_escape"

	case runtime.StatusTerminated, runtime.StatusDone:
		// No action needed
		return result

	case runtime.StatusWorking:
		// Check for stuck via fingerprinting
		output, err := rt.ReadOutput(sessionName, 30)
		if err != nil {
			return result
		}
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(output)))

		prev, exists := w.fingerprints[sessionName]
		now := time.Now()
		w.fingerprints[sessionName] = Fingerprint{Hash: hash, Timestamp: now}

		if exists && prev.Hash == hash {
			elapsed := now.Sub(prev.Timestamp)
			if elapsed.Seconds() >= float64(w.config.StuckThresholdS) {
				result.Status = runtime.StatusStuck
				result.Action = "stuck_detected"
				w.eventStore.Append(state.NewEvent(state.EventAgentStuck, "", "", map[string]any{
					"session_name": sessionName,
					"stuck_for_s":  int(elapsed.Seconds()),
				}))
			}
		}
	}

	return result
}

// ClearFingerprint removes tracked state for a session, used during cleanup.
func (w *Watchdog) ClearFingerprint(sessionName string) {
	delete(w.fingerprints, sessionName)
}

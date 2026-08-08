package notify

import (
	"fmt"

	"github.com/tzone85/nexus-dispatch/internal/state"
)

// Render maps an event to a human-readable (title, body) pair. Pure — safe to
// unit test and reuse from any channel (webhook, desktop, future TUI toast).
// Unknown event types fall back to the raw type name so a new event is never
// silently dropped by the renderer.
func Render(evt state.Event) (title, body string) {
	p := state.DecodePayload(evt.Payload)

	switch evt.Type {
	case state.EventReqCompleted:
		return "NXD: requirement completed ✅", withReq(p, "All stories merged and the mainline verified green.")
	case state.EventReqBlocked:
		return "NXD: requirement BLOCKED ⛔", withReq(p,
			"Completion gate could not get the mainline green. See .nxd-fix-gaps.md, then resume with --godmode.")
	case state.EventReqPaused:
		return "NXD: requirement paused ⏸", withReq(p, payloadString(p, "reason"))
	case state.EventHumanReviewNeeded:
		b := payloadString(p, "reason")
		if d := payloadString(p, "pattern"); d != "" {
			b += " (pattern: " + d + ")"
		}
		return "NXD: human review needed 🙋", b
	case state.EventStorySecurityFailed:
		b := fmt.Sprintf("Security gate failed on story %s.", evt.StoryID)
		if s := payloadString(p, "summary"); s != "" {
			b += " " + s
		}
		return "NXD: security gate failed 🛡️", b
	case state.EventReqBudgetWarning:
		return "NXD: LLM budget warning 💸", spendLine(p, "Spend has crossed the warning threshold.")
	case state.EventReqBudgetExceeded:
		return "NXD: LLM budget EXCEEDED 💸", spendLine(p, "The requirement was paused to stop further spend.")
	default:
		return "NXD: " + string(evt.Type), payloadString(p, "reason")
	}
}

// withReq prefixes body with the requirement id when the payload carries one,
// so a notification is actionable ("nxd resume <id>") without opening a
// terminal first.
func withReq(p map[string]any, body string) string {
	id := payloadString(p, "id")
	if id == "" {
		id = payloadString(p, "req_id")
	}
	if id == "" {
		return body
	}
	if body == "" {
		return "Requirement " + id
	}
	return fmt.Sprintf("[%s] %s", id, body)
}

func spendLine(p map[string]any, suffix string) string {
	spent := payloadFloat(p, "spent_usd")
	budget := payloadFloat(p, "budget_usd")
	line := fmt.Sprintf("Spent $%.2f of $%.2f.", spent, budget)
	return withReq(p, line+" "+suffix)
}

func payloadString(p map[string]any, key string) string {
	if v, ok := p[key].(string); ok {
		return v
	}
	return ""
}

func payloadFloat(p map[string]any, key string) float64 {
	if v, ok := p[key].(float64); ok {
		return v
	}
	return 0
}

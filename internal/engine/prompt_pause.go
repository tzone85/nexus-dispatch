package engine

import (
	"fmt"
	"log"
)

// Permission prompts from CLI agents that are NOT auto-approved (host
// runtimes without sandbox.auto_approve_prompts) need a human: the watchdog
// reports the prompt once per episode (CheckResult.PromptEpisodeStarted) and
// the monitor pauses the requirement so the operator is notified
// (HUMAN_REVIEW_NEEDED + REQ_PAUSED). The agent is deliberately left running
// — it is blocked at the prompt, answering it in tmux lets it continue and
// its work still flows through the pipeline; only the next wave waits.

// pauseForPermissionPrompt pauses the story's requirement with instructions
// for answering the prompt. Called once per prompt episode.
func (m *Monitor) pauseForPermissionPrompt(sessionName string, ag ActiveAgent) {
	storyID := ag.Assignment.StoryID
	log.Printf("[monitor] %s is waiting at a permission prompt in tmux session %s (runtime %s, not auto-approved)",
		storyID, sessionName, ag.RuntimeName)
	m.pauseRequirement(storyID, fmt.Sprintf(
		"agent for %s is waiting at a permission prompt (tmux session %s); answer it with `tmux attach -t %s`, or set sandbox.auto_approve_prompts: true / run the %s runtime sandboxed to auto-approve — the agent keeps running and its work still goes through review once it continues",
		storyID, sessionName, sessionName, ag.RuntimeName))
}

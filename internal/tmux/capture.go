package tmux

import (
	"fmt"
	"strings"
)

// CapturePaneOutput returns the last N lines of visible output from the
// named session's active pane.
func CapturePaneOutput(sessionName string, lines int) (string, error) {
	if lines <= 0 {
		lines = 50
	}
	startLine := fmt.Sprintf("-%d", lines)
	out, err := output("capture-pane", "-t", sessionName, "-p", "-S", startLine)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

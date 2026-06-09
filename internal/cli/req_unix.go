//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

// applyDaemonDetach puts the forked daemon into its own session via Setsid
// so parent-shell teardown / macOS app-nap cannot terminate it.
func applyDaemonDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

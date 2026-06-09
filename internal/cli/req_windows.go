//go:build windows

package cli

import (
	"os/exec"
	"syscall"
)

// CREATE_NEW_PROCESS_GROUP (0x00000200) detaches the child from the parent's
// console process group so it survives parent shell teardown.
const createNewProcessGroup = 0x00000200

// applyDaemonDetach puts the forked daemon into its own process group so the
// parent console closing does not kill it.
func applyDaemonDetach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: createNewProcessGroup}
}

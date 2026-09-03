package cli

import (
	"fmt"
	"io"

	"github.com/tzone85/nexus-dispatch/internal/config"
	"github.com/tzone85/nexus-dispatch/internal/runtime"
)

// installSandbox configures the process-wide command sandbox from nxd.yaml
// before any agent, criteria check or investigator runs a command. The
// auto-mode fallback warning is printed to out so it lands in the operator's
// terminal, not just the log.
func installSandbox(cfg config.Config, out io.Writer) error {
	_, err := runtime.InstallSandbox(cfg.Sandbox, func(msg string) { fmt.Fprintln(out, msg) })
	if err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	return nil
}

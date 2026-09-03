package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// Defect 7: `nxd --version` printed the compiled-in placeholder because main
// never forwarded the ldflags value. SetVersion must update both the package
// variable and the cobra Version field.
func TestSetVersion_UpdatesRootAndVersionCmd(t *testing.T) {
	prev := Version()
	t.Cleanup(func() { SetVersion(prev) })

	SetVersion("1.2.3")
	if Version() != "1.2.3" || rootCmd.Version != "1.2.3" {
		t.Fatalf("Version()=%q rootCmd.Version=%q", Version(), rootCmd.Version)
	}

	var buf bytes.Buffer
	cmd := newVersionCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs(nil)
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "nxd 1.2.3" {
		t.Errorf("version cmd printed %q", buf.String())
	}

	// Empty is ignored so an un-stamped build keeps "dev".
	SetVersion("")
	if Version() != "1.2.3" {
		t.Errorf("SetVersion(\"\") must be a no-op, got %q", Version())
	}
}

func TestRootCmd_SilencesErrorsAndHasGlobalFlags(t *testing.T) {
	if !rootCmd.SilenceErrors {
		t.Error("rootCmd.SilenceErrors must be set so main prints errors exactly once")
	}
	for _, flag := range []string{"config", "state-dir"} {
		if rootCmd.PersistentFlags().Lookup(flag) == nil {
			t.Errorf("missing persistent flag --%s", flag)
		}
	}
	names := map[string]bool{}
	for _, c := range rootCmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"state", "cancel", "version"} {
		if !names[want] {
			t.Errorf("root is missing command %q", want)
		}
	}
}

func TestWaitForUpdateCheck(t *testing.T) {
	t.Cleanup(func() { updateCheckDone = nil })

	// No refresh started: returns immediately.
	updateCheckDone = nil
	waitForUpdateCheck()

	// Refresh finished: returns without waiting for the bound.
	done := make(chan struct{})
	close(done)
	updateCheckDone = done
	start := time.Now()
	waitForUpdateCheck()
	if time.Since(start) > updateCheckMaxWait {
		t.Error("should not wait when the refresh already finished")
	}

	// Refresh hung: bounded by updateCheckMaxWait.
	prevWait := updateCheckMaxWait
	updateCheckMaxWait = 20 * time.Millisecond
	t.Cleanup(func() { updateCheckMaxWait = prevWait })
	updateCheckDone = make(chan struct{}) // never closed
	start = time.Now()
	waitForUpdateCheck()
	if el := time.Since(start); el < 20*time.Millisecond || el > 2*time.Second {
		t.Errorf("bounded wait elapsed %v", el)
	}
}

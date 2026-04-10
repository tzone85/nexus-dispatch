package plugin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunPluginQACheck_Passes(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "pass.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho 'all good'\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	check := PluginQACheck{Name: "pass-check", ScriptPath: script, After: "test"}
	result := RunPluginQACheck(context.Background(), check, dir)

	if !result.Passed {
		t.Error("expected pass")
	}
	if result.Name != "pass-check" {
		t.Errorf("Name = %q, want %q", result.Name, "pass-check")
	}
	if result.Elapsed <= 0 {
		t.Error("expected positive elapsed duration")
	}
}

func TestRunPluginQACheck_Fails(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "fail.sh")
	if err := os.WriteFile(script, []byte("#!/bin/bash\necho 'vulnerability found'\nexit 1\n"), 0755); err != nil {
		t.Fatalf("failed to write script: %v", err)
	}

	check := PluginQACheck{Name: "fail-check", ScriptPath: script, After: "test"}
	result := RunPluginQACheck(context.Background(), check, dir)

	if result.Passed {
		t.Error("expected fail")
	}
	if result.Output == "" {
		t.Error("expected output")
	}
}

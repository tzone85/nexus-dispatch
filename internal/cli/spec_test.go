package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSpecInit_CreatesAllEightFiles(t *testing.T) {
	dir := t.TempDir()
	cmd := newSpecInitCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	for _, f := range specDimensionFiles {
		path := filepath.Join(dir, ".spec", f.Name)
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("missing %s: %v", f.Name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", f.Name)
		}
	}
}

func TestSpecInit_SkipsExistingWithoutForce(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".spec")
	if err := os.MkdirAll(specDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Pre-write user content.
	user := []byte("# my custom what")
	if err := os.WriteFile(filepath.Join(specDir, "what.md"), user, 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := newSpecInitCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("init: %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(specDir, "what.md"))
	if string(got) != string(user) {
		t.Errorf("user content overwritten without --force")
	}
}

func TestSpecAssemble_ConcatenatesFiles(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".spec")
	os.MkdirAll(specDir, 0o755)
	os.WriteFile(filepath.Join(specDir, "what.md"), []byte("# WHAT\n\nA test thing.\n"), 0o644)
	os.WriteFile(filepath.Join(specDir, "why.md"), []byte("# WHY\n\nBecause.\n"), 0o644)

	cmd := newSpecAssembleCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{dir})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("assemble: %v", err)
	}

	out, err := os.ReadFile(filepath.Join(dir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("AGENTS.md: %v", err)
	}
	if !strings.Contains(string(out), "A test thing.") {
		t.Errorf("AGENTS.md missing what content: %s", string(out))
	}
	if !strings.Contains(string(out), "Because.") {
		t.Errorf("AGENTS.md missing why content")
	}
}

func TestSpecValidate_FlagsPlaceholders(t *testing.T) {
	dir := t.TempDir()
	cmd := newSpecInitCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{dir})
	cmd.Execute()

	// Default templates contain TODO — validate should fail.
	v := newSpecValidateCmd()
	var vout bytes.Buffer
	v.SetOut(&vout)
	v.SetErr(&vout)
	v.SetArgs([]string{dir})
	err := v.Execute()
	if err == nil {
		t.Fatal("expected validate to fail on default templates")
	}
	if !strings.Contains(vout.String(), "TODO") {
		t.Errorf("output should list TODO placeholders, got: %s", vout.String())
	}
}

func TestSpecValidate_PassesWhenComplete(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".spec")
	os.MkdirAll(specDir, 0o755)
	for _, f := range specDimensionFiles {
		os.WriteFile(filepath.Join(specDir, f.Name), []byte("# answered\n\nAll good.\n"), 0o644)
	}

	v := newSpecValidateCmd()
	v.SetOut(new(bytes.Buffer))
	v.SetErr(new(bytes.Buffer))
	v.SetArgs([]string{dir})
	if err := v.Execute(); err != nil {
		t.Errorf("validate should pass: %v", err)
	}
}

func TestLoadSpecForRequirement_NoSpec(t *testing.T) {
	dir := t.TempDir()
	if got := LoadSpecForRequirement(dir); got != "" {
		t.Errorf("expected empty for missing .spec, got: %s", got)
	}
}

func TestLoadSpecForRequirement_WithSpec(t *testing.T) {
	dir := t.TempDir()
	specDir := filepath.Join(dir, ".spec")
	os.MkdirAll(specDir, 0o755)
	os.WriteFile(filepath.Join(specDir, "what.md"), []byte("This is a tic-tac-toe game."), 0o644)

	got := LoadSpecForRequirement(dir)
	if got == "" {
		t.Fatal("expected non-empty spec context")
	}
	if !strings.Contains(got, "tic-tac-toe") {
		t.Errorf("missing what content: %s", got)
	}
	if !strings.Contains(got, "WHAT — What the system is") {
		t.Errorf("missing dimension header: %s", got)
	}
}

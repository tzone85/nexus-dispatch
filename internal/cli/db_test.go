package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestNewDBCmd_Structure(t *testing.T) {
	cmd := newDBCmd()
	if cmd.Use != "db" {
		t.Errorf("Use = %q, want %q", cmd.Use, "db")
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}

	subNames := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subNames[sub.Name()] = true
		// Also check aliases — connect has alias "psql"
		for _, a := range sub.Aliases {
			subNames[a] = true
		}
	}

	expected := []string{"list", "connect", "sql", "schema", "delete", "gc", "ping", "template"}
	for _, name := range expected {
		if !subNames[name] {
			t.Errorf("subcommand %q not registered on 'nxd db'", name)
		}
	}
}

func TestDBCmd_Help_ContainsAllSubcommands(t *testing.T) {
	cmd := newDBCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--help"})
	// Execute returns an error for --help (pflag exits), so ignore it.
	_ = cmd.Execute()

	output := buf.String()
	for _, sub := range []string{"list", "connect", "sql", "schema", "delete", "gc", "ping", "template"} {
		if !strings.Contains(output, sub) {
			t.Errorf("expected %q in `nxd db --help` output:\n%s", sub, output)
		}
	}
}

func TestNewDBListCmd(t *testing.T) {
	cmd := newDBListCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
}

func TestNewDBConnectCmd(t *testing.T) {
	cmd := newDBConnectCmd()
	if cmd.Use != "connect <db-name>" {
		t.Errorf("Use = %q", cmd.Use)
	}
	found := false
	for _, a := range cmd.Aliases {
		if a == "psql" {
			found = true
		}
	}
	if !found {
		t.Error("connect command missing 'psql' alias")
	}
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error with 0 args")
	}
	if err := cmd.Args(cmd, []string{"mydb"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
}

func TestNewDBSQLCmd(t *testing.T) {
	cmd := newDBSQLCmd()
	if cmd.Use != "sql <db-name> <query>" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if err := cmd.Args(cmd, []string{"mydb"}); err == nil {
		t.Error("expected error with only 1 arg")
	}
	if err := cmd.Args(cmd, []string{"mydb", "SELECT 1"}); err != nil {
		t.Errorf("expected 2 args to be valid: %v", err)
	}
}

func TestNewDBSchemaCmd(t *testing.T) {
	cmd := newDBSchemaCmd()
	if cmd.Use != "schema <db-name>" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if err := cmd.Args(cmd, []string{"mydb"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
}

func TestNewDBDeleteCmd(t *testing.T) {
	cmd := newDBDeleteCmd()
	if cmd.Use != "delete <db-name>" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if cmd.Flags().Lookup("confirm") == nil {
		t.Error("flag 'confirm' not registered")
	}
	// --confirm defaults to false
	confirmFlag := cmd.Flags().Lookup("confirm")
	if confirmFlag.DefValue != "false" {
		t.Errorf("--confirm default = %q, want %q", confirmFlag.DefValue, "false")
	}
}

func TestNewDBGCCmd(t *testing.T) {
	cmd := newDBGCCmd()
	if cmd.Use != "gc" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
}

func TestNewDBPingCmd(t *testing.T) {
	cmd := newDBPingCmd()
	if cmd.Use != "ping" {
		t.Errorf("Use = %q", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
}

func TestDBDeleteCmd_RequiresConfirm(t *testing.T) {
	cmd := newDBDeleteCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	// Verify the guard is wired: flag must exist and default to false.
	f := cmd.Flags().Lookup("confirm")
	if f == nil {
		t.Fatal("--confirm flag not found")
	}
	if f.Value.String() != "false" {
		t.Errorf("--confirm should default to false, got %q", f.Value.String())
	}
}

func TestRootCmd_DBRegistered(t *testing.T) {
	found := false
	for _, c := range rootCmd.Commands() {
		if c.Name() == "db" {
			found = true
			break
		}
	}
	if !found {
		t.Error("'db' subcommand not registered on rootCmd")
	}
}

func TestDBTemplateCmd_HelpListsSubcommands(t *testing.T) {
	cmd := newDBCmd()
	cmd.SetArgs([]string{"template", "--help"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	_ = cmd.Execute()
	s := buf.String()
	for _, sub := range []string{"list", "create"} {
		if !strings.Contains(s, sub) {
			t.Errorf("expected %q in `nxd db template --help`, got:\n%s", sub, s)
		}
	}
}

func TestDBTemplateCreate_RequiresFromFlag(t *testing.T) {
	cmd := newDBCmd()
	cmd.SetArgs([]string{"template", "create", "my-template"})
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	err := cmd.Execute()
	if err == nil {
		t.Error("expected error when --from flag is missing")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("expected error to mention --from, got: %v", err)
	}
}

func TestNewDBTemplateCmd(t *testing.T) {
	cmd := newDBTemplateCmd()
	if cmd.Use != "template" {
		t.Errorf("Use = %q, want template", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}

	subNames := make(map[string]bool)
	for _, sub := range cmd.Commands() {
		subNames[sub.Name()] = true
	}

	expected := []string{"list", "create"}
	for _, name := range expected {
		if !subNames[name] {
			t.Errorf("subcommand %q not registered on 'nxd db template'", name)
		}
	}
}

func TestNewDBTemplateListCmd(t *testing.T) {
	cmd := newDBTemplateListCmd()
	if cmd.Use != "list" {
		t.Errorf("Use = %q, want list", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
}

func TestNewDBTemplateCreateCmd(t *testing.T) {
	cmd := newDBTemplateCreateCmd()
	if cmd.Use != "create <name>" {
		t.Errorf("Use = %q, want 'create <name>'", cmd.Use)
	}
	if cmd.Short == "" {
		t.Error("Short is empty")
	}
	if err := cmd.Args(cmd, []string{}); err == nil {
		t.Error("expected error with 0 args")
	}
	if err := cmd.Args(cmd, []string{"mytemplate"}); err != nil {
		t.Errorf("expected 1 arg to be valid: %v", err)
	}
	if cmd.Flags().Lookup("from") == nil {
		t.Error("flag 'from' not registered")
	}
}

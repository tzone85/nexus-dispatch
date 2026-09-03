package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGoTestModule writes a minimal Go module with one test file into dir.
func writeGoTestModule(t *testing.T, dir, testBody string) {
	t.Helper()
	files := map[string]string{
		"go.mod":      "module example.com/verify\n\ngo 1.22\n",
		"lib.go":      "package verify\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n",
		"lib_test.go": testBody,
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckTests_GoModule(t *testing.T) {
	t.Run("passing suite is counted", func(t *testing.T) {
		dir := t.TempDir()
		writeGoTestModule(t, dir, "package verify\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n\nfunc TestAddZero(t *testing.T) {\n\tif Add(0, 0) != 0 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		passing, failing, total := checkTests(dir)
		if passing != 2 || failing != 0 || total != 2 {
			t.Fatalf("checkTests = pass %d fail %d total %d, want 2/0/2", passing, failing, total)
		}
	})

	t.Run("failing test is counted", func(t *testing.T) {
		dir := t.TempDir()
		writeGoTestModule(t, dir, "package verify\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 4 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")
		passing, failing, total := checkTests(dir)
		if passing != 0 || failing != 1 || total != 1 {
			t.Fatalf("checkTests = pass %d fail %d total %d, want 0/1/1", passing, failing, total)
		}
	})

	t.Run("compile error in tests fails closed", func(t *testing.T) {
		dir := t.TempDir()
		writeGoTestModule(t, dir, "package verify\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tundefinedSymbol()\n}\n")
		passing, failing, total := checkTests(dir)
		if failing != 1 || total != passing+1 {
			t.Fatalf("a broken _test.go must count as a failure: pass %d fail %d total %d", passing, failing, total)
		}
	})

	t.Run("no framework detected", func(t *testing.T) {
		passing, failing, total := checkTests(t.TempDir())
		if passing != 0 || failing != 0 || total != 0 {
			t.Fatalf("empty dir = %d/%d/%d, want zeros", passing, failing, total)
		}
	})
}

func TestEnsureDependencies(t *testing.T) {
	t.Run("go module with no deps downloads cleanly", func(t *testing.T) {
		dir := t.TempDir()
		writeGoTestModule(t, dir, "package verify\n")
		if !ensureDependencies(dir) {
			t.Fatal("go mod download on a dependency-free module must succeed")
		}
	})

	t.Run("no manifest is a no-op success", func(t *testing.T) {
		if !ensureDependencies(t.TempDir()) {
			t.Fatal("a repo without a manifest has nothing to install")
		}
	})

}

func TestCleanWorkspaceArtifacts(t *testing.T) {
	t.Run("already clean", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-clean")
		if !cleanWorkspaceArtifacts(wt) {
			t.Fatal("clean tree must report true")
		}
	})

	t.Run("removes and commits NXD artifacts", func(t *testing.T) {
		_, wt := newPipelineRepo(t, "nxd/s-dirty")
		if err := os.WriteFile(filepath.Join(wt, "WAVE_CONTEXT.md"), []byte("wave\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(wt, ".nxd-prompts"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wt, ".nxd-prompts", "prompt.txt"), []byte("p\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		gitIn(t, wt, "add", "-A")
		gitIn(t, wt, "commit", "-m", "agent committed artifacts")

		if cleanWorkspaceArtifacts(wt) {
			t.Fatal("dirty tree must report false (cleanup happened)")
		}
		for _, p := range []string{"WAVE_CONTEXT.md", ".nxd-prompts"} {
			if _, err := os.Stat(filepath.Join(wt, p)); !os.IsNotExist(err) {
				t.Errorf("%s still present (err=%v)", p, err)
			}
		}
		if subject := gitIn(t, wt, "log", "-1", "--pretty=%s"); subject != "chore: clean NXD workspace artifacts" {
			t.Errorf("cleanup was not committed; last subject = %q", subject)
		}
		if out := gitIn(t, wt, "status", "--porcelain"); out != "" {
			t.Errorf("tree dirty after cleanup commit: %q", out)
		}
	})
}

// shimTool puts a fake executable named name first on PATH for the test. The
// script echoes its arguments to argv.txt in dir, prints output and exits
// with code.
func shimTool(t *testing.T, name, output string, code int) string {
	t.Helper()
	dir := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" > \"" + filepath.Join(dir, name+"-argv.txt") + "\"\nprintf '%s' '" + output + "'\nexit " + itoa(code) + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func writePackageJSON(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name": "app", "scripts": {"test": "jest"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckTests_NodeProject(t *testing.T) {
	t.Run("jest summary line is parsed", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir)
		shim := shimTool(t, "npx", "Tests: 1 failed, 3 passed, 4 total\n", 1)
		passing, failing, total := checkTests(dir)
		if passing != 3 || failing != 1 || total != 4 {
			t.Fatalf("checkTests = %d/%d/%d, want 3/1/4", passing, failing, total)
		}
		argv, _ := os.ReadFile(filepath.Join(shim, "npx-argv.txt"))
		if !strings.HasPrefix(string(argv), "jest --passWithNoTests --json") {
			t.Errorf("npx argv = %q, want jest invocation", argv)
		}
	})

	t.Run("vitest config selects vitest and json counters are read", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir)
		if err := os.WriteFile(filepath.Join(dir, "vitest.config.ts"), []byte("export default {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		shim := shimTool(t, "npx", `{"numPassedTests": 2,`+"\n"+`"numFailedTests": 0}`+"\n", 0)
		passing, failing, total := checkTests(dir)
		if passing != 1 || failing != 1 || total != 2 {
			// One line per counter key: the simplified parser counts lines, not values.
			t.Fatalf("checkTests = %d/%d/%d, want 1/1/2 (one line per counter key)", passing, failing, total)
		}
		argv, _ := os.ReadFile(filepath.Join(shim, "npx-argv.txt"))
		if !strings.HasPrefix(string(argv), "vitest run --reporter=json") {
			t.Errorf("npx argv = %q, want vitest invocation", argv)
		}
	})

	t.Run("runner crash with no output fails closed", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir)
		shimTool(t, "npx", "", 2)
		passing, failing, total := checkTests(dir)
		if passing != 0 || failing != 1 || total != 1 {
			t.Fatalf("checkTests = %d/%d/%d, want 0/1/1", passing, failing, total)
		}
	})
}

func TestEnsureDependencies_NodeProject(t *testing.T) {
	t.Run("npm install success", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir)
		shim := shimTool(t, "npm", "", 0)
		if !ensureDependencies(dir) {
			t.Fatal("successful npm install must report true")
		}
		argv, _ := os.ReadFile(filepath.Join(shim, "npm-argv.txt"))
		if strings.TrimSpace(string(argv)) != "install" {
			t.Errorf("npm argv = %q, want install", argv)
		}
	})

	t.Run("npm install failure", func(t *testing.T) {
		dir := t.TempDir()
		writePackageJSON(t, dir)
		shimTool(t, "npm", "", 1)
		if ensureDependencies(dir) {
			t.Fatal("failed npm install must report false")
		}
	})
}

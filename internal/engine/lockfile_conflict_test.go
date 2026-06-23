package engine

import "testing"

func TestIsGeneratedLockFile(t *testing.T) {
	lock := []string{
		"package-lock.json", "path/to/package-lock.json", "yarn.lock",
		"pnpm-lock.yaml", "go.sum", "Cargo.lock", "composer.lock",
		"Gemfile.lock", "poetry.lock", "Pipfile.lock", "npm-shrinkwrap.json",
	}
	for _, f := range lock {
		if !isGeneratedLockFile(f) {
			t.Errorf("%q should be treated as a generated lock file", f)
		}
	}
	notLock := []string{
		"package.json", "main.go", "src/index.ts", "tsconfig.json",
		"vitest.config.ts", ".gitignore", "lock.go", "README.md",
	}
	for _, f := range notLock {
		if isGeneratedLockFile(f) {
			t.Errorf("%q should NOT be treated as a generated lock file", f)
		}
	}
}

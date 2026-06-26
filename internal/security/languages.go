package security

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DetectLanguages inspects repoDir for manifest files and source extensions and
// returns the set of languages present (sorted, unique). It is best-effort and
// shallow: it reads the top-level manifests and walks a bounded number of files
// so a huge repo doesn't stall the scan.
func DetectLanguages(repoDir string) []string {
	set := map[string]bool{}

	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(repoDir, name))
		return err == nil
	}

	// Manifests are the strongest signal.
	if exists("go.mod") {
		set["go"] = true
	}
	if exists("Cargo.toml") {
		set["rust"] = true
	}
	if exists("composer.json") {
		set["php"] = true
	}
	if exists("Gemfile") {
		set["ruby"] = true
	}
	if exists("requirements.txt") || exists("pyproject.toml") || exists("setup.py") || exists("setup.cfg") || exists("Pipfile") {
		set["python"] = true
	}
	if exists("package.json") {
		// tsconfig.json (or any .ts in the tree) ⇒ typescript, else javascript.
		if exists("tsconfig.json") {
			set["typescript"] = true
		} else {
			set["javascript"] = true
		}
	}

	// Extension fallback — walk a bounded slice of the tree for languages a
	// manifest didn't already establish.
	extLang := map[string]string{
		".go": "go", ".rs": "rust", ".php": "php", ".rb": "ruby",
		".py": "python", ".ts": "typescript", ".tsx": "typescript",
		".js": "javascript", ".jsx": "javascript", ".sh": "shell",
		".java": "java", ".kt": "kotlin", ".c": "c", ".cpp": "cpp", ".cc": "cpp",
	}
	const maxFiles = 4000
	count := 0
	_ = filepath.WalkDir(repoDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || count >= maxFiles {
			return nil
		}
		if d.IsDir() {
			base := d.Name()
			if base == ".git" || base == "node_modules" || base == "vendor" ||
				base == "target" || base == "dist" || base == "build" || base == ".venv" {
				return filepath.SkipDir
			}
			return nil
		}
		count++
		if lang, ok := extLang[strings.ToLower(filepath.Ext(path))]; ok {
			// Respect the typescript/javascript distinction already set by manifest.
			if lang == "javascript" && set["typescript"] {
				return nil
			}
			set[lang] = true
		}
		return nil
	})

	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	sort.Strings(out)
	return out
}

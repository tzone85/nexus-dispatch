package repolearn

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

// sourceExtensions maps file extensions to their language name.
var sourceExtensions = map[string]string{
	".go":    "go",
	".py":    "python",
	".ts":    "typescript",
	".tsx":   "typescript",
	".js":    "javascript",
	".jsx":   "javascript",
	".rs":    "rust",
	".java":  "java",
	".kt":    "kotlin",
	".rb":    "ruby",
	".php":   "php",
	".swift": "swift",
	".c":     "c",
	".cpp":   "cpp",
	".cs":    "csharp",
	".scala": "scala",
	".ex":    "elixir",
	".exs":   "elixir",
}

// ScanStatic performs Pass 1: a filesystem-only scan of the repository.
// It detects tech stack, build/lint/test commands, directory structure,
// CI configuration, entry points, dependencies, and noteworthy signals.
// No git commands or LLM calls are made.
func ScanStatic(repoPath string) (*RepoProfile, error) {
	profile := &RepoProfile{RepoPath: repoPath}

	// 1. Count files by extension to determine primary/secondary languages
	langCounts := countFilesByLanguage(repoPath)
	profile.TechStack = detectTechStack(repoPath, langCounts)

	// 2. Detect build, lint, test commands from config files
	profile.Build = detectBuildConfig(repoPath, profile.TechStack.PrimaryLanguage)
	profile.Test = detectTestConfig(repoPath, profile.TechStack.PrimaryLanguage)

	// 3. Detect CI system
	profile.CI = detectCI(repoPath)

	// 4. Scan directory structure
	profile.Structure = scanStructure(repoPath, langCounts)

	// 5. Parse dependencies
	profile.Dependencies = parseDependencies(repoPath, profile.TechStack.PrimaryLanguage)

	// 6. Detect notable signals
	detectSignals(profile, repoPath)

	profile.MarkPass(1)
	return profile, nil
}

// countFilesByLanguage walks the repo and counts source files per language.
// It skips hidden directories, vendor, node_modules, and other non-source dirs.
func countFilesByLanguage(repoPath string) map[string]int {
	counts := make(map[string]int)
	totalFiles := 0

	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			name := info.Name()
			if shouldSkipDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		totalFiles++
		ext := strings.ToLower(filepath.Ext(info.Name()))
		if lang, ok := sourceExtensions[ext]; ok {
			counts[lang]++
		}
		return nil
	})

	// Store total for later use
	counts["_total"] = totalFiles
	return counts
}

// shouldSkipDir returns true for directories that should not be scanned.
func shouldSkipDir(name string) bool {
	skip := map[string]bool{
		".git": true, ".hg": true, ".svn": true,
		"vendor": true, "node_modules": true, ".venv": true,
		"venv": true, "__pycache__": true, ".tox": true,
		"dist": true, "build": true, ".next": true,
		".nuxt": true, "target": true, ".gradle": true,
		".idea": true, ".vscode": true, ".DS_Store": true,
	}
	return skip[name] || strings.HasPrefix(name, ".")
}

// detectTechStack determines the primary and secondary languages, build tool,
// framework, and language version from marker files and file counts.
func detectTechStack(repoPath string, langCounts map[string]int) TechStackDetail {
	stack := TechStackDetail{}

	// Find primary language by file count (excluding "_total")
	maxCount := 0
	for lang, count := range langCounts {
		if lang == "_total" {
			continue
		}
		if count > maxCount {
			maxCount = count
			stack.PrimaryLanguage = lang
		}
	}

	// Collect secondary languages (>5% of source files, not primary)
	sourceTotal := 0
	for lang, count := range langCounts {
		if lang != "_total" {
			sourceTotal += count
		}
	}
	if sourceTotal > 0 {
		for lang, count := range langCounts {
			if lang == "_total" || lang == stack.PrimaryLanguage {
				continue
			}
			if float64(count)/float64(sourceTotal) > 0.05 {
				stack.SecondaryLanguages = append(stack.SecondaryLanguages, lang)
			}
		}
		sort.Strings(stack.SecondaryLanguages)
	}

	// Detect build tool and version from marker files
	type markerDetection struct {
		file      string
		language  string // override primary if set and primary is empty
		buildTool string
		framework func(repoPath string) string
		version   func(repoPath string) string
	}

	markers := []markerDetection{
		{"go.mod", "go", "go", nil, extractGoVersion},
		{"Cargo.toml", "rust", "cargo", nil, extractCargoVersion},
		{"pom.xml", "java", "maven", nil, nil},
		{"build.gradle", "java", "gradle", nil, nil},
		{"build.gradle.kts", "kotlin", "gradle", nil, nil},
		{"pyproject.toml", "python", "poetry", detectPythonFramework, extractPythonVersion},
		{"setup.py", "python", "setuptools", detectPythonFramework, nil},
		{"requirements.txt", "python", "pip", detectPythonFramework, nil},
		{"Pipfile", "python", "pipenv", detectPythonFramework, nil},
		{"package.json", "javascript", "npm", detectJSFramework, extractNodeVersion},
		{"tsconfig.json", "typescript", "npm", detectJSFramework, extractNodeVersion},
		{"Gemfile", "ruby", "bundler", detectRubyFramework, nil},
		{"mix.exs", "elixir", "mix", nil, nil},
	}

	for _, m := range markers {
		if _, err := os.Stat(filepath.Join(repoPath, m.file)); err == nil {
			if stack.PrimaryLanguage == "" {
				stack.PrimaryLanguage = m.language
			}
			if stack.PrimaryBuildTool == "" || m.language == stack.PrimaryLanguage {
				stack.PrimaryBuildTool = m.buildTool
			}
			if m.framework != nil && stack.PrimaryFramework == "" {
				stack.PrimaryFramework = m.framework(repoPath)
			}
			if m.version != nil && stack.LanguageVersion == "" {
				stack.LanguageVersion = m.version(repoPath)
			}
		}
	}

	// Override build tool with yarn/pnpm if lock file exists
	if stack.PrimaryBuildTool == "npm" {
		if _, err := os.Stat(filepath.Join(repoPath, "yarn.lock")); err == nil {
			stack.PrimaryBuildTool = "yarn"
		}
		if _, err := os.Stat(filepath.Join(repoPath, "pnpm-lock.yaml")); err == nil {
			stack.PrimaryBuildTool = "pnpm"
		}
	}

	return stack
}

// detectBuildConfig extracts build and lint commands from config files.
func detectBuildConfig(repoPath, primaryLang string) BuildConfig {
	bc := BuildConfig{}

	// Parse Makefile targets
	bc.MakeTargets = parseMakefileTargets(repoPath)
	if len(bc.MakeTargets) > 0 {
		// Infer build command from Makefile
		for _, t := range bc.MakeTargets {
			switch t {
			case "build":
				bc.BuildCommand = "make build"
			case "lint":
				bc.LintCommand = "make lint"
			case "fmt", "format":
				bc.FormatCommand = "make " + t
			}
		}
	}

	// Language-specific defaults if Makefile didn't provide them
	switch primaryLang {
	case "go":
		if bc.BuildCommand == "" {
			bc.BuildCommand = "go build ./..."
		}
		if bc.LintCommand == "" {
			if _, err := os.Stat(filepath.Join(repoPath, ".golangci.yml")); err == nil {
				bc.LintCommand = "golangci-lint run ./..."
			} else if _, err := os.Stat(filepath.Join(repoPath, ".golangci.yaml")); err == nil {
				bc.LintCommand = "golangci-lint run ./..."
			} else {
				bc.LintCommand = "go vet ./..."
			}
		}
		if bc.FormatCommand == "" {
			bc.FormatCommand = "gofmt -w ."
		}
	case "python":
		if bc.LintCommand == "" {
			for _, linter := range []struct{ file, cmd string }{
				{"ruff.toml", "ruff check ."},
				{".flake8", "flake8 ."},
				{"setup.cfg", "flake8 ."},
				{"pyproject.toml", "ruff check ."},
			} {
				if _, err := os.Stat(filepath.Join(repoPath, linter.file)); err == nil {
					bc.LintCommand = linter.cmd
					break
				}
			}
		}
	case "javascript", "typescript":
		bc = detectNPMBuildConfig(repoPath, bc)
	case "rust":
		if bc.BuildCommand == "" {
			bc.BuildCommand = "cargo build"
		}
		if bc.LintCommand == "" {
			bc.LintCommand = "cargo clippy"
		}
		if bc.FormatCommand == "" {
			bc.FormatCommand = "cargo fmt"
		}
	}

	return bc
}

// detectTestConfig extracts test commands, framework, and conventions.
func detectTestConfig(repoPath, primaryLang string) TestConfig {
	tc := TestConfig{}

	// Check Makefile for test target first
	targets := parseMakefileTargets(repoPath)
	for _, t := range targets {
		if t == "test" {
			tc.TestCommand = "make test"
			break
		}
	}

	switch primaryLang {
	case "go":
		if tc.TestCommand == "" {
			tc.TestCommand = "go test ./..."
		}
		tc.TestFramework = "go test"
		tc.CoverageTool = "go tool cover"
		tc.TestFilePattern = "*_test.go"
	case "python":
		tc.TestFilePattern = "test_*.py"
		if _, err := os.Stat(filepath.Join(repoPath, "pytest.ini")); err == nil {
			tc.TestFramework = "pytest"
			if tc.TestCommand == "" {
				tc.TestCommand = "pytest"
			}
		} else if _, err := os.Stat(filepath.Join(repoPath, "pyproject.toml")); err == nil {
			// Check pyproject.toml for pytest config
			if content, err := os.ReadFile(filepath.Join(repoPath, "pyproject.toml")); err == nil {
				if strings.Contains(string(content), "[tool.pytest") {
					tc.TestFramework = "pytest"
					if tc.TestCommand == "" {
						tc.TestCommand = "pytest"
					}
				}
			}
		}
		if tc.TestFramework == "" {
			tc.TestFramework = "unittest"
			if tc.TestCommand == "" {
				tc.TestCommand = "python -m pytest"
			}
		}
	case "javascript", "typescript":
		tc = detectNPMTestConfig(repoPath, tc)
	case "rust":
		if tc.TestCommand == "" {
			tc.TestCommand = "cargo test"
		}
		tc.TestFramework = "cargo test"
		tc.TestFilePattern = "*_test.rs"
	case "ruby":
		if _, err := os.Stat(filepath.Join(repoPath, "spec")); err == nil {
			tc.TestFramework = "rspec"
			tc.TestFilePattern = "*_spec.rb"
			tc.TestDirs = []string{"spec/"}
			if tc.TestCommand == "" {
				tc.TestCommand = "bundle exec rspec"
			}
		} else {
			tc.TestFramework = "minitest"
			tc.TestFilePattern = "test_*.rb"
			tc.TestDirs = []string{"test/"}
			if tc.TestCommand == "" {
				tc.TestCommand = "bundle exec rake test"
			}
		}
	case "java":
		tc.TestFramework = "junit"
		tc.TestFilePattern = "*Test.java"
		tc.TestDirs = []string{"src/test/"}
	}

	// Detect test directories
	if len(tc.TestDirs) == 0 {
		for _, dir := range []string{"test", "tests", "spec", "__tests__", "test_data", "testdata"} {
			if info, err := os.Stat(filepath.Join(repoPath, dir)); err == nil && info.IsDir() {
				tc.TestDirs = append(tc.TestDirs, dir+"/")
			}
		}
	}

	return tc
}

// detectCI identifies the CI/CD system from well-known config file locations.
func detectCI(repoPath string) CIConfig {
	ci := CIConfig{}

	type ciMarker struct {
		path   string
		system string
	}

	markers := []ciMarker{
		{".github/workflows", "github_actions"},
		{".gitlab-ci.yml", "gitlab_ci"},
		{".circleci/config.yml", "circleci"},
		{"Jenkinsfile", "jenkins"},
		{".travis.yml", "travis"},
		{"azure-pipelines.yml", "azure_devops"},
		{"bitbucket-pipelines.yml", "bitbucket"},
		{".drone.yml", "drone"},
		{"cloudbuild.yaml", "google_cloud_build"},
		{"buildkite.yml", "buildkite"},
		{".buildkite/pipeline.yml", "buildkite"},
	}

	for _, m := range markers {
		fullPath := filepath.Join(repoPath, m.path)
		if _, err := os.Stat(fullPath); err == nil {
			ci.System = m.system
			// For GitHub Actions, list workflow files
			if m.system == "github_actions" {
				entries, _ := os.ReadDir(fullPath)
				for _, e := range entries {
					if !e.IsDir() && (strings.HasSuffix(e.Name(), ".yml") || strings.HasSuffix(e.Name(), ".yaml")) {
						ci.Files = append(ci.Files, filepath.Join(".github/workflows", e.Name()))
					}
				}
			} else {
				ci.Files = []string{m.path}
			}
			break
		}
	}

	return ci
}

// scanStructure analyses the top-level directory layout and detects entry points.
func scanStructure(repoPath string, langCounts map[string]int) RepoStructure {
	rs := RepoStructure{
		TotalFiles: langCounts["_total"],
	}

	// Count source files
	for lang, count := range langCounts {
		if lang != "_total" {
			rs.SourceFiles += count
		}
	}

	// Classify top-level directories
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return rs
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		name := e.Name()
		files := countFilesInDir(filepath.Join(repoPath, name))
		purpose := classifyDir(name)
		rs.TopDirs = append(rs.TopDirs, DirInfo{
			Name:    name,
			Purpose: purpose,
			Files:   files,
		})
	}

	// Detect entry points
	rs.EntryPoints = detectEntryPoints(repoPath)

	return rs
}

// classifyDir infers the purpose of a top-level directory from its name.
func classifyDir(name string) string {
	lower := strings.ToLower(name)
	switch {
	case lower == "cmd" || lower == "bin":
		return "commands"
	case lower == "internal" || lower == "pkg" || lower == "lib" || lower == "src":
		return "source"
	case lower == "test" || lower == "tests" || lower == "spec" || lower == "__tests__":
		return "test"
	case lower == "docs" || lower == "doc" || lower == "documentation":
		return "docs"
	case lower == "vendor" || lower == "third_party":
		return "vendor"
	case lower == "scripts" || lower == "tools" || lower == "hack":
		return "scripts"
	case lower == "config" || lower == "configs" || lower == "conf":
		return "config"
	case lower == "migrations" || lower == "db":
		return "database"
	case lower == "api" || lower == "proto" || lower == "graphql":
		return "api"
	case lower == "web" || lower == "static" || lower == "public" || lower == "assets":
		return "web"
	case lower == "deploy" || lower == "infra" || lower == "terraform" || lower == "k8s" || lower == "helm":
		return "infrastructure"
	case lower == "examples" || lower == "samples":
		return "examples"
	case lower == "build" || lower == "dist" || lower == "out":
		return "build"
	case lower == "generated" || lower == "gen":
		return "generated"
	default:
		return "source"
	}
}

// detectEntryPoints finds main packages, executable entry points, and cmd/ subdirs.
func detectEntryPoints(repoPath string) []EntryPoint {
	var eps []EntryPoint

	// Go: check cmd/ subdirectories and main.go
	cmdDir := filepath.Join(repoPath, "cmd")
	if entries, err := os.ReadDir(cmdDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				mainFile := filepath.Join("cmd", e.Name(), "main.go")
				if _, err := os.Stat(filepath.Join(repoPath, mainFile)); err == nil {
					eps = append(eps, EntryPoint{Path: mainFile, Kind: "cmd"})
				}
			}
		}
	}
	if _, err := os.Stat(filepath.Join(repoPath, "main.go")); err == nil {
		eps = append(eps, EntryPoint{Path: "main.go", Kind: "main"})
	}

	// Python: look for common entry points
	for _, candidate := range []string{"main.py", "app.py", "manage.py", "wsgi.py", "asgi.py"} {
		if _, err := os.Stat(filepath.Join(repoPath, candidate)); err == nil {
			kind := "main"
			if candidate == "manage.py" {
				kind = "cmd"
			} else if candidate == "wsgi.py" || candidate == "asgi.py" {
				kind = "handler"
			}
			eps = append(eps, EntryPoint{Path: candidate, Kind: kind})
		}
	}

	// Node.js: check package.json main/bin fields
	pkgPath := filepath.Join(repoPath, "package.json")
	if data, err := os.ReadFile(pkgPath); err == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			if main, ok := pkg["main"].(string); ok && main != "" {
				eps = append(eps, EntryPoint{Path: main, Kind: "main"})
			}
		}
	}

	// Rust: src/main.rs
	if _, err := os.Stat(filepath.Join(repoPath, "src", "main.rs")); err == nil {
		eps = append(eps, EntryPoint{Path: "src/main.rs", Kind: "main"})
	}
	if _, err := os.Stat(filepath.Join(repoPath, "src", "lib.rs")); err == nil {
		eps = append(eps, EntryPoint{Path: "src/lib.rs", Kind: "main"})
	}

	return eps
}

// parseDependencies reads dependencies from the project's dependency file.
func parseDependencies(repoPath, primaryLang string) []Dependency {
	switch primaryLang {
	case "go":
		return parseGoDependencies(repoPath)
	case "python":
		return parsePythonDependencies(repoPath)
	case "javascript", "typescript":
		return parseNPMDependencies(repoPath)
	case "rust":
		return parseRustDependencies(repoPath)
	default:
		return nil
	}
}

// detectSignals identifies noteworthy patterns in the repository.
func detectSignals(profile *RepoProfile, repoPath string) {
	// Monorepo detection: multiple go.mod or package.json files
	goModCount := 0
	pkgJSONCount := 0
	filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			if info != nil && info.IsDir() && shouldSkipDir(info.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		switch info.Name() {
		case "go.mod":
			goModCount++
		case "package.json":
			pkgJSONCount++
		}
		if goModCount > 1 || pkgJSONCount > 3 {
			return filepath.SkipAll
		}
		return nil
	})
	if goModCount > 1 || pkgJSONCount > 3 {
		profile.AddSignal("monorepo", "Multiple module/package roots detected — this may be a monorepo", "")
	}

	// Vendored dependencies
	if _, err := os.Stat(filepath.Join(repoPath, "vendor")); err == nil {
		profile.AddSignal("vendored", "Vendor directory present — dependencies are vendored", "vendor/")
	}

	// No test files detected
	hasTests := false
	for _, dir := range profile.Test.TestDirs {
		if _, err := os.Stat(filepath.Join(repoPath, strings.TrimSuffix(dir, "/"))); err == nil {
			hasTests = true
			break
		}
	}
	if !hasTests && profile.Test.TestFilePattern != "" {
		// Walk for test files
		filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				if info != nil && info.IsDir() && shouldSkipDir(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			matched, _ := filepath.Match(profile.Test.TestFilePattern, info.Name())
			if matched {
				hasTests = true
				return filepath.SkipAll
			}
			return nil
		})
	}
	if !hasTests {
		profile.AddSignal("no_tests", "No test files detected — agents should add tests for any changes", "")
	}

	// Docker present
	if _, err := os.Stat(filepath.Join(repoPath, "Dockerfile")); err == nil {
		profile.AddSignal("docker", "Dockerfile present", "Dockerfile")
	}
	if _, err := os.Stat(filepath.Join(repoPath, "docker-compose.yml")); err == nil {
		profile.AddSignal("docker_compose", "Docker Compose configuration present", "docker-compose.yml")
	}
	if _, err := os.Stat(filepath.Join(repoPath, "compose.yml")); err == nil {
		profile.AddSignal("docker_compose", "Docker Compose configuration present", "compose.yml")
	}

	// Generated code markers
	for _, gen := range []string{".proto", ".swagger.json", ".openapi.json", ".graphql"} {
		filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				if info != nil && info.IsDir() && shouldSkipDir(info.Name()) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasSuffix(info.Name(), gen) {
				rel, _ := filepath.Rel(repoPath, path)
				profile.AddSignal("generated_code", "Schema/proto file found — code may be auto-generated", rel)
				return filepath.SkipAll
			}
			return nil
		})
	}

	// Code review graph: if .code-review-graph/graph.db exists, read stats
	detectCodeGraphSignals(profile, repoPath)
}

// detectCodeGraphSignals checks for an existing code-review-graph database
// and adds graph statistics as signals to the profile.
func detectCodeGraphSignals(profile *RepoProfile, repoPath string) {
	dbPath := filepath.Join(repoPath, ".code-review-graph", "graph.db")
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		return
	}
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return
	}
	defer db.Close()

	var nodeCount, edgeCount, fileCount int
	if err := db.QueryRow("SELECT COUNT(*) FROM nodes").Scan(&nodeCount); err != nil {
		return
	}
	db.QueryRow("SELECT COUNT(*) FROM edges").Scan(&edgeCount)
	db.QueryRow("SELECT COUNT(DISTINCT file_path) FROM nodes").Scan(&fileCount)

	var langs []string
	rows, err := db.Query("SELECT DISTINCT language FROM nodes WHERE language IS NOT NULL AND language != ''")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var lang string
			if rows.Scan(&lang) == nil {
				langs = append(langs, lang)
			}
		}
	}

	msg := fmt.Sprintf("Code graph: %d nodes, %d edges across %d files", nodeCount, edgeCount, fileCount)
	if len(langs) > 0 {
		msg += fmt.Sprintf(" (%s)", strings.Join(langs, ", "))
	}
	profile.AddSignal("codegraph_stats", msg, ".code-review-graph/graph.db")
}

// --------------------------------------------------------------------------
// Helper functions for parsing specific config file formats
// --------------------------------------------------------------------------

// parseMakefileTargets extracts target names from a Makefile.
// Package-level regexes — compiled once at init, reused across calls.
// Hoisting these out of per-call hot paths matters because ScanRepo
// can be invoked many times during a single planning + dispatch cycle.
var (
	makefileTargetRe = regexp.MustCompile(`^([a-zA-Z_][a-zA-Z0-9_-]*)\s*:`)
	goModVersionRe   = regexp.MustCompile(`(?m)^go\s+(\d+\.\d+(?:\.\d+)?)`)
	cargoRustRe      = regexp.MustCompile(`(?m)^rust-version\s*=\s*"([^"]+)"`)
	cargoEditionRe   = regexp.MustCompile(`(?m)^edition\s*=\s*"([^"]+)"`)
	pyRequiresRe     = regexp.MustCompile(`(?m)requires-python\s*=\s*"([^"]+)"`)
)

func parseMakefileTargets(repoPath string) []string {
	makefilePath := filepath.Join(repoPath, "Makefile")
	f, err := os.Open(makefilePath)
	if err != nil {
		return nil
	}
	defer f.Close()

	var targets []string
	seen := make(map[string]bool)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if matches := makefileTargetRe.FindStringSubmatch(line); len(matches) > 1 {
			target := matches[1]
			if !seen[target] {
				targets = append(targets, target)
				seen[target] = true
			}
		}
	}
	return targets
}

// extractGoVersion reads the go directive from go.mod.
func extractGoVersion(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return ""
	}
	if m := goModVersionRe.FindSubmatch(data); len(m) > 1 {
		return string(m[1])
	}
	return ""
}

// extractCargoVersion reads the rust-version or edition from Cargo.toml.
func extractCargoVersion(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "Cargo.toml"))
	if err != nil {
		return ""
	}
	if m := cargoRustRe.FindSubmatch(data); len(m) > 1 {
		return string(m[1])
	}
	if m := cargoEditionRe.FindSubmatch(data); len(m) > 1 {
		return "edition " + string(m[1])
	}
	return ""
}

// extractPythonVersion reads python version from pyproject.toml.
func extractPythonVersion(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "pyproject.toml"))
	if err != nil {
		return ""
	}
	if m := pyRequiresRe.FindSubmatch(data); len(m) > 1 {
		return string(m[1])
	}
	return ""
}

// extractNodeVersion reads from .nvmrc or .node-version or engines in package.json.
func extractNodeVersion(repoPath string) string {
	for _, f := range []string{".nvmrc", ".node-version"} {
		if data, err := os.ReadFile(filepath.Join(repoPath, f)); err == nil {
			return strings.TrimSpace(string(data))
		}
	}
	if data, err := os.ReadFile(filepath.Join(repoPath, "package.json")); err == nil {
		var pkg map[string]any
		if json.Unmarshal(data, &pkg) == nil {
			if engines, ok := pkg["engines"].(map[string]any); ok {
				if node, ok := engines["node"].(string); ok {
					return node
				}
			}
		}
	}
	return ""
}

// detectJSFramework detects the JS/TS framework from package.json dependencies.
func detectJSFramework(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return ""
	}
	var pkg map[string]any
	if json.Unmarshal(data, &pkg) != nil {
		return ""
	}

	// Merge deps and devDeps for scanning
	allDeps := make(map[string]bool)
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[key].(map[string]any); ok {
			for name := range deps {
				allDeps[name] = true
			}
		}
	}

	// Order matters: more specific frameworks first
	frameworks := []struct{ dep, name string }{
		{"next", "Next.js"},
		{"nuxt", "Nuxt"},
		{"@angular/core", "Angular"},
		{"svelte", "Svelte"},
		{"vue", "Vue"},
		{"react", "React"},
		{"express", "Express"},
		{"fastify", "Fastify"},
		{"koa", "Koa"},
		{"hono", "Hono"},
		{"nest", "NestJS"},
		{"@nestjs/core", "NestJS"},
	}
	for _, fw := range frameworks {
		if allDeps[fw.dep] {
			return fw.name
		}
	}
	return ""
}

// detectPythonFramework detects the Python framework from dependency files.
func detectPythonFramework(repoPath string) string {
	// Check requirements.txt, pyproject.toml, Pipfile
	var content string
	for _, f := range []string{"requirements.txt", "pyproject.toml", "Pipfile", "setup.py"} {
		if data, err := os.ReadFile(filepath.Join(repoPath, f)); err == nil {
			content += string(data) + "\n"
		}
	}
	lower := strings.ToLower(content)

	frameworks := []struct{ pattern, name string }{
		{"django", "Django"},
		{"fastapi", "FastAPI"},
		{"flask", "Flask"},
		{"starlette", "Starlette"},
		{"tornado", "Tornado"},
		{"aiohttp", "aiohttp"},
		{"sanic", "Sanic"},
	}
	for _, fw := range frameworks {
		if strings.Contains(lower, fw.pattern) {
			return fw.name
		}
	}
	return ""
}

// detectRubyFramework detects the Ruby framework from the Gemfile.
func detectRubyFramework(repoPath string) string {
	data, err := os.ReadFile(filepath.Join(repoPath, "Gemfile"))
	if err != nil {
		return ""
	}
	content := strings.ToLower(string(data))
	if strings.Contains(content, "rails") {
		return "Rails"
	}
	if strings.Contains(content, "sinatra") {
		return "Sinatra"
	}
	return ""
}

// detectNPMBuildConfig reads build and lint scripts from package.json.
func detectNPMBuildConfig(repoPath string, bc BuildConfig) BuildConfig {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return bc
	}
	var pkg map[string]any
	if json.Unmarshal(data, &pkg) != nil {
		return bc
	}
	scripts, ok := pkg["scripts"].(map[string]any)
	if !ok {
		return bc
	}

	runner := "npm run"
	if _, err := os.Stat(filepath.Join(repoPath, "yarn.lock")); err == nil {
		runner = "yarn"
	} else if _, err := os.Stat(filepath.Join(repoPath, "pnpm-lock.yaml")); err == nil {
		runner = "pnpm"
	}

	if _, ok := scripts["build"]; ok && bc.BuildCommand == "" {
		bc.BuildCommand = runner + " build"
	}
	if _, ok := scripts["lint"]; ok && bc.LintCommand == "" {
		bc.LintCommand = runner + " lint"
	}
	if _, ok := scripts["format"]; ok && bc.FormatCommand == "" {
		bc.FormatCommand = runner + " format"
	}
	if _, ok := scripts["fmt"]; ok && bc.FormatCommand == "" {
		bc.FormatCommand = runner + " fmt"
	}

	return bc
}

// detectNPMTestConfig reads test configuration from package.json.
func detectNPMTestConfig(repoPath string, tc TestConfig) TestConfig {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return tc
	}
	var pkg map[string]any
	if json.Unmarshal(data, &pkg) != nil {
		return tc
	}

	runner := "npm"
	if _, err := os.Stat(filepath.Join(repoPath, "yarn.lock")); err == nil {
		runner = "yarn"
	} else if _, err := os.Stat(filepath.Join(repoPath, "pnpm-lock.yaml")); err == nil {
		runner = "pnpm"
	}

	if scripts, ok := pkg["scripts"].(map[string]any); ok {
		if _, ok := scripts["test"]; ok && tc.TestCommand == "" {
			if runner == "npm" {
				tc.TestCommand = "npm test"
			} else {
				tc.TestCommand = runner + " test"
			}
		}
	}

	// Detect test framework
	allDeps := make(map[string]bool)
	for _, key := range []string{"dependencies", "devDependencies"} {
		if deps, ok := pkg[key].(map[string]any); ok {
			for name := range deps {
				allDeps[name] = true
			}
		}
	}

	frameworks := []struct{ dep, name, pattern string }{
		{"vitest", "vitest", "*.test.ts"},
		{"jest", "jest", "*.test.js"},
		{"mocha", "mocha", "*.spec.js"},
		{"ava", "ava", "*.test.js"},
		{"@playwright/test", "playwright", "*.spec.ts"},
		{"cypress", "cypress", "*.cy.js"},
	}
	for _, fw := range frameworks {
		if allDeps[fw.dep] {
			tc.TestFramework = fw.name
			tc.TestFilePattern = fw.pattern
			break
		}
	}

	return tc
}

// parseGoDependencies reads go.mod for direct dependencies.
func parseGoDependencies(repoPath string) []Dependency {
	data, err := os.ReadFile(filepath.Join(repoPath, "go.mod"))
	if err != nil {
		return nil
	}

	var deps []Dependency
	inRequire := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "require (") || line == "require (" {
			inRequire = true
			continue
		}
		if line == ")" {
			inRequire = false
			continue
		}
		if inRequire {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				kind := "direct"
				if strings.Contains(line, "// indirect") {
					kind = "indirect"
				}
				deps = append(deps, Dependency{
					Name:    parts[0],
					Version: parts[1],
					Kind:    kind,
				})
			}
		}
	}
	return deps
}

// parsePythonDependencies reads requirements.txt for dependencies.
func parsePythonDependencies(repoPath string) []Dependency {
	var deps []Dependency
	for _, filename := range []string{"requirements.txt", "requirements-dev.txt"} {
		data, err := os.ReadFile(filepath.Join(repoPath, filename))
		if err != nil {
			continue
		}
		kind := "direct"
		if strings.Contains(filename, "dev") {
			kind = "dev"
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
				continue
			}
			// Parse "package==1.0.0" or "package>=1.0.0" or just "package"
			re := regexp.MustCompile(`^([a-zA-Z0-9_-]+)(?:[=<>!]+(.+))?`)
			if m := re.FindStringSubmatch(line); len(m) > 1 {
				deps = append(deps, Dependency{
					Name:    m[1],
					Version: m[2],
					Kind:    kind,
				})
			}
		}
	}
	return deps
}

// parseNPMDependencies reads package.json for dependencies.
func parseNPMDependencies(repoPath string) []Dependency {
	data, err := os.ReadFile(filepath.Join(repoPath, "package.json"))
	if err != nil {
		return nil
	}
	var pkg map[string]any
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}

	var deps []Dependency
	for _, entry := range []struct{ key, kind string }{
		{"dependencies", "direct"},
		{"devDependencies", "dev"},
	} {
		if depMap, ok := pkg[entry.key].(map[string]any); ok {
			for name, ver := range depMap {
				version := ""
				if v, ok := ver.(string); ok {
					version = v
				}
				deps = append(deps, Dependency{
					Name:    name,
					Version: version,
					Kind:    entry.kind,
				})
			}
		}
	}
	return deps
}

// parseRustDependencies reads Cargo.toml for dependencies.
func parseRustDependencies(repoPath string) []Dependency {
	data, err := os.ReadFile(filepath.Join(repoPath, "Cargo.toml"))
	if err != nil {
		return nil
	}

	var deps []Dependency
	inDeps := false
	inDevDeps := false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[dependencies]") {
			inDeps = true
			inDevDeps = false
			continue
		}
		if strings.HasPrefix(line, "[dev-dependencies]") {
			inDeps = false
			inDevDeps = true
			continue
		}
		if strings.HasPrefix(line, "[") {
			inDeps = false
			inDevDeps = false
			continue
		}
		if !inDeps && !inDevDeps {
			continue
		}
		// Parse "name = "version"" or "name = { version = "x" }"
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		version := strings.Trim(strings.TrimSpace(parts[1]), "\"")
		kind := "direct"
		if inDevDeps {
			kind = "dev"
		}
		deps = append(deps, Dependency{Name: name, Version: version, Kind: kind})
	}
	return deps
}

// countFilesInDir counts files (non-recursive) in a directory.
func countFilesInDir(dir string) int {
	count := 0
	filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() && shouldSkipDir(info.Name()) {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			count++
		}
		return nil
	})
	return count
}

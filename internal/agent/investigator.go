package agent

import (
	"encoding/json"

	"github.com/tzone85/nexus-dispatch/internal/llm"
)

// InvestigatorSystemPrompt returns the system prompt for the Investigator role.
// The investigator thoroughly analyses an existing codebase BEFORE any planning
// or implementation begins, following six ordered investigation phases.
func InvestigatorSystemPrompt() string {
	return `You are a Codebase Investigator for NXD, an AI agent orchestration system.
Your job is to thoroughly analyze an existing codebase BEFORE any planning or implementation begins.

You have access to tools: read_file, run_command, submit_report.

Follow these 6 investigation phases IN ORDER:

Phase 1: ORIENTATION
- Read README.md, CLAUDE.md, Makefile, docker-compose.yml if they exist
- Identify the project's purpose and entry points

Phase 2: ARCHITECTURE
- List source files (find . -name "*.go" -o -name "*.py" -o -name "*.js" | head -50)
- Identify the largest files (wc -l on source files, sort by size)
- Read package/module boundaries

Phase 3: HEALTH CHECK
- Run the build command (go build ./... or npm run build)
- Run the test suite (go test ./... or npm test)
- Check test coverage if available (go test -cover ./...)

Phase 4: DEPENDENCY GRAPH
- Check dependency manifests (go.mod, package.json, requirements.txt)
- Map internal module dependencies via imports

Phase 5: CODE SMELLS
- Files exceeding 500 lines
- Source files with no corresponding test file
- Count of TODO/FIXME comments
- Deeply nested code (>4 levels)
- Hardcoded values (URLs, ports, credentials)

Phase 6: RISK ASSESSMENT
- Check git log for recent churn: git log --since=30d --name-only --pretty=format: | sort | uniq -c | sort -rn | head -20
- Cross-reference high-churn files with test coverage
- Identify untested critical paths

After all 6 phases, call submit_report with a structured JSON report.`
}

// InvestigatorTools returns the tool definitions available to the Investigator
// role: read_file, run_command, and submit_report.
func InvestigatorTools() []llm.ToolDefinition {
	return []llm.ToolDefinition{
		{
			Name:        "read_file",
			Description: "Read the contents of a file in the project.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string","description":"Relative file path"}},"required":["path"]}`),
		},
		{
			Name:        "run_command",
			Description: "Run a shell command in the project directory. Use for: ls, find, wc, grep, git, go build, go test, npm.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"command":{"type":"string","description":"Shell command to execute"}},"required":["command"]}`),
		},
		{
			Name:        "submit_report",
			Description: "Submit the final investigation report. Call this ONCE after completing all 6 phases.",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"summary":{"type":"string","description":"200-word architecture brief"},"entry_points":{"type":"array","items":{"type":"string"}},"modules":{"type":"array","items":{"type":"object","properties":{"name":{"type":"string"},"path":{"type":"string"},"file_count":{"type":"integer"},"line_count":{"type":"integer"},"has_tests":{"type":"boolean"}},"required":["name","path"]}},"build_passes":{"type":"boolean"},"test_passes":{"type":"boolean"},"test_count":{"type":"integer"},"coverage_pct":{"type":"number"},"code_smells":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"severity":{"type":"string"},"description":{"type":"string"}},"required":["file","severity","description"]}},"risk_areas":{"type":"array","items":{"type":"object","properties":{"file":{"type":"string"},"reason":{"type":"string"},"severity":{"type":"string"}},"required":["file","reason","severity"]}},"recommendations":{"type":"array","items":{"type":"string"}}},"required":["summary","entry_points","build_passes","test_passes","recommendations"]}`),
		},
	}
}

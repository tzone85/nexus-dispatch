# Port VXD Guardrails to NXD (Phase 1 — Critical)

## Context
NXD needs 4 guardrails that VXD developed through production usage. All go in internal/engine/.

## Requirements

### 1. Hallucination Scrubber
Create internal/engine/sanitize_output.go:
- scrubHallucinationsFromWorktree() scans committed files for LLM reasoning text
- scrubFile() strips preamble lines like "Looking at...", "I'll...", "Here's..."
- isHallucinationLine() matches 25+ known LLM preamble patterns
- isSourceExt() identifies source files (.js/.ts/.go/.py etc)
- scanFileForConflictMarkers() detects unresolved <<<<<<< markers
- validateNoConflictMarkers() scans all changed files, returns list

Wire into postExecutionPipeline in monitor.go after autoCommit, before gitDiff.
Add tests: TestIsHallucinationLine, TestScrubFile, TestScrubFile_NoHallucination, TestScanFileForConflictMarkers.

### 2. Build Validation
Add validateBuild() to sanitize_output.go:
- Detect project type from package.json/go.mod/pyproject.toml
- Run npm run build, go build ./..., or python syntax check
- Return error with truncated output on failure
- Wire into postExecutionPipeline after scrubbing (non-blocking warning)

### 3. Enhanced Gitignore
Update ensureGitignorePatterns() to add: WAVE_CONTEXT.md, REQUIREMENT.md, nxd.yaml, .nxd-fix-gaps.md

### 4. File Tree for Reviewer
Add captureFileTree() (runs git ls-files). Pass to reviewer in Review() call alongside diff. Prevents hallucination about missing files.

## Constraints
- Go code, tests with -race
- Must not break existing tests
- go build ./... and go test ./... must pass

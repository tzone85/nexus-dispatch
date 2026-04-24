# Requirements: Port VXD Guardrails & Capabilities to NXD

## Context

Vortex Dispatch (VXD) and Nexus Dispatch (NXD) share the same core mission — autonomous AI-powered software delivery. VXD targets cloud/enterprise (Claude API), NXD targets offline-first (Ollama). Through production usage, VXD has developed critical guardrails and post-completion capabilities that NXD lacks. This requirement ports those capabilities while respecting NXD's offline-first philosophy.

## Architectural Decision

**Architecture: Domain-Driven Design (DDD) with Hexagonal Ports/Adapters**

The existing architecture is already DDD-aligned:
- **Domain Layer:** `internal/engine/` — core orchestration logic (monitor, dispatcher, reviewer, QA, merger)
- **Application Layer:** `internal/cli/` — CLI commands that orchestrate domain operations
- **Infrastructure Layer:** `internal/llm/`, `internal/runtime/`, `internal/state/` — adapters for LLMs, runtimes, storage

We maintain this pattern because:
1. Both VXD and NXD already use it — consistency reduces cognitive load
2. The ported features are domain-level guardrails (belong in `internal/engine/`)
3. Hexagonal ports allow swapping infrastructure (Ollama vs Claude) without changing domain logic
4. No microservices needed — this is a single-binary CLI tool where monolithic DDD is appropriate

A microservice architecture would be overengineering for a CLI tool that runs on a developer's laptop. The hexagonal/ports-and-adapters pattern gives us the modularity benefits without network overhead.

## Requirements

### Phase 1: Critical Guardrails (MUST-HAVE)

#### 1.1 Hallucination Scrubber
**Priority:** CRITICAL
**Port from:** VXD `internal/engine/sanitize_output.go`
**Adapt for NXD:** Rename artifact patterns from `vxd` to `nxd`

Create `internal/engine/sanitize_output.go` with:
- `scrubHallucinationsFromWorktree()` — scans committed source files for LLM reasoning preamble
- `scrubFile()` — strips hallucination lines from individual files
- `isHallucinationLine()` — matches 25+ patterns: "Looking at", "I'll", "Here's", "Based on", etc.
- `isSourceExt()` — identifies source file extensions (.js, .ts, .go, .py, etc.)
- `scanFileForConflictMarkers()` — detects unresolved `<<<<<<<` markers
- `validateNoConflictMarkers()` — scans all changed files

Wire into `postExecutionPipeline()` in monitor.go:
- After `autoCommit()`, before `gitDiff()`
- If hallucinations found: strip, amend commit, log warning
- If conflict markers found: reset story to draft, don't proceed

Tests:
- `TestIsHallucinationLine` — verify all 25+ patterns detected
- `TestScrubFile` — verify preamble stripped, code preserved
- `TestScrubFile_NoHallucination` — verify clean files untouched
- `TestScrubFile_EntirelyHallucination` — verify skipped with warning
- `TestScanFileForConflictMarkers` — verify marker detection

#### 1.2 Build Validation
**Priority:** CRITICAL
**Port from:** VXD `internal/engine/sanitize_output.go` (validateBuild function)

Add `validateBuild()` to sanitize_output.go:
- Auto-detect project type (package.json → Node, go.mod → Go, pyproject.toml → Python)
- Run appropriate build check (tsc --noEmit, npm run build, go build ./...)
- Return error with truncated output if build fails
- Wire into postExecutionPipeline after hallucination scrubbing
- Non-blocking: log warning, let review/QA catch it

#### 1.3 Enhanced Gitignore Patterns
**Priority:** HIGH
**Port from:** VXD `ensureGitignorePatterns()`

Update NXD's existing `ensureGitignorePatterns()` to include:
```
CLAUDE.md
WAVE_CONTEXT.md
REQUIREMENT.md
nxd.yaml
.nxd-prompts/
.serena/
.nxd-fix-gaps.md
```
Currently NXD only gitignores: CLAUDE.md, .nxd-prompts/, .serena/

#### 1.4 File Tree Context for Reviewer
**Priority:** HIGH
**Port from:** VXD `captureFileTree()` + reviewer integration

Add `captureFileTree()` function (runs `git ls-files`) and pass the file tree to the reviewer alongside the diff. This prevents the reviewer from hallucinating about "missing" files that exist in the repo but weren't changed.

### Phase 2: Post-Completion Capabilities (SHOULD-HAVE)

#### 2.1 Verification Loop
**Priority:** HIGH
**Port from:** VXD `internal/engine/verification_loop.go`
**Adapt:** Replace VXD-specific paths/names with NXD equivalents

Create `internal/engine/verification_loop.go` with:
- `RunVerificationLoop()` — orchestrates the full verification cycle
- `ensureDependencies()` — runs npm install / go mod download
- `checkBuild()` — validates project builds
- `checkTests()` — runs test suite, counts pass/fail
- `scanForHallucinations()` — walks source tree for LLM text
- `cleanWorkspaceArtifacts()` — removes NXD temp files
- `GapsToRequirement()` — converts gaps to a follow-up requirement doc
- `ShouldRunFixCycle()` — determines if a fix dispatch is needed

Wire into Monitor completion path:
```go
if allDone {
    // 1. Generate documentation
    // 2. Run verification loop
    // 3. If gaps found, write .nxd-fix-gaps.md
    // 4. Mark requirement complete
}
```

#### 2.2 Auto-Documentation (README Generator)
**Priority:** HIGH
**Port from:** VXD `internal/engine/doc_generator.go`
**Adapt:** Change footer to NXD branding, use NXD's LLM client

Create `internal/engine/doc_generator.go` with:
- `generateDocumentation()` — creates/updates README.md using LLM
- If README exists: prompts LLM to update with new features
- If README missing: generates full professional README
- Appends NXD Team footer
- Commits the documentation update
- `SetDocGenerator()` on Monitor to wire the LLM client

Footer:
```markdown
---
## Development Team
Built by the **NXD Team** — autonomous AI-powered software delivery (offline-first)
```

#### 2.3 Wave Context Capture
**Priority:** MEDIUM
**Port from:** VXD `CaptureStoryContext()`

After each successful merge, write a summary of what the story accomplished to `WAVE_CONTEXT.md`. This gives subsequent waves context about what's been built, preventing duplicate work.

### Phase 3: Cross-Pollination (NXD → VXD learnings)

These are NXD features that VXD should also adopt. Document them but don't implement in this requirement — they'll be a separate VXD requirement.

#### 3.1 Pipeline Timeout (NXD → VXD)
NXD wraps postExecutionPipeline in a 5-minute context timeout. VXD should adopt this to prevent LLM call hangs.

#### 3.2 QA Failure Analysis (NXD → VXD)
NXD's `AnalyzeFailure()` provides LLM-generated hints to agents on retry after QA failure. VXD should adopt this for better retry success rates.

#### 3.3 Story ID Validation (NXD → VXD)
NXD validates story IDs against `^[a-zA-Z0-9._-]+$` before dispatch. VXD should adopt this instead of the current branch name sanitization (which fixes the symptom but not the cause).

## Technical Constraints
- All code must be Go (matching existing NXD codebase)
- Must work fully offline (no cloud API dependencies in guardrails)
- LLM calls for documentation may use Ollama or any configured provider
- Tests must use Go testing package with `-race -count=1`
- Must not break existing NXD functionality (backward compatible)
- Follow existing NXD code style and package organization

## Acceptance Criteria
- All guardrails (hallucination, build, conflict) wired into postExecutionPipeline
- Verification loop runs automatically on requirement completion
- README generated/updated after all stories merge
- NXD gitignore patterns include all workspace artifacts
- File tree context passed to reviewer
- All new code has unit tests
- `go build ./...` passes
- `go test ./... -race -count=1` passes
- Existing NXD tests unaffected

## Estimated Effort
- Phase 1 (Critical Guardrails): ~7 hours
- Phase 2 (Post-Completion): ~11 hours  
- Phase 3 (Documentation only): ~1 hour
- **Total: ~19 hours**

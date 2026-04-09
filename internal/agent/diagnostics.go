package agent

// CodebaseArchaeology is a playbook injected into agent system prompts when
// working with existing codebases. It guides the agent through orientation
// before planning any changes.
const CodebaseArchaeology = `
BEFORE PLANNING, follow this codebase orientation:

1. Read the Investigation Report provided below
2. Identify which modules are affected by this requirement
3. Check test coverage of affected modules — untested modules need a "add test coverage" story first
4. Check build health — if build is broken, first story must fix it
5. Map file ownership carefully — existing files must not be owned by multiple stories
6. Plan stories that follow existing patterns, not introduce new ones

ORIENTATION PROCEDURE:

Step 1 — Inventory
  List every top-level directory and its purpose.
  Identify the entry point (main.go, index.ts, manage.py, etc.).
  Locate configuration files (Makefile, Dockerfile, CI configs, package.json).
  Note the dependency manager and lock file (go.sum, yarn.lock, Pipfile.lock).

Step 2 — Architecture Map
  Draw a dependency graph: which packages import which.
  Identify the data layer (models, repositories, migrations).
  Identify the transport layer (HTTP handlers, gRPC, CLI commands).
  Identify the domain/business layer (services, use-cases, core logic).
  Note any middleware, interceptors, or decorators.

Step 3 — Convention Detection
  File naming: snake_case, camelCase, kebab-case?
  Test co-location: same directory or separate test/ folder?
  Error handling: sentinel errors, error wrapping, result types?
  Logging: structured (slog, zap, logrus) or fmt.Println?
  Configuration: env vars, YAML, TOML, or flags?

Step 4 — Health Assessment
  Run the build command — does it pass?
  Run the test suite — what is the pass rate?
  Run the linter — how many warnings?
  Check for stale dependencies (outdated, deprecated, vulnerable).
  Check git history: when was the last commit? Is the repo active?

Step 5 — Risk Identification
  Which modules have zero test coverage?
  Which modules have high churn (frequent changes)?
  Are there any TODO/FIXME/HACK comments in critical paths?
  Are there known tech debt items documented anywhere?
  Does the CI pipeline have any failing or skipped jobs?

Step 6 — Story Planning Constraints
  Each story MUST target a specific module or file set.
  No two stories may modify the same file (prevents merge conflicts).
  Stories that touch untested code must include characterization tests.
  Stories must preserve existing public APIs unless the requirement explicitly changes them.
  Database migration stories must be ordered before stories that depend on the new schema.
  Infrastructure stories (CI, Docker, deploy) are always separate from feature stories.

OUTPUT: Before generating any stories, output a brief Codebase Orientation
Summary covering: architecture style, tech stack, build health, test coverage
estimate, and any blockers that must be resolved first.
`

// BugHuntingMethodology is a structured 5-phase playbook for systematically
// diagnosing and fixing bugs. Injected into agent prompts when the requirement
// is classified as a bug fix.
const BugHuntingMethodology = `
BUG HUNTING METHODOLOGY — 5 PHASES

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PHASE 1: REPRODUCE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Goal: Confirm the bug exists and capture its exact behavior.

Steps:
  1. Write a failing test that demonstrates the bug.
     - The test must FAIL before any fix is applied.
     - Use the exact input described in the bug report.
  2. Run the test and confirm it fails with the expected symptom.
  3. Document three things clearly:
     - INPUT: The exact data, request, or action that triggers the bug.
     - EXPECTED: What the correct behavior should be.
     - ACTUAL: What currently happens instead.
  4. If the bug is intermittent, increase iterations or add timing controls
     to make it deterministic.
  5. If you cannot reproduce, check:
     - Environment differences (OS, versions, config).
     - Data dependencies (database state, file existence).
     - Timing dependencies (race conditions, timeouts).

DO NOT proceed to Phase 2 until you have a reliably failing test.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PHASE 2: ISOLATE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Goal: Narrow down the exact location and trigger of the bug.

Techniques (apply in order):
  1. Stack Trace Analysis
     - Read the stack trace BOTTOM to TOP (root cause is at the bottom).
     - Identify the first frame that belongs to YOUR code (not stdlib/deps).
     - Open that file and line number.

  2. Targeted Logging
     - Add temporary log statements at suspected code paths.
     - Log variable values BEFORE and AFTER the operation.
     - Include correlation IDs to trace through concurrent flows.

  3. Binary Search
     - If the bug was recently introduced, use git bisect.
     - Command: git bisect start HEAD <last-known-good-commit>
     - Mark each commit: git bisect good / git bisect bad
     - This finds the exact commit that introduced the bug.

  4. Git Blame
     - Run git blame on the suspected file.
     - Check the commit message for the change that introduced the logic.
     - Review the PR or issue associated with that commit.

  5. Dependency Isolation
     - Replace external dependencies with mocks to rule them out.
     - Test with a minimal configuration (no plugins, default settings).
     - Check if the bug exists on a clean database/state.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PHASE 3: UNDERSTAND ROOT CAUSE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Goal: Fully understand WHY the bug happens before writing any fix.

Answer ALL five questions:
  1. What does the code EXPECT to happen at this point?
  2. What ACTUALLY happens instead?
  3. Which specific line or expression produces the wrong result?
  4. WHY does that line behave this way? (trace the data flow backward)
  5. When was this behavior introduced? (was it always broken, or did a
     change cause it?)

Common root cause categories:
  - Logic error: wrong condition, inverted boolean, missing case.
  - Data error: unexpected nil/null, wrong type, stale cache.
  - Concurrency error: race condition, deadlock, missing synchronization.
  - Integration error: API contract changed, schema mismatch.
  - Configuration error: wrong env var, missing secret, bad default.

DO NOT proceed to Phase 4 until you can explain the root cause in one sentence.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PHASE 4: MINIMAL FIX
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Goal: Fix ONLY the bug with the smallest possible change.

Rules:
  1. Fix only the root cause identified in Phase 3.
  2. Do NOT refactor surrounding code.
  3. Do NOT fix unrelated issues you noticed.
  4. Do NOT change formatting or style.
  5. Do NOT update dependencies unless the bug is in the dependency.

Verification:
  1. The failing test from Phase 1 MUST now pass.
  2. Run the FULL test suite — no regressions allowed.
  3. The fix should touch as few files as possible (ideally 1-2).
  4. The diff should be reviewable in under 5 minutes.

If the fix requires more than ~20 lines of change, reconsider whether you
have identified the true root cause.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
PHASE 5: VERIFY
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

Goal: Ensure the fix is correct, complete, and safe.

Checks:
  1. Race Detection
     - Go: go test -race ./...
     - Other: use thread sanitizers or equivalent tools.

  2. Edge Cases
     - What happens with empty input?
     - What happens with maximum/overflow values?
     - What happens with nil/null/undefined?
     - What happens with concurrent access?

  3. Regression Tests
     - Add edge case tests discovered during investigation.
     - Ensure these tests would have caught the bug.

  4. Integration Verification
     - If the bug involved external systems, verify the integration.
     - Check that error handling covers the failure mode.

  5. Documentation
     - Add a comment explaining WHY the fix is needed (not WHAT it does).
     - If the bug pattern could recur, document the prevention strategy.

COMMON BUG PATTERNS (check these first):

  Nil/Null Pointer
    Symptom: panic, segfault, NullPointerException.
    Check: every pointer/reference dereference, map access, slice index.

  Race Condition
    Symptom: intermittent failures, different results on each run.
    Check: shared mutable state accessed from goroutines/threads.

  Off-by-One
    Symptom: missing first/last element, index out of bounds.
    Check: loop bounds, slice ranges, string indices.

  Type Coercion / Overflow
    Symptom: wrong numeric results, unexpected truncation.
    Check: integer division, int32/int64 boundaries, float precision.

  Environment-Dependent
    Symptom: works locally, fails in CI or production.
    Check: file paths, timezone, locale, env vars, DNS resolution.

  State Mutation
    Symptom: works on first call, fails on subsequent calls.
    Check: shared state modified between calls, missing reset/cleanup.

  Error Swallowing
    Symptom: silent failure, missing data, incomplete operation.
    Check: ignored error returns, empty catch blocks, missing nil checks.

  Zero-Value Fields
    Symptom: struct fields have default values instead of expected values.
    Check: missing initialization, JSON tags, constructor usage.

  Resource Leaks
    Symptom: increasing memory, file descriptor exhaustion, connection pool depletion.
    Check: unclosed files, HTTP bodies, database connections, channels.
`

// InfrastructureDebugging is a diagnostic toolkit organized by infrastructure
// domain. Injected into agent prompts when the requirement involves
// infrastructure, deployment, or operational issues.
const InfrastructureDebugging = `
INFRASTRUCTURE DEBUGGING TOOLKIT

Use the appropriate section below based on the infrastructure domain involved.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
DOCKER
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Container Status:
    docker ps -a                          # all containers, including stopped
    docker logs <container> --tail 100    # recent logs
    docker inspect <container>            # full container config
    docker stats --no-stream              # resource usage snapshot

  Compose:
    docker compose config                 # validate and display effective config
    docker compose ps                     # service status
    docker compose logs --tail 50 <svc>   # per-service logs
    docker compose down && docker compose up -d  # full restart

  Disk & Images:
    docker system df                      # disk usage breakdown
    docker image prune -f                 # remove dangling images
    docker volume ls                      # list volumes
    docker volume inspect <vol>           # volume mount details

  Network:
    docker network ls                     # list networks
    docker network inspect <net>          # subnet, connected containers
    docker exec <container> ping <other>  # test inter-container connectivity

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
DATABASE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  PostgreSQL Health:
    pg_isready -h <host> -p <port>        # connection check
    psql -c "SELECT version();"           # version info
    psql -c "SELECT * FROM pg_stat_activity WHERE state = 'active';"
    psql -c "SELECT pg_database_size(current_database());"
    psql -c "SELECT schemaname, relname, n_dead_tup FROM pg_stat_user_tables ORDER BY n_dead_tup DESC LIMIT 10;"

  SQLite Health:
    sqlite3 <db> "PRAGMA integrity_check;"
    sqlite3 <db> "PRAGMA journal_mode;"
    sqlite3 <db> ".tables"
    sqlite3 <db> "SELECT COUNT(*) FROM <table>;"

  MySQL Health:
    mysqladmin -h <host> -u <user> -p status
    mysql -e "SHOW PROCESSLIST;"
    mysql -e "SHOW ENGINE INNODB STATUS\G"
    mysql -e "SELECT table_schema, table_name, table_rows FROM information_schema.tables ORDER BY table_rows DESC LIMIT 10;"

  Common Database Issues:
    - Connection refused: check host, port, pg_hba.conf, bind address.
    - Too many connections: check pool size, idle connections, leaked conns.
    - Slow queries: check EXPLAIN ANALYZE, missing indexes, table bloat.
    - Lock contention: check pg_locks, long-running transactions.
    - Migration failures: check migration status table, run pending migrations.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
CI/CD
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  GitHub Actions:
    gh run list --limit 10                # recent workflow runs
    gh run view <run-id>                  # run details and status
    gh run view <run-id> --log-failed     # logs from failed steps only
    gh run rerun <run-id>                 # retry a failed run

  Secrets & Environment:
    gh secret list                        # repository secrets (names only)
    gh variable list                      # repository variables
    Verify secrets are set for the correct environment (production, staging).
    Check that secret names match what the workflow YAML references.

  Dependency Drift:
    Compare lock file in CI vs local (go.sum, yarn.lock, Pipfile.lock).
    Check for version constraints that resolve differently on CI.
    Verify that CI cache is not serving stale dependencies.
    Run dependency audit: npm audit, go vuln check, pip-audit.

  Common CI Failures:
    - "module not found": stale cache, missing go mod tidy, wrong Go version.
    - Flaky tests: timing, ordering, shared state, network calls in tests.
    - Permission denied: wrong file permissions, missing chmod in Dockerfile.
    - Out of memory: reduce parallelism, increase runner resources.
    - Timeout: optimize slow tests, split into parallel jobs.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
NETWORK
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  HTTP Debugging:
    curl -v <url>                         # verbose request/response headers
    curl -o /dev/null -s -w "%{http_code} %{time_total}s\n" <url>
    curl -k <url>                         # skip TLS verification (debug only)

  Port & Connection:
    lsof -i :<port>                       # what process is using a port
    netstat -tlnp                         # listening TCP ports (Linux)
    ss -tlnp                              # same, modern alternative
    nc -zv <host> <port>                  # test TCP connectivity

  DNS:
    nslookup <hostname>                   # basic DNS resolution
    dig <hostname> +short                 # concise DNS lookup
    dig <hostname> ANY                    # all DNS records
    cat /etc/resolv.conf                  # DNS resolver configuration

  TLS / Certificates:
    openssl s_client -connect <host>:443  # TLS handshake details
    openssl x509 -in cert.pem -text -noout  # certificate details
    echo | openssl s_client -connect <host>:443 2>/dev/null | openssl x509 -dates -noout

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
ENVIRONMENT
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Variables & Configuration:
    env | sort                            # all environment variables
    echo $PATH | tr ':' '\n'             # PATH entries, one per line
    printenv <VAR_NAME>                   # specific variable value
    cat .env                              # dotenv file contents

  Runtime Versions:
    go version                            # Go
    node --version && npm --version       # Node.js
    python3 --version && pip --version    # Python
    ruby --version && bundle --version    # Ruby
    java -version 2>&1                    # Java

  System Resources:
    df -h                                 # disk space
    free -h                               # memory (Linux)
    vm_stat                               # memory (macOS)
    ulimit -a                             # resource limits
    top -bn1 | head -20                   # process snapshot (Linux)

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
LOG ANALYSIS
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Pattern Searching:
    grep -rn "ERROR\|FATAL\|PANIC" <logfile>
    grep -C 3 "error" <logfile>           # 3 lines of context around errors
    grep -i "timeout\|deadline\|exceeded" <logfile>

  Frequency Analysis:
    sort <logfile> | uniq -c | sort -rn | head -20   # most frequent lines
    grep "ERROR" <logfile> | cut -d' ' -f1-2 | uniq -c | sort -rn  # errors by timestamp

  Live Monitoring:
    tail -f <logfile>                     # follow log output
    tail -f <logfile> | grep --line-buffered "ERROR"

  Systemd (Linux):
    journalctl -u <service> --since "1 hour ago"
    journalctl -u <service> -f            # follow service logs
    systemctl status <service>            # service status and recent log

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
COMMON INFRASTRUCTURE FAILURES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Port Conflict
    Symptom: "address already in use", "bind: EADDRINUSE".
    Fix: lsof -i :<port>, then stop the conflicting process or use a different port.

  Permission Denied
    Symptom: "permission denied", "EACCES".
    Fix: check file ownership (ls -la), Docker user, volume mount permissions.

  Disk Full
    Symptom: "no space left on device", write failures.
    Fix: df -h, docker system prune, clear old logs, remove temp files.

  DNS Resolution Failure
    Symptom: "could not resolve host", "NXDOMAIN".
    Fix: check /etc/resolv.conf, try direct IP, check DNS server status.

  TLS Certificate Error
    Symptom: "certificate has expired", "x509: certificate signed by unknown authority".
    Fix: check certificate dates, renew via Let's Encrypt, update CA bundle.

  Out of Memory
    Symptom: "OOM killed", process exits with signal 9.
    Fix: increase memory limits, check for memory leaks, reduce concurrency.

  Connection Timeout
    Symptom: "connection timed out", "context deadline exceeded".
    Fix: check firewall rules, security groups, network ACLs, service health.
`

// LegacyCodeSurvival is a playbook for safely working with legacy or unfamiliar
// codebases. It emphasizes characterization tests, small steps, and preserving
// existing behavior.
const LegacyCodeSurvival = `
LEGACY CODE SURVIVAL GUIDE

For working safely with existing, unfamiliar, or poorly-tested codebases.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
5 GOLDEN RULES
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  1. NEVER rewrite from scratch.
     Rewrites lose institutional knowledge encoded in edge-case handling.
     They introduce new bugs while solving none of the old ones.
     The existing code works (however ugly) — respect that.

  2. Make the change easy, THEN make the easy change.
     First refactor the code so your change is simple to add.
     Then add your change as a trivial second step.
     Two small PRs are better than one large one.

  3. Write characterization tests FIRST.
     Before changing anything, write tests that capture current behavior.
     These tests document what the code actually does (not what it should do).
     They are your safety net — if they break, you changed behavior.

  4. Take small steps.
     Each commit should be independently revertable.
     Each change should be verifiable in isolation.
     If something goes wrong, you can pinpoint exactly which step caused it.

  5. Commit often.
     Commit after every successful refactoring step.
     Commit after every passing test.
     Your git history is your undo stack.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
WORKING WITH UNKNOWN CODE
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Trace Entry Points
    Find where execution begins for the feature you need to change.
    HTTP handler? CLI command? Event listener? Cron job?
    Follow the call chain from entry point to the code you need to modify.

  Grep Before You Write
    Search for existing usage of the function/type/constant you want to change.
    Find all callers — changing a function signature affects every caller.
    Search for string literals that reference the behavior (error messages, log lines).

  Read Tests First
    Tests are the best documentation of intended behavior.
    They show expected inputs, outputs, and edge cases.
    They reveal the author's assumptions about how the code should work.
    If tests are missing, that tells you something important about risk.

  Git Blame for Context
    Every line of code was written for a reason.
    git blame shows WHEN and WHY each line was added.
    The commit message often explains the reasoning.
    Recent changes are more likely to be the source of recent bugs.

  Follow Existing Patterns
    If the codebase uses a specific error handling pattern, use the same one.
    If there is a naming convention, follow it even if you disagree.
    If there is a directory structure, put new files where they belong.
    Consistency matters more than individual preference in legacy code.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
SAFE REFACTORING STEPS (in risk order, lowest first)
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  1. Extract Function / Method
     Risk: LOW. Moves code without changing behavior.
     Verification: existing tests still pass.
     Benefit: improves readability, enables testing of extracted logic.

  2. Rename (variable, function, type)
     Risk: LOW. Purely cosmetic change.
     Verification: compiler/linter catches all references.
     Benefit: improves readability, clarifies intent.

  3. Remove Dead Code
     Risk: LOW-MEDIUM. Code might be used via reflection or dynamic dispatch.
     Verification: grep for all references, check for string-based lookups.
     Benefit: reduces confusion, shrinks codebase.

  4. Add Type Annotations / Interfaces
     Risk: MEDIUM. May reveal type mismatches.
     Verification: compiler checks, existing tests pass.
     Benefit: documents contracts, enables better tooling.

  5. Add Error Handling
     Risk: MEDIUM. Changes control flow.
     Verification: existing tests pass, new error paths tested.
     Benefit: prevents silent failures, improves observability.

  6. Restructure / Move Files
     Risk: HIGH. Affects imports, build config, CI, documentation.
     Verification: full build, full test suite, CI pipeline.
     Benefit: better organization, clearer boundaries.

IMPORTANT: Each step should be a SEPARATE commit. Never combine risk levels.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
WHAT NOT TO DO
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  No directory restructuring in bug fix PRs.
    A bug fix should fix the bug. Period.
    Restructuring is a separate story with its own review.

  No formatting changes mixed with logic changes.
    Run formatters in a dedicated commit BEFORE logic changes.
    This keeps the logic diff clean and reviewable.

  No "while I'm here" fixes.
    Resist the urge to fix unrelated issues.
    Each fix is a separate story with its own test and review.
    Mixing changes makes it impossible to revert one without the other.

  No premature abstractions.
    Do not create interfaces for things that have only one implementation.
    Do not add factory patterns "for future flexibility."
    Do not build plugin systems nobody asked for.
    Wait until the second or third use case before abstracting.

  No fixing unrelated bugs you discover.
    Document them as new issues/stories instead.
    Fixing them in the current PR increases risk and review burden.
    Each bug fix needs its own reproduction test and verification.

━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
WHEN TESTS DON'T EXIST
━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━

  Step 1: Write Characterization Tests
    Call the function with known inputs and assert on CURRENT output.
    Even if the current output is "wrong," test it.
    These tests document reality, not intent.
    Cover happy path, error path, and boundary cases.

  Step 2: Commit Tests Separately
    Commit characterization tests BEFORE making any changes.
    This proves the tests pass against the existing code.
    It also creates a clean git boundary for reverting.

  Step 3: Make Your Change
    Now modify the code with the safety net of characterization tests.
    If a characterization test breaks, you know you changed behavior.
    Decide explicitly: is the behavior change intended or accidental?

  Step 4: Add Targeted Tests
    Write new tests that cover the CHANGED behavior.
    Update characterization tests that intentionally changed.
    Add edge case tests for the new logic.

  Step 5: Verify Coverage
    Run coverage analysis on the modified files.
    Ensure all new code paths are tested.
    Ensure all modified branches have test coverage.

REMEMBER: The goal is not to make the code perfect.
The goal is to make your change safely without breaking existing behavior.
Legacy code survived this long for a reason — treat it with respect.
`

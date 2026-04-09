# NXD Troubleshooting Guide

## Common Issues

### "Ollama not detected" on `nxd init`

**Cause:** Ollama server isn't running or isn't accessible on port 11434.

**Fix:**
```bash
# Start Ollama
ollama serve

# Verify it's running
curl http://localhost:11434/api/tags
```

If you installed Ollama as a system service:
```bash
# macOS
brew services start ollama

# Linux (systemd)
sudo systemctl start ollama
```

### "model not found" during planning

**Cause:** The model specified in `nxd.yaml` hasn't been pulled to Ollama.

**Fix:**
```bash
# Check what models you have
ollama list

# Pull the default model
ollama pull gemma4:26b
```

**Tip:** Model names in `nxd.yaml` must exactly match Ollama tags. Use `ollama list` to see exact names.

### Gemma 4 model not loading

**Cause:** Ollama version is too old. Gemma 4 requires Ollama >= 0.20.

**Fix:**
```bash
# Check your Ollama version
ollama --version

# Update Ollama
# macOS
brew upgrade ollama

# Linux
curl -fsSL https://ollama.com/install.sh | sh
```

If Ollama reports the correct version but the model still fails to load, try removing and re-pulling:
```bash
ollama rm gemma4:26b
ollama pull gemma4:26b
```

### Google AI 429 errors

**Cause:** The Google AI free tier quota is exhausted. This is normal and expected.

**Symptoms:**
- Log messages: `Google AI rate limited, falling back to Ollama`
- Slightly slower inference (local vs cloud)

**Fix:**
This is handled automatically. When using the `google+ollama` provider, NXD falls back to local Ollama on HTTP 429 and retries Google AI after `fallback_cooldown_s` (default: 60 seconds).

If you see persistent 429 errors:
1. Verify your API key is valid: `echo $GOOGLE_AI_API_KEY`
2. Check free tier limits at [ai.google.dev](https://ai.google.dev)
3. Increase cooldown: set `fallback_cooldown_s: 120` in `nxd.yaml`
4. Switch to `ollama` provider to skip cloud entirely

### Function calling unexpected results

**Cause:** The model does not support native function calling, or is returning malformed tool calls.

**Symptoms:**
- JSON parse errors in planning output
- Stories with missing fields
- Agent actions not matching expected tool calls

**Fix:**
1. Verify you are using a Gemma 4 model (native function calling support):
   ```bash
   ollama show gemma4:26b | head -5
   ```

2. Ensure Ollama >= 0.20 (function calling support):
   ```bash
   ollama --version
   ```

3. If using a non-Gemma model, NXD falls back to text-based JSON parsing automatically. This is less reliable -- consider switching to Gemma 4.

4. For persistent issues, increase `max_tokens` for the affected role to give the model more room for structured output.

### Native runtime command blocked

**Cause:** The native Gemma runtime tried to execute a shell command not in the `command_allowlist`.

**Symptoms:**
- Log messages: `command blocked by allowlist`
- Agent appears stuck after attempting a build/test step

**Fix:**
Add the needed command to the `command_allowlist` in `nxd.yaml`:
```yaml
runtimes:
  gemma:
    native: true
    command_allowlist:
      - "go build ./..."
      - "go test ./..."
      - "npm test"
      - "npm run build"
      - "make test"         # Add your project's commands
      - "cargo test"
```

Only add commands you trust -- this allowlist is a safety boundary preventing arbitrary shell execution.

### Model update check

**Cause:** NXD checks for newer model versions on startup (when `update_check: true`).

**Symptoms:**
- Startup message: `Newer version of gemma4:26b available`
- Slow startup on metered connections

**Fix:**
- To update: `ollama pull gemma4:26b`
- To check manually: `nxd models check`
- To disable automatic checks:
  ```yaml
  update_check: false
  ```

### Planning produces poor or malformed output

**Cause:** The Tech Lead model is too small to produce valid structured JSON.

**Symptoms:**
- Stories with no acceptance criteria
- Invalid JSON parse errors
- Circular dependencies
- All stories scored as complexity 1

**Fix:**
1. Upgrade the Tech Lead model:
   ```yaml
   tech_lead:
     provider: ollama
     model: gemma4:26b    # 26B MoE recommended for good planning
     max_tokens: 16000
   ```

2. Simplify your requirement — break large requirements into smaller, focused ones:
   ```bash
   # Instead of:
   nxd req "Build a complete e-commerce platform"

   # Try:
   nxd req "Add product listing with search and pagination"
   ```

### Agent stuck in tmux session

**Cause:** The agent hasn't produced output for longer than `stuck_threshold_s`.

**Symptoms:**
- `nxd agents --status stuck` shows stuck agents
- `AGENT_STUCK` events in `nxd events`
- Dashboard shows red status

**Fix:**
1. Check the tmux session directly:
   ```bash
   tmux attach -t nxd-req01-junior-1
   ```

2. If the session is genuinely stuck, kill and resume:
   ```bash
   tmux kill-session -t nxd-req01-junior-1
   nxd resume <req-id>
   ```

3. Increase the stuck threshold if your model is slow:
   ```yaml
   monitor:
     stuck_threshold_s: 300  # 5 minutes instead of 2
   ```

### Build/test failures in QA

**Cause:** The implementing agent wrote code that doesn't compile or pass tests.

**Symptoms:**
- `STORY_QA_FAILED` events
- `nxd events --type STORY_QA_FAILED` shows which checks failed

**Fix:**
1. Check what failed:
   ```bash
   nxd events --type STORY_QA_FAILED --limit 5
   ```

2. The story automatically loops back to the agent for fixes. If it keeps failing, it escalates after `max_qa_failures_before_escalation` attempts.

3. Adjust QA commands if they're project-specific — NXD detects common build tools, but you may need custom commands for your project.

### Merge conflicts

**Cause:** Two stories modified the same file in conflicting ways.

**Symptoms:**
- `nxd events --type STORY_MERGED` shows fewer merges than expected
- Error messages mentioning "conflict"

**Fix:**
1. In local mode, conflicts are auto-detected and the merge is aborted:
   ```bash
   # Check which stories have conflicts
   nxd status
   ```

2. Resolve manually:
   ```bash
   cd <worktree-path>
   git merge main
   # Fix conflicts
   git add .
   git commit
   ```

3. Prevention — ensure stories have proper dependency declarations so conflicting stories don't run in the same wave.

### "permission denied" errors with tmux

**Cause:** tmux server socket permissions or missing tmux installation.

**Fix:**
```bash
# Check tmux is installed
which tmux

# Kill any stale tmux server
tmux kill-server

# Start fresh
tmux new-session -d -s test && tmux kill-session -t test
```

### Out of memory (OOM) with large models

**Cause:** Model requires more RAM/VRAM than available.

**Symptoms:**
- Ollama crashes or returns errors
- System becomes unresponsive during model loading

**Fix:**
1. Use smaller models:
   ```yaml
   tech_lead: { provider: ollama, model: gemma4:e4b }
   ```

2. Ensure only one model is loaded at a time (Ollama default behavior)

3. Close other memory-intensive applications during NXD runs

4. Check Ollama memory usage:
   ```bash
   ollama ps
   ```

### Events not appearing in dashboard

**Cause:** Dashboard isn't connected to the right state directory, or events aren't being projected.

**Fix:**
```bash
# Verify events exist
nxd events --limit 5

# Verify config points to the right state dir
nxd config show | grep state_dir

# Check the database
sqlite3 ~/.nxd/nxd.db "SELECT COUNT(*) FROM stories;"
```

### "empty diff for story" in review

**Cause:** The implementing agent didn't commit any changes to the feature branch.

**Fix:**
1. Check the worktree for the story:
   ```bash
   ls ~/.nxd/worktrees/
   cd ~/.nxd/worktrees/nxd-req01-junior-1/
   git log --oneline -5
   git diff main
   ```

2. If the agent didn't produce code, it may have been stuck or the task was unclear. Check agent events:
   ```bash
   nxd events --story <story-id>
   ```

## Diagnostic Commands

```bash
# System health
nxd config validate           # Check config is valid
ollama list                    # Check available models
ollama ps                      # Check running models
tmux list-sessions             # Check active sessions

# State inspection
nxd status                     # Overview of all requirements
nxd status --req <id>          # Detailed status for one requirement
nxd agents                     # All agents and their status
nxd agents --status stuck      # Find stuck agents
nxd escalations                # View escalations
nxd events --limit 50          # Recent events

# Cleanup
nxd gc --dry-run               # Preview cleanup
nxd gc                         # Run cleanup

# Direct database inspection
sqlite3 ~/.nxd/nxd.db ".tables"
sqlite3 ~/.nxd/nxd.db "SELECT id, status FROM stories;"
sqlite3 ~/.nxd/nxd.db "SELECT type, COUNT(*) FROM events GROUP BY type;"
```

## Getting Help

- GitHub Issues: https://github.com/tzone85/nexus-dispatch/issues
- Check `nxd events` for detailed event history
- Inspect `~/.nxd/events.jsonl` for the raw event log
- Check tmux sessions directly: `tmux attach -t <session-name>`

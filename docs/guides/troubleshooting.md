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

# Pull the missing model
ollama pull deepseek-coder-v2:latest
```

**Tip:** Model names in `nxd.yaml` must exactly match Ollama tags. Use `ollama list` to see exact names.

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
     model: deepseek-coder-v2:latest  # 16B is minimum for good planning
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
   tech_lead: { provider: ollama, model: qwen2.5-coder:7b }
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

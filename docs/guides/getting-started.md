# Getting Started with NXD

This guide walks you through installing NXD, setting up your local AI stack, and running your first fully autonomous requirement.

## Prerequisites

### 1. Install Go 1.23+

```bash
# macOS
brew install go

# Linux
wget https://go.dev/dl/go1.23.0.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.23.0.linux-amd64.tar.gz
export PATH=$PATH:/usr/local/go/bin
```

Verify: `go version` should show 1.23 or higher.

### 2. Install Ollama

Ollama is the local LLM engine that powers all AI operations in NXD.

```bash
# macOS
brew install ollama

# Linux
curl -fsSL https://ollama.com/install.sh | sh
```

Start the Ollama server:

```bash
ollama serve
```

This runs in the background on `http://localhost:11434`. Keep this terminal open or run it as a service.

### 3. Pull a Model

You only need **one model** to run the full NXD pipeline. The default is **Gemma 4 26B MoE** -- a 26B Mixture-of-Experts model with only 3.8B active parameters per token, giving strong coding performance (77.1% LiveCodeBench) on consumer hardware.

```bash
# Recommended (~18GB download) — 24GB+ RAM
ollama pull gemma4:26b
```

Set all agent roles to the same model in `nxd.yaml` and the complete pipeline (planning, execution, review, QA, merge) works end to end.

**Smaller hardware?** Pull a lighter Gemma 4 variant:

```bash
# 16GB RAM — good function calling, lower code quality
ollama pull gemma4:e4b

# Minimal devices (4GB RAM)
ollama pull gemma4:e2b
```

**Want maximum quality?** Pull the dense 31B model for leadership roles:

```bash
ollama pull gemma4:31b   # Dense 31B (~20GB, needs 64GB+ RAM)
ollama pull gemma4:26b   # MoE 26B for workers
```

See [Gemma 4 Guide](gemma-4-guide.md) for detailed setup and hardware tuning, or [Model Selection](model-selection.md) for all model recommendations per role and hardware tier.

### 4. Install tmux

NXD runs agent sessions inside tmux for isolation and monitoring.

```bash
# macOS
brew install tmux

# Ubuntu/Debian
sudo apt install tmux
```

### 5. Install a Coding Runtime (Optional)

The default coding runtime is [Aider](https://github.com/paul-gauthier/aider):

```bash
pip install aider-chat
```

Aider connects to Ollama automatically when configured in `nxd.yaml`.

## Installation

```bash
go install github.com/tzone85/nexus-dispatch/cmd/nxd@latest
```

Or build from source:

```bash
git clone https://github.com/tzone85/nexus-dispatch.git
cd nexus-dispatch
make build
make install
```

#### macOS: Additional Setup

On macOS you may need to complete these extra steps before `make install` and `nxd` will work:

**1. Create the Go bin directory** (if it doesn't already exist):

```bash
mkdir -p "$(go env GOPATH)/bin"
```

**2. Add it to your PATH** by appending this line to `~/.zshrc` (or `~/.bash_profile` for Bash):

```bash
export PATH="$HOME/go/bin:$PATH"
```

**3. Reload your shell** so the PATH change takes effect:

```bash
source ~/.zshrc
```

Then re-run `make install` and proceed to verification below.

> **Note:** Windows setup may differ — refer to the [Go installation docs](https://go.dev/doc/install) for platform-specific guidance on configuring `GOPATH` and `PATH`.

Verify: `nxd --version` should show `0.1.0`.

## Configuration

## First Run

### Step 1: Initialize Workspace

```bash
nxd init
```

This creates:
- `~/.nxd/` — state directory
- `~/.nxd/events.jsonl` — append-only event log
- `~/.nxd/nxd.db` — SQLite projection store
- `~/.nxd/logs/` — agent session logs
- `~/.nxd/worktrees/` — git worktree checkout directory
- `nxd.yaml` — configuration file (in current directory)

You should see:

```
Initialized NXD workspace at /Users/you/.nxd
  Created directories: /Users/you/.nxd, logs, worktrees
  Event store: /Users/you/.nxd/events.jsonl
  Projection DB: /Users/you/.nxd/nxd.db
  Config: nxd.yaml

Ollama detected and running.

Run 'nxd req "<requirement>"' to submit your first requirement.
```

If you see "Ollama not detected", make sure `ollama serve` is running.

### Step 2: Submit a Requirement

Navigate to your project directory, then:

```bash
cd ~/projects/my-app
nxd req "Add user authentication with JWT tokens, login/register endpoints, and password hashing"
```

NXD will:
1. Emit a `REQ_SUBMITTED` event
2. Call the Tech Lead model (Gemma 4 26B) to decompose the requirement
3. Create stories with Fibonacci complexity scores
4. Build a dependency graph
5. Print the plan

Example output:

```
Requirement submitted: req-01HZ...
Planning with Tech Lead (gemma4:26b)...

Stories created:
  [1] story-01 | Add User model with password hashing      | Complexity: 2 | Deps: none
  [2] story-02 | Create JWT token generation utility        | Complexity: 3 | Deps: none
  [3] story-03 | Implement register endpoint                | Complexity: 3 | Deps: story-01
  [4] story-04 | Implement login endpoint with JWT response | Complexity: 5 | Deps: story-01, story-02
  [5] story-05 | Add auth middleware for protected routes   | Complexity: 3 | Deps: story-02

Dependency waves:
  Wave 1: story-01, story-02 (parallel)
  Wave 2: story-03, story-05 (parallel, after wave 1)
  Wave 3: story-04 (after wave 2)

Run 'nxd status --req req-01HZ...' to track progress.
```

### Step 3: Monitor Progress

```bash
# Text-based status
nxd status

# Live TUI dashboard
nxd dashboard

# Browser-based web dashboard
nxd dashboard --web
```

The TUI dashboard shows all sections at once in a single pane: agents, a pipeline summary bar with progress, a scrollable stories table, the activity log, and collapsible escalations. Use `j`/`k` to scroll stories, `w` to open the web dashboard, and `q` to quit.

The web dashboard opens at `http://localhost:8787` (change port with `--port`). It updates in real time via WebSocket and provides a full control panel: pause/resume requirements, retry/reassign/escalate stories, kill agents, and edit story details.

### Step 4: Check Events

```bash
# Last 20 events
nxd events --limit 20

# Filter by story
nxd events --story story-01

# Filter by event type
nxd events --type STORY_MERGED
```

### Step 5: Clean Up After Completion

```bash
# Preview what would be cleaned up
nxd gc --dry-run

# Actually clean up
nxd gc
```

## What Happens Behind the Scenes

When you run `nxd req`, the following pipeline executes:

```
1. INTAKE       Your requirement text -> REQ_SUBMITTED event
2. PLANNING     Tech Lead LLM decomposes -> stories + dependency DAG
3. DISPATCH     Topo sort -> wave 1 stories assigned to agents
4. EXECUTION    Each agent gets: tmux session + git worktree + Aider
5. REVIEW       Senior LLM reviews the git diff
6. QA           Lint -> Build -> Test (local shell commands)
7. MERGE        Local git merge into base branch (or GitHub PR)
8. CLEANUP      Delete worktree, archive logs, defer branch GC
```

Waves repeat until all stories are merged. If an agent gets stuck, the Watchdog detects it (via screen fingerprinting) and escalates.

## Generating the Demo GIF (optional)

If you want to generate the animated demo GIF, you'll need [VHS](https://github.com/charmbracelet/vhs) along with its dependencies `ffmpeg` and `ttyd`. On macOS:

```bash
brew install vhs ffmpeg ttyd
vhs docs/demo.tape
```

This produces `docs/demo.gif` showing the full `nxd init` through `nxd dashboard` workflow.

## Alternative Models (Legacy)

If you prefer DeepSeek or Qwen models instead of Gemma 4, they still work with NXD:

```bash
ollama pull deepseek-coder-v2:latest  # Tech Lead + Supervisor (~9GB)
ollama pull qwen2.5-coder:32b        # Senior (~20GB, needs 24GB+ VRAM)
ollama pull qwen2.5-coder:14b        # Intermediate + QA (~9GB)
ollama pull qwen2.5-coder:7b         # Junior (~4.5GB)
```

These models do not support native function calling, so NXD falls back to text-based JSON parsing. See [Model Selection](model-selection.md) for configuration details.

## Next Steps

- [Gemma 4 Guide](./gemma-4-guide.md) — detailed Gemma 4 setup, hardware tuning, and features
- [Configuration Guide](./configuration.md) — customize models, routing, runtimes
- [Architecture Deep Dive](./architecture.md) — understand event sourcing, agent hierarchy
- [Troubleshooting](./troubleshooting.md) — common issues and fixes
- [Model Selection Guide](./model-selection.md) — pick the right models for your hardware
- [Migration Guide](./migration-from-v0.md) — upgrading from DeepSeek/Qwen configurations

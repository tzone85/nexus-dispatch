# Migration Guide: v0 to v1 (two-model split)

NXD's recommended setup is a **two-model split**: `qwen3-coder:30b` for reviewer/planner roles (32GB+ machines; budget: `qwen2.5-coder:14b` on 24GB) and `gemma4:e4b` for coder roles. This guide covers upgrading from earlier single-model defaults (DeepSeek, single-Qwen, or all-Gemma).

## What Changed

| Aspect          | Before (v0)                                | Now                                                              |
|-----------------|--------------------------------------------|--------------------------------------------------------------------|
| Default models  | DeepSeek / single Qwen / all-Gemma         | `qwen3-coder:30b` (reviewer) + `gemma4:e4b` (coder)              |
| Schema version  | No `version` field                         | `version: "1.0"` (pinned; older configs run in compat mode)|
| Output format   | Free-text JSON parsing                     | Native function calling on Gemma side                      |
| Coding runtime  | Aider only                                 | Aider + native `gemma` runtime (criteria-gated completion) |
| Memory layer    | None                                       | MemPalace (offline-first, pinned `mempalace==2.0.0`)       |
| Same-model rule | No warning                                 | Logs `WARNING` if senior == junior model (informational)   |

## Your Existing Config Still Works

Older `nxd.yaml` files load unchanged. NXD logs a one-line hint suggesting you pin `version: "1.0"`, and a `same-model review` warning if every role uses the same model — both are informational, neither blocks startup. Function calling auto-falls back to text parsing for non-Gemma models.

## Step-by-Step Migration (recommended)

```bash
# 1. Pull both recommended models (32GB+ machines)
ollama pull qwen3-coder          # reviewer/planner (~19GB)
ollama pull gemma4:e4b           # coder (~6GB)
# On 24GB machines, use: ollama pull qwen2.5-coder:14b instead of qwen3-coder

# 2. Install MemPalace (offline-first, no network calls)
make install-mempalace     # or: pip install -r requirements.txt
make mempalace-check

# 3. Backup config
cp nxd.yaml nxd.yaml.backup

# 4. Get fresh defaults (delete old config first)
rm nxd.yaml && nxd init

# 5. Validate
nxd config validate
nxd status
```

## Config Diff

**Before (single-model):**
```yaml
models:
  tech_lead:    { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 16000 }
  senior:       { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 8000 }
  intermediate: { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 4000 }
  junior:       { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 4000 }
```

**After (two-model split, recommended for 32GB+ machines):**
```yaml
version: "1.0"
models:
  tech_lead:    { provider: ollama, model: qwen3-coder:30b, max_tokens: 16000 }
  senior:       { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  qa:           { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  supervisor:   { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
memory:
  enabled: true
```

**24GB budget alternative** (same principle, smaller reviewer):
```yaml
version: "1.0"
models:
  tech_lead:    { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 16000 }
  senior:       { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  intermediate: { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  qa:           { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  supervisor:   { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
memory:
  enabled: true
```

## On Single-GPU Machines

The two-model split adds ~3-5s per role swap on a single GPU. If that's a problem:
- **64GB+ RAM:** pin both with `OLLAMA_KEEP_ALIVE=24h OLLAMA_MAX_LOADED_MODELS=2` — eliminates swap.
- **16GB RAM:** stay on single-model `gemma4:e4b`. Accept the `same-model review` warning. See [Model Selection](model-selection.md#single-model-mode-16gb-ram-or-throughput-priority).

## Rollback

```bash
cp nxd.yaml.backup nxd.yaml
nxd config validate
```

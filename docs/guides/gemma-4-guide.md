# Gemma 4 Guide

NXD uses Google's Gemma 4 family as the **coder** in its default two-model split. This guide covers setup, hardware tuning, and how Gemma fits into the recommended workflow.

> [!IMPORTANT]
> **Gemma 4 is the coder, not the reviewer.** NXD's default setup pairs `gemma4:e4b` (junior/intermediate) with `qwen2.5-coder:14b` (senior/QA reviewer) — different model families catch different bugs. See [Model Selection](model-selection.md) for the full rationale.

## Why Gemma 4 (as the coder)

- **Native function calling**: Dedicated tool-call tokens for structured agent outputs — no JSON parsing heuristics
- **MoE efficiency**: 26B variant uses only 3.8B active params per token — fast inference
- **Strong coding**: 77.1% on LiveCodeBench (`gemma4:26b`), 1718 Codeforces ELO
- **256K context window**: Handles large codebases and long conversations
- **Apache 2.0 license**: Fully open, no usage restrictions
- **Runs locally via Ollama**: No API keys, no costs, full privacy

## Quick Start (recommended split)

```bash
# 1. Pull both models — reviewer + coder
ollama pull qwen2.5-coder:14b    # senior / QA reviewer (~9GB)
ollama pull gemma4:e4b           # junior / intermediate coder (~6GB)

# 2. Initialize NXD (writes the recommended split into nxd.yaml)
nxd init

# 3. Submit your first requirement
nxd req "Build a REST API for user management with CRUD endpoints"

# 4. Monitor progress
nxd status
nxd dashboard
```

## Single-Model Gemma (16GB RAM laptop)

If you don't have VRAM for two models, use `gemma4:e4b` for everything. NXD will print a `same-model review` warning at startup — that's expected:

```yaml
models:
  tech_lead:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  senior:       { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  intermediate: { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  junior:       { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  qa:           { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  supervisor:   { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
```

## Google AI Free Tier (Optional)

NXD can use Google AI Studio as a fast cloud primary, falling back to local Ollama when the free tier quota is exhausted.

1. Get an API key at [ai.google.dev](https://ai.google.dev)
2. Set it: `export GOOGLE_AI_API_KEY=your-key-here`
3. NXD uses Google AI first, switches to Ollama on quota hit (HTTP 429)
4. After cooldown (default 60s), retries Google AI

If you don't set the API key, NXD uses Ollama only. This is the recommended offline-first setup.

## Hardware Guide

| Setup            | RAM   | Models                              | Disk    | Notes                                            |
|------------------|-------|-------------------------------------|---------|--------------------------------------------------|
| Minimal          | 16GB  | `gemma4:e4b` (single)               | ~6GB    | Function calling works, same-model warning       |
| **Recommended**  | 24GB+ | `qwen2.5-coder:14b` + `gemma4:e4b`  | ~15GB   | **Two-model split, best blind-spot coverage**    |
| Heavy            | 64GB+ | `qwen2.5-coder:32b` + `gemma4:26b`  | ~38GB   | Pin both in VRAM via `OLLAMA_KEEP_ALIVE=24h`     |

### Apple Silicon Notes

Ollama uses unified memory. The recommended split (qwen 14b + gemma4 e4b) at Q4_K_M uses ~15GB total, leaving headroom for macOS on 24GB machines.

```bash
export OLLAMA_KEEP_ALIVE=24h          # Keep both models loaded — kills GPU swap latency
export OLLAMA_MAX_LOADED_MODELS=2     # Allow both reviewer + coder resident
```

Without `MAX_LOADED_MODELS=2`, Ollama swaps the inactive model out and you pay the swap penalty on every role change.

## Function Calling

NXD defines tools per role (e.g., `create_story`, `submit_review`). Gemma 4 responds with structured JSON tool calls instead of free text — more reliable, no parsing ambiguity.

For non-Gemma models, NXD falls back to text-based JSON parsing automatically. See [Function Calling Reference](function-calling.md).

## Choosing a Runtime

| Feature       | `gemma` (Native)                          | `aider`                          |
|---------------|-------------------------------------------|----------------------------------|
| Dependencies  | None (built into NXD)                     | Requires `pip install aider-chat`|
| Model support | Gemma 4 family                            | Many models                      |
| Code editing  | Tool-call based                           | Edit-format based                |
| Safety        | Command allowlist + path restriction      | Aider built-in                   |
| Completion    | Criteria-gated (build/vet/test must pass) | Aider commit-and-go              |

The native `gemma` runtime auto-selects for Gemma 4 models. It also enforces **criteria-gated completion**: agents cannot declare a story done until `go build`, `go vet`, and `go test` (or your project's configured criteria) all pass in the worktree. See `qa.success_criteria` in [Configuration](configuration.md).

## Model Size Guide

| Model         | Active Params | VRAM   | LiveCodeBench | Best For                              |
|---------------|---------------|--------|---------------|---------------------------------------|
| `gemma4:e2b`  | 2.3B          | ~4GB   | 44%           | Very constrained devices              |
| `gemma4:e4b`  | 4.5B          | ~6GB   | 52%           | **Default coder** — 16-24GB machines  |
| `gemma4:26b`  | 3.8B (MoE)    | ~10GB  | 77.1%         | Heavy coder, 24GB+ if paired with qwen|
| `gemma4:31b`  | 30.7B         | ~17GB  | 80%           | 64GB+ machines                        |

## Troubleshooting

**Model too large**: Use `gemma4:e4b` or set `OLLAMA_MAX_LOADED_MODELS=1`.

**Slow first inference**: Normal (model loading). Set `OLLAMA_KEEP_ALIVE=24h`.

**Model swap takes 3-5s on every role change**: That's the cost of the two-model split. Pin both with `OLLAMA_MAX_LOADED_MODELS=2 OLLAMA_KEEP_ALIVE=24h` if you have VRAM, or fall back to single-model mode if you don't.

**Google AI 429 errors**: Free tier exhausted. Auto-falls back to Ollama. Cooldown resets after 60s.

**Ollama not detected**: Run `ollama serve`. Check: `curl http://localhost:11434/api/tags`.

**Function calling not working**: Verify Ollama >= 0.20: `ollama --version`.

**`same-model review` warning at startup**: Expected if you're using single-model mode. Otherwise, check that `models.senior.model` differs from `models.junior.model` — see [Model Selection](model-selection.md).

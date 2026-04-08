# Gemma 4 Guide

NXD now defaults to Google's Gemma 4 26B MoE model for all agent roles. This guide covers setup, hardware tuning, and key features.

## Why Gemma 4

- **MoE efficiency**: 26B total params but only 3.8B active per token — fast inference on consumer hardware
- **Strong coding**: 77.1% on LiveCodeBench, 1718 Codeforces ELO
- **Native function calling**: Dedicated tool-call tokens for structured agent outputs
- **256K context window**: Handles large codebases and long conversations
- **Apache 2.0 license**: Fully open, no usage restrictions
- **Runs locally via Ollama**: No API keys, no costs, full privacy

## Quick Start

```bash
# 1. Pull the model (~18GB download)
ollama pull gemma4:26b

# 2. Initialize NXD
nxd init

# 3. Submit your first requirement
nxd req "Build a REST API for user management with CRUD endpoints"

# 4. Monitor progress
nxd status
nxd dashboard
```

## Google AI Free Tier (Optional)

NXD can use Google AI Studio as a fast cloud primary, falling back to local Ollama when the free tier quota is exhausted.

1. Get an API key at [ai.google.dev](https://ai.google.dev)
2. Set it: `export GOOGLE_AI_API_KEY=your-key-here`
3. NXD uses Google AI first, switches to Ollama on quota hit (HTTP 429)
4. After cooldown (default 60s), retries Google AI

If you don't set the API key, NXD uses Ollama only. This is the recommended offline-first setup.

## Hardware Guide

| Setup | RAM | Model | Disk | Notes |
|-------|-----|-------|------|-------|
| Minimal | 16GB | `gemma4:e4b` | ~10GB | Function calling works, lower code quality |
| **Recommended** | **24GB** | **`gemma4:26b`** | **~18GB** | **Best quality/performance ratio** |
| Full Team | 64GB+ | `gemma4:31b` + `gemma4:26b` | ~38GB | Maximum quality |

### Apple Silicon Notes

Ollama uses unified memory. The 26B MoE at Q4_K_M uses ~10GB total, leaving ~14GB for macOS on 24GB machines.

```bash
export OLLAMA_KEEP_ALIVE=30m          # Keep model loaded
export OLLAMA_MAX_LOADED_MODELS=1     # Save memory
```

## Function Calling

NXD defines tools per role (e.g., `create_story`, `submit_review`). Gemma 4 responds with structured JSON tool calls instead of free text — more reliable, no parsing ambiguity.

For non-Gemma models, NXD falls back to text-based JSON parsing automatically. See [Function Calling Reference](function-calling.md).

## Choosing a Runtime

| Feature | `gemma` (Native) | `aider` |
|---------|-------------------|---------|
| Dependencies | None (built into NXD) | Requires `pip install aider-chat` |
| Model support | Gemma 4 family | Many models |
| Code editing | Tool-call based | Edit-format based |
| Safety | Command allowlist, path restriction | Aider built-in |

The native `gemma` runtime auto-selects for Gemma 4 models.

## Model Size Guide

| Model | Active Params | VRAM | LiveCodeBench | Best For |
|-------|--------------|------|---------------|----------|
| `gemma4:e2b` | 2.3B | ~4GB | 44% | Very constrained devices |
| `gemma4:e4b` | 4.5B | ~6GB | 52% | 16GB machines |
| `gemma4:26b` | 3.8B (MoE) | ~10GB | 77.1% | 24GB+ (recommended) |
| `gemma4:31b` | 30.7B | ~17GB | 80% | 64GB+ machines |

## Troubleshooting

**Model too large**: Use `gemma4:e4b` or set `OLLAMA_MAX_LOADED_MODELS=1`.

**Slow first inference**: Normal (model loading). Set `OLLAMA_KEEP_ALIVE=30m`.

**Google AI 429 errors**: Free tier exhausted. Auto-falls back to Ollama. Cooldown resets after 60s.

**Ollama not detected**: Run `ollama serve`. Check: `curl http://localhost:11434/api/tags`.

**Function calling not working**: Verify Ollama >= 0.20: `ollama --version`.

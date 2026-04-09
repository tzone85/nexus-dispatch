# Model Selection Guide

Choosing the right local models is critical to NXD's performance. This guide helps you pick models based on your hardware and quality requirements.

## Gemma 4 (Recommended)

NXD defaults to Google's **Gemma 4** model family. These models offer native function calling, strong coding benchmarks, and efficient inference. See the [Gemma 4 Guide](gemma-4-guide.md) for detailed setup.

| Model | Params | Active Params | RAM | LiveCodeBench | Best For |
|-------|--------|--------------|-----|---------------|----------|
| `gemma4:26b` | 26B MoE | 3.8B | 12GB+ | 77.1% | **Recommended for all roles** |
| `gemma4:31b` | 31B dense | 30.7B | 20GB+ | 80% | Best quality (leadership roles) |
| `gemma4:e4b` | 4.5B | 4.5B | 6GB | 52% | Lightweight / 16GB machines |
| `gemma4:e2b` | 2.3B | 2.3B | 4GB | 44% | Minimal devices |

**Key advantages over legacy models:**
- Native function calling with structured tool-call tokens (no JSON parsing heuristics)
- 256K context window (vs 4K-32K for DeepSeek/Qwen)
- MoE architecture gives near-32B quality at ~7B inference cost
- Apache 2.0 license with no usage restrictions

## Hardware Tiers

### Tier 1: Minimum (16GB RAM)

Run `gemma4:e4b` for all roles. Suitable for sequential story execution.

```yaml
models:
  tech_lead:     { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  senior:        { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  intermediate:  { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  qa:            { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  supervisor:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
```

**What to expect:**
- Planning works but may produce overly simple decompositions
- Function calling works (structured tool outputs)
- Junior tasks (complexity 1-3) work well
- Best for: small projects, learning NXD, constrained hardware

### Tier 2: Recommended (24GB+ RAM)

Run `gemma4:26b` for all roles. The MoE architecture keeps active parameters low (~3.8B) while delivering strong results.

```yaml
models:
  tech_lead:     { provider: ollama, model: gemma4:26b, max_tokens: 16000 }
  senior:        { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  qa:            { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
```

**What to expect:**
- Good planning with meaningful story decomposition
- Native function calling for reliable structured outputs
- All task complexities handled well
- Best for: daily development, medium-complexity projects

### Tier 3: Full Team (64GB+ RAM)

Use `gemma4:31b` (dense) for leadership roles and `gemma4:26b` (MoE) for workers.

```yaml
models:
  tech_lead:     { provider: ollama, model: gemma4:31b, max_tokens: 16000 }
  senior:        { provider: ollama, model: gemma4:31b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:26b, max_tokens: 4000 }
  qa:            { provider: ollama, model: gemma4:26b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: gemma4:31b, max_tokens: 4000 }
```

**What to expect:**
- High-quality planning with proper architecture considerations
- Thorough code reviews (80% LiveCodeBench on dense 31B)
- Parallel wave execution with efficient MoE workers
- Best for: complex projects, production-quality output

## Model Recommendations by Role

### Tech Lead (Planning + Decomposition)

The most demanding role -- needs strong reasoning to decompose requirements into properly-scoped stories with accurate complexity scores and dependency graphs.

| Model | Size | Quality | Speed |
|-------|------|---------|-------|
| **gemma4:31b** | 31B dense | Best | Moderate |
| **gemma4:26b** | 26B MoE | Good | Fast |
| gemma4:e4b | 4.5B | Basic | Fast |

**Key skill:** Structured JSON output. Gemma 4 models use native function calling to return valid JSON tool calls, eliminating parsing failures common with text-based approaches.

### Senior (Code Review)

Reviews git diffs against acceptance criteria. Needs to understand code patterns, spot bugs, and assess test coverage.

| Model | Size | Quality | Speed |
|-------|------|---------|-------|
| **gemma4:31b** | 31B dense | Best | Moderate |
| **gemma4:26b** | 26B MoE | Good | Fast |
| gemma4:e4b | 4.5B | Basic | Fast |

**Key skill:** Code comprehension. The reviewer reads full diffs and must understand multi-file changes. The 256K context window handles large diffs well.

### Junior / Intermediate (Implementation)

These agents write code via the native Gemma runtime or Aider in tmux sessions. The model needs to understand the codebase and produce working, tested code.

| Model | Size | Best For |
|-------|------|----------|
| **gemma4:26b** | 26B MoE | All task complexities (recommended) |
| gemma4:e4b | 4.5B | Simple tasks (complexity 1-3) |
| gemma4:e2b | 2.3B | Trivial tasks on constrained devices |

**Key skill:** Code generation with proper imports, error handling, and test writing.

### QA (Test Analysis)

Runs lint/build/test commands and interprets results. Mostly shell-driven, but uses LLM for failure analysis.

| Model | Size | Notes |
|-------|------|-------|
| **gemma4:26b** | 26B MoE | Good balance of speed and analysis |
| gemma4:e4b | 4.5B | Sufficient since QA is mostly shell commands |

### Supervisor (Drift Detection)

Periodically reviews whether stories are on track. Needs to compare story progress against the original requirement.

| Model | Size | Notes |
|-------|------|-------|
| **gemma4:31b** | 31B dense | Best reasoning for drift detection |
| **gemma4:26b** | 26B MoE | Good for most projects |

## Pulling Models

```bash
# List available models
ollama list

# Pull the recommended model
ollama pull gemma4:26b

# Check model details
ollama show gemma4:26b

# Remove an unused model (free disk space)
ollama rm gemma4:e4b
```

## Performance Tips

### 1. GPU Offloading

Ollama automatically uses your GPU if available. Check with:
```bash
ollama ps  # Shows running models and GPU layers
```

For NVIDIA GPUs, ensure CUDA drivers are installed. For Apple Silicon, Metal acceleration is automatic.

### 2. Model Concurrency

Ollama loads one model at a time by default. When NXD switches between roles (e.g., Junior -> Reviewer), Ollama swaps models. This takes 5-30 seconds depending on model size.

To reduce swapping, use the same model for multiple roles (this is the default with Gemma 4):
```yaml
# All roles use gemma4:26b — no model swapping at all
senior:        { provider: ollama, model: gemma4:26b }
intermediate:  { provider: ollama, model: gemma4:26b }
qa:            { provider: ollama, model: gemma4:26b }
```

### 3. Quantization

Ollama models are typically Q4_K_M quantized (good quality/size balance). The Gemma 4 26B MoE at Q4_K_M uses ~10GB, fitting comfortably on 24GB machines.

### 4. Context Length

Gemma 4 supports a 256K context window, which handles most codebases comfortably. For very large projects:
- Keep stories small (complexity 1-5)
- Use clear, focused acceptance criteria
- Let NXD's wave dispatch handle orchestration

## Comparison: Local vs Cloud

| Aspect | Local (Ollama) | Cloud (Anthropic/OpenAI) |
|--------|---------------|--------------------------|
| Cost per token | $0 | $3-75 per million tokens |
| Latency (first token) | 1-5s | 0.5-2s |
| Throughput | 10-50 tok/s | 50-100 tok/s |
| Context window | 4K-32K | 128K-200K |
| Code quality (planning) | Good (16B+) | Excellent |
| Code quality (implementation) | Good (7B+) | Excellent |
| Privacy | Full | Data leaves machine |
| Availability | Always (if hardware works) | Depends on API uptime |

## Recommended Upgrade Path

1. Start with `gemma4:26b` for all roles (recommended default)
2. On 16GB machines, start with `gemma4:e4b` for everything
3. Upgrade Tech Lead and Senior to `gemma4:31b` when planning/review quality matters (64GB+ RAM)
4. Enable Google AI free tier for fast cloud primary with Ollama fallback
5. Switch to cloud models (Anthropic/OpenAI) for specific roles if needed (hybrid mode)

## Legacy Models (Alternative)

DeepSeek Coder V2 and Qwen 2.5 Coder are still supported but are no longer the default. These models lack native function calling, so NXD uses text-based JSON parsing (less reliable for structured outputs).

| Model | Size | Notes |
|-------|------|-------|
| `deepseek-coder-v2:latest` | 16B | Previously recommended for Tech Lead/Supervisor |
| `qwen2.5-coder:32b` | 32B | Previously recommended for Senior |
| `qwen2.5-coder:14b` | 14B | Previously recommended for Intermediate/QA |
| `qwen2.5-coder:7b` | 7B | Previously recommended for Junior |

To use legacy models, set them explicitly in `nxd.yaml`. See [Migration Guide](migration-from-v0.md) for details on switching from legacy to Gemma 4.

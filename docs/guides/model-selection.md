# Model Selection Guide

Choosing the right local models is critical to NXD's performance. This guide helps you pick models based on your hardware and quality requirements.

## Hardware Tiers

### Tier 1: Minimum (16GB RAM, 8GB VRAM)

Run one model at a time. Suitable for sequential story execution.

```yaml
models:
  tech_lead:     { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  senior:        { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  intermediate:  { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  junior:        { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  supervisor:    { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
```

**What to expect:**
- Planning works but may produce overly simple decompositions
- Junior tasks (complexity 1-3) work well
- Code review quality is basic
- Best for: small projects, learning NXD, constrained hardware

### Tier 2: Recommended (32GB RAM, 16GB VRAM)

Run 14B models for most roles with 7B for workers.

```yaml
models:
  tech_lead:     { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 16000 }
  senior:        { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  junior:        { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 4000 }
```

**What to expect:**
- Good planning with meaningful story decomposition
- Decent code review catching real issues
- Junior/Intermediate tasks are reliable
- Best for: daily development, medium-complexity projects

### Tier 3: Full Team (64GB+ RAM, 24GB+ VRAM)

Run the full recommended model set.

```yaml
models:
  tech_lead:     { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 16000 }
  senior:        { provider: ollama, model: qwen2.5-coder:32b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 4000 }
  junior:        { provider: ollama, model: qwen2.5-coder:7b, max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: deepseek-coder-v2:latest, max_tokens: 4000 }
```

**What to expect:**
- High-quality planning with proper architecture considerations
- Thorough code reviews
- Parallel wave execution with different model sizes
- Best for: complex projects, production-quality output

## Model Recommendations by Role

### Tech Lead (Planning + Decomposition)

The most demanding role — needs strong reasoning to decompose requirements into properly-scoped stories with accurate complexity scores and dependency graphs.

| Model | Size | Quality | Speed |
|-------|------|---------|-------|
| deepseek-coder-v2:latest | 16B | Best | Moderate |
| qwen2.5-coder:14b | 14B | Good | Moderate |
| qwen2.5-coder:7b | 7B | Basic | Fast |

**Key skill:** Structured JSON output. The Tech Lead must return valid JSON arrays of stories. Larger models are significantly better at this.

### Senior (Code Review)

Reviews git diffs against acceptance criteria. Needs to understand code patterns, spot bugs, and assess test coverage.

| Model | Size | Quality | Speed |
|-------|------|---------|-------|
| qwen2.5-coder:32b | 32B | Best | Slow |
| qwen2.5-coder:14b | 14B | Good | Moderate |
| deepseek-coder-v2:latest | 16B | Good | Moderate |

**Key skill:** Code comprehension. The reviewer reads full diffs and must understand multi-file changes.

### Junior / Intermediate (Implementation)

These agents write code via Aider in tmux sessions. The model needs to understand the codebase and produce working, tested code.

| Model | Size | Best For |
|-------|------|----------|
| qwen2.5-coder:7b | 7B | Simple tasks (complexity 1-3) |
| qwen2.5-coder:14b | 14B | Medium tasks (complexity 4-5) |
| qwen2.5-coder:32b | 32B | Complex tasks (complexity 6+) |

**Key skill:** Code generation with proper imports, error handling, and test writing.

### QA (Test Analysis)

Runs lint/build/test commands and interprets results. Mostly shell-driven, but uses LLM for failure analysis.

| Model | Size | Notes |
|-------|------|-------|
| qwen2.5-coder:14b | 14B | Good balance of speed and analysis |
| qwen2.5-coder:7b | 7B | Sufficient since QA is mostly shell commands |

### Supervisor (Drift Detection)

Periodically reviews whether stories are on track. Needs to compare story progress against the original requirement.

| Model | Size | Notes |
|-------|------|-------|
| deepseek-coder-v2:latest | 16B | Best reasoning for drift detection |
| qwen2.5-coder:14b | 14B | Acceptable for simple projects |

## Pulling Models

```bash
# List available models
ollama list

# Pull a specific model
ollama pull qwen2.5-coder:7b

# Check model details
ollama show qwen2.5-coder:7b

# Remove an unused model (free disk space)
ollama rm qwen2.5-coder:32b
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

To reduce swapping, use the same model for multiple roles:
```yaml
# All non-planning roles use the same 14B model
senior:        { provider: ollama, model: qwen2.5-coder:14b }
intermediate:  { provider: ollama, model: qwen2.5-coder:14b }
qa:            { provider: ollama, model: qwen2.5-coder:14b }
```

### 3. Quantization

Ollama models are typically Q4_K_M quantized (good quality/size balance). For higher quality at the cost of more RAM:

```bash
# Pull the full-precision version if available
ollama pull qwen2.5-coder:14b-fp16
```

### 4. Context Length

Local models have limited context windows (typically 4K-32K tokens). For large codebases:
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

1. Start with `qwen2.5-coder:7b` for everything
2. Upgrade Tech Lead to `deepseek-coder-v2:latest` when planning quality matters
3. Upgrade Senior to `qwen2.5-coder:14b` for better reviews
4. Add `qwen2.5-coder:32b` for Senior when handling complex stories
5. Switch to cloud models for specific roles if needed (hybrid mode)

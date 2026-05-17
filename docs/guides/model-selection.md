# Model Selection Guide

Choosing the right local models is critical to NXD's output quality. This guide explains why NXD recommends a **two-model split** by default, when to deviate, and how to size models to your hardware.

> [!IMPORTANT]
> **Don't use the same model for `senior` and `junior` roles.** When the reviewer and the coder are the same model, the reviewer shares the coder's hallucinations and overconfidence — bad code gets approved because both sides have the same blind spots. NXD logs a `WARNING` at startup if you do this anyway; the warning is informational, not blocking.

![Two-model split: reviewer vs coder](../diagrams/two-model-split.svg)

## The Recommended Split (qwen3-coder + gemma4)

NXD's default — and what every new install should start with — is a two-model setup:

| Role           | Model               | Why                                                                       |
|----------------|---------------------|---------------------------------------------------------------------------|
| `tech_lead`    | `qwen3-coder:30b`   | 262K context, SWE-bench 51.6%; strongest open-source planner/decomposer   |
| `senior`       | `qwen3-coder:30b`   | Reviewer — different family from coder catches what the coder missed      |
| `qa`           | `qwen3-coder:30b`   | Deep reasoning on failure analysis; explains root causes, not just symptoms|
| `intermediate` | `gemma4:e4b`        | Coder — native function calling, fast, runs on modest VRAM                |
| `junior`       | `gemma4:e4b`        | Coder — same                                                              |
| `supervisor`   | `gemma4:e4b`        | Drift detection; lightweight role, same VRAM as coder stays warm          |

**Why two different families:**
- `qwen3-coder:30b` is a MoE model (3.3B active params) with 262K context window, trained for deep reasoning over large codebases. Despite 30B total weights, inference speed tracks its active params — closer to a 3-4B model.
- `gemma4` is a MoE model with **native function-calling tokens**, essential for the native Gemma runtime's tool-call loop.
- Their *failure modes don't overlap* — when gemma4 hallucinates an import, qwen3-coder catches it (and vice versa).

> [!NOTE]
> **Why not qwen3-coder as the coder?** As of mid-2025, Ollama has confirmed bugs in qwen3-coder's tool-calling template (malformed tool definitions, history stripping, XML fallback at >5 tools). The native Gemma runtime passes 6+ tools per turn. Until these Ollama issues are resolved, keep `gemma4` in the coder role.

**The trade-off:** GPU swap. On a single-GPU machine, Ollama only holds one model in VRAM at a time. Each pipeline transition (plan → code → review → QA) swaps models, adding **~3-5s per role change**. The blind-spot coverage is worth those seconds — but see [Single-Model Mode](#single-model-mode-16gb-ram-or-throughput-priority) below if your workload prefers raw throughput.

## Hardware Tiers

### Tier 1: Budget Two-Model (24GB RAM)

`qwen3-coder:30b` + `gemma4:e4b` needs ~25GB combined — just over the 24GB limit. On 24GB machines, use the smaller qwen2.5 reviewer instead:

```yaml
version: "1.0"
models:
  tech_lead:     { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 16000 }
  senior:        { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen2.5-coder:14b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: gemma4:e4b,        max_tokens: 4000 }
```

**What to expect:**
- ~3-5s extra per role swap on single-GPU
- Good review quality — `qwen2.5-coder:14b` is still a strong reviewer
- Reviewer catches coder mistakes the coder couldn't see
- Best for: 24GB machines that need the two-model split at lower disk cost (~15GB)

### Tier 2: Recommended (32GB+ RAM)

The recommended split. Both models fit on 32GB with VRAM headroom for macOS/OS overhead.

```yaml
version: "1.0"
models:
  tech_lead:     { provider: ollama, model: qwen3-coder:30b, max_tokens: 16000 }
  senior:        { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: gemma4:e4b,      max_tokens: 4000 }
```

**What to expect:**
- ~3-5s extra per role swap on single-GPU
- 262K context — the reviewer can ingest large repo diffs in full
- Meaningfully better review quality than qwen2.5-coder:14b (SWE-bench 51.6% vs ~34%)
- Best for: daily development, production-quality output, 32GB+ machines

### Tier 3: Heavy (64GB+ RAM, no compromises)

Pin both models in VRAM and upgrade the coder to the larger Gemma:

```yaml
models:
  tech_lead:     { provider: ollama, model: qwen3-coder:30b, max_tokens: 16000 }
  senior:        { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  intermediate:  { provider: ollama, model: gemma4:26b,      max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:26b,      max_tokens: 4000 }
  qa:            { provider: ollama, model: qwen3-coder:30b, max_tokens: 8000 }
  supervisor:    { provider: ollama, model: gemma4:26b,      max_tokens: 4000 }
```

Start Ollama with `OLLAMA_KEEP_ALIVE=24h` and pre-load both models at session start. Eliminates the GPU swap entirely.

### Single-Model Mode (16GB RAM, or throughput priority)

If you don't have VRAM for two models, or you genuinely want raw throughput on a known-simple project, fall back to a single small model:

```yaml
models:
  tech_lead:     { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  senior:        { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  intermediate:  { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  junior:        { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  qa:            { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
  supervisor:    { provider: ollama, model: gemma4:e4b, max_tokens: 4000 }
```

NXD will print a `same-model review` warning at startup. That's expected for this config. Accept the reduced hallucination detection in exchange for zero model-swap latency. Suitable for: laptops, learning NXD, small projects where bug rate is already low.

## Per-Role Sizing

### Tech Lead (Planning + Decomposition)

The most demanding role — decomposes requirements into properly-scoped stories with accurate complexity scores and dependency graphs.

| Model                  | Size       | Quality | Notes                                                  |
|------------------------|------------|---------|--------------------------------------------------------|
| `qwen3-coder:30b`      | 30B (MoE)  | Best    | **Default** for 32GB+ — 262K context, SWE-bench 51.6% |
| `qwen2.5-coder:14b`    | 14B        | Good    | Budget option for 24GB machines                        |
| `gemma4:26b` (MoE)     | 26B        | Good    | Alternative if you prefer single-family setups         |
| `gemma4:e4b`           | 4.5B       | Basic   | Acceptable for 16GB machines (single-model mode)       |

### Senior (Code Review)

Reviews git diffs against acceptance criteria. Quality matters here more than speed — a bad review approves broken code.

| Model               | Size      | Quality | Notes                                                     |
|---------------------|-----------|---------|-----------------------------------------------------------|
| `qwen3-coder:30b`   | 30B (MoE) | Best    | **Default** — explains root causes, 262K diff context     |
| `qwen2.5-coder:14b` | 14B       | Good    | Budget option, paired with gemma4 coder on 24GB machines  |
| `gemma4:31b` (dense)| 31B       | Good    | Only viable if junior uses a different family              |

### Junior / Intermediate (Implementation)

These agents write code via the native Gemma runtime. Native function calling is critical — it eliminates JSON parsing heuristics.

| Model         | Size  | Best For                                       |
|---------------|-------|------------------------------------------------|
| `gemma4:26b`  | 26B MoE | All task complexities; 24GB+ RAM             |
| `gemma4:e4b`  | 4.5B  | **Default** — fast, low VRAM, function calls  |
| `gemma4:e2b`  | 2.3B  | Trivial tasks on constrained devices          |

### QA (Test Analysis)

Runs lint/build/test and interprets results. Mostly shell-driven but uses LLM for failure analysis.

| Model               | Size      | Notes                                                        |
|---------------------|-----------|--------------------------------------------------------------|
| `qwen3-coder:30b`   | 30B (MoE) | **Default** — deep failure analysis, explains root causes    |
| `qwen2.5-coder:14b` | 14B       | Budget option — still strong on failure analysis             |
| `gemma4:e4b`        | 4.5B      | Sufficient for shell-driven QA on resource-constrained setup |

### Supervisor (Drift Detection)

Lightweight periodic role — compare story progress against the original requirement.

| Model                  | Size  | Notes                                                  |
|------------------------|-------|--------------------------------------------------------|
| `gemma4:e4b`           | 4.5B  | **Default** — same family as coder keeps VRAM warm     |
| `qwen2.5-coder:14b`    | 14B   | If you already have it loaded for senior anyway        |

## Pulling Models

```bash
# Recommended split — pull both (32GB+ machines)
ollama pull qwen3-coder          # reviewer/planner (~19GB)
ollama pull gemma4:e4b           # coder (~6GB)

# Budget split — pull both (24GB machines)
ollama pull qwen2.5-coder:14b
ollama pull gemma4:e4b

# List what's installed
ollama list

# Inspect a model
ollama show qwen2.5-coder:14b

# Free disk space
ollama rm gemma4:26b
```

## Performance Tips

### 1. GPU Offloading

Ollama automatically uses your GPU if available. Check with:
```bash
ollama ps   # shows running models + GPU layers
```

For NVIDIA, install CUDA drivers. Apple Silicon uses Metal automatically.

### 2. Reducing Model Swap

Default Ollama behavior unloads idle models after ~5 minutes. With the two-model split this means a swap on every role change.

To pin both models in VRAM (32GB+ recommended):
```bash
export OLLAMA_KEEP_ALIVE=24h
export OLLAMA_MAX_LOADED_MODELS=2
# pre-load at session start
ollama run qwen3-coder ""
ollama run gemma4:e4b ""
```

### 3. Quantization

Ollama models default to Q4_K_M (good quality/size balance). At Q4_K_M:
- `qwen3-coder:30b` ≈ 19GB VRAM (MoE — only 3.3B active params per forward pass)
- `gemma4:e4b` ≈ 6GB VRAM
- Combined ≈ 25GB — fits on 32GB+ cards

Budget alternative at Q4_K_M:
- `qwen2.5-coder:14b` ≈ 9GB VRAM
- `gemma4:e4b` ≈ 6GB VRAM
- Combined ≈ 15GB — fits on most 24GB cards

### 4. Context Length

`qwen3-coder:30b` supports 262K tokens natively (extendable to 1M with YaRN), `gemma4` supports 256K. Both handle large monorepos without chunking. For huge codebases:
- Keep stories small (complexity 1-5)
- Write clear, focused acceptance criteria
- Let NXD's wave dispatch handle orchestration

## Local vs Cloud

| Aspect                    | Local (Ollama)            | Cloud (Anthropic/OpenAI)         |
|---------------------------|---------------------------|----------------------------------|
| Cost per token            | $0                        | $3-75 per million tokens         |
| Latency (first token)     | 1-5s                      | 0.5-2s                           |
| Throughput                | 10-50 tok/s               | 50-100 tok/s                     |
| Context window            | 262K-256K (qwen3/gemma4)  | 128K-200K                        |
| Code quality (planning)   | Good (14B+)               | Excellent                        |
| Code quality (review)     | Good (14B+, different fam)| Excellent                        |
| Privacy                   | Full — offline-first      | Data leaves machine              |
| Availability              | Always (if hardware works)| Depends on API uptime + quota    |

## Upgrade Path

1. **16GB machines** — single-model mode with `gemma4:e4b`. Accept the same-model warning.
2. **24GB machines** — budget two-model split: `qwen2.5-coder:14b` + `gemma4:e4b` (~15GB).
3. **32GB+ machines** — recommended split: `qwen3-coder:30b` + `gemma4:e4b` (~25GB, meaningfully better review quality).
4. **64GB+ machines** — heavy tier: `qwen3-coder:30b` + `gemma4:26b` pinned in VRAM with `OLLAMA_KEEP_ALIVE=24h`.
5. Enable Google AI free tier for cloud primary with Ollama fallback (optional) — see [Configuration](configuration.md).
6. Switch specific roles to Anthropic/OpenAI only if quality requires it (hybrid mode).

## See Also

- [Configuration Guide](configuration.md) — full `nxd.yaml` reference
- [Gemma 4 Guide](gemma-4-guide.md) — native runtime details
- [Migration from v0](migration-from-v0.md) — legacy DeepSeek/Qwen setups

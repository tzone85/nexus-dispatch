# Migration Guide: v0 to Gemma 4

NXD now defaults to Gemma 4 26B MoE. This guide covers upgrading from DeepSeek/Qwen defaults.

## What Changed

| Aspect | Before (v0) | After |
|--------|-------------|-------|
| Default model | DeepSeek Coder V2 / Qwen2.5-Coder | Gemma 4 26B MoE |
| Provider | `ollama` | `google+ollama` |
| Output format | Free-text JSON parsing | Native function calling |
| Coding runtime | Aider only | Aider + native `gemma` runtime |

## Your Existing Config Still Works

Custom `nxd.yaml` with DeepSeek/Qwen models works unchanged. Function calling auto-falls back to text parsing for non-Gemma models.

## Step-by-Step Migration

```bash
# 1. Pull the new model
ollama pull gemma4:26b

# 2. Backup config
cp nxd.yaml nxd.yaml.backup

# 3. Get fresh defaults (delete old config first)
rm nxd.yaml && nxd init

# 4. Validate
nxd config validate
nxd status

# 5. (Optional) Google AI free tier
export GOOGLE_AI_API_KEY=your-key-here
```

## Config Diff

**Before:**
```yaml
models:
  tech_lead:
    provider: ollama
    model: deepseek-coder-v2:latest
    max_tokens: 16000
```

**After:**
```yaml
models:
  tech_lead:
    provider: google+ollama
    model: gemma4:26b
    google_model: gemma-4-26b-a4b-it
    max_tokens: 16000
    fallback_cooldown_s: 60
```

## Rollback

```bash
cp nxd.yaml.backup nxd.yaml
nxd config validate
```

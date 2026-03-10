# NXD Training Documentation

Welcome to the NXD (Nexus Dispatch) training guides. Whether you're a first-time user or a contributor looking to extend the system, start here.

## For Users

| Guide | Description |
|-------|-------------|
| [Getting Started](guides/getting-started.md) | Prerequisites, installation, first run, and step-by-step tutorial |
| [Configuration](guides/configuration.md) | Full config reference with examples for every hardware tier |
| [Model Selection](guides/model-selection.md) | Pick the right Ollama models for your hardware |
| [Architecture Deep Dive](guides/architecture.md) | Event sourcing, agent hierarchy, wave dispatch, monitoring |
| [Troubleshooting](guides/troubleshooting.md) | Common issues, diagnostics, and fixes |

## Reference

| Guide | Description |
|-------|-------------|
| [CLI Reference](reference/cli-reference.md) | Complete command, flag, and option reference |
| [Event Reference](reference/event-reference.md) | All 31 event types with payloads and state transitions |

## Demo

Generate an animated GIF of the full NXD workflow with [VHS](https://github.com/charmbracelet/vhs):

```bash
brew install vhs ffmpeg ttyd
vhs docs/demo.tape
```

This runs through `nxd init` -> `nxd req` -> `nxd status` -> `nxd agents` -> `nxd events` -> `nxd dashboard`.

## Recommended Reading Order

**New users:** Getting Started -> Configuration -> Model Selection

**Power users:** Architecture -> Configuration (tuning sections) -> Troubleshooting

**Offline setup:** Model Selection -> Getting Started -> Configuration -> Troubleshooting

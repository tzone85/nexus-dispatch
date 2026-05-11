# NXD Architecture Diagrams

Rendered SVGs for the docs. Source files are the `.d2` companions in this directory.

| Diagram | Purpose |
|---------|---------|
| `system-overview.svg` | High-level: user → CLI → orchestrator → agents → git |
| `pipeline-flow.svg` | Requirement → plan → wave dispatch → code → review → QA → merge |
| `two-model-split.svg` | qwen reviewer vs gemma4 coder, GPU swap trade-off |
| `event-sourcing.svg` | events.jsonl → projection → SQLite → readers |
| `native-runtime-loop.svg` | Gemma runtime tool-call loop with criteria gate |
| `agent-hierarchy.svg` | Agent roles + complexity routing + supervisor feedback |

## Regenerating

```bash
# requires d2 (https://d2lang.com)
brew install d2

d2 --theme=0 --pad=20 --layout=dagre docs/diagrams/system-overview.d2 docs/diagrams/system-overview.svg
# repeat for each .d2 file
```

Or one-shot:
```bash
for f in docs/diagrams/*.d2; do
  d2 --theme=0 --pad=20 --layout=dagre "$f" "${f%.d2}.svg"
done
```

The `--theme=0` flag picks the "Neutral default" theme — good contrast on both GitHub light and dark modes.

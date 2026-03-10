# Traffic Stats

This directory contains weekly snapshots of GitHub repository traffic data, auto-collected by the [traffic-stats workflow](../.github/workflows/traffic-stats.yml).

## How It Works

- Runs every **Monday at 06:00 UTC** (and can be triggered manually)
- Fetches the 14-day rolling window from GitHub's Traffic API (clones, views, referrers)
- Saves a JSON snapshot per week (e.g., `2026-03-17.json`)
- Regenerates `SUMMARY.md` with a cumulative table

## What's Tracked

| Metric | Description |
|--------|-------------|
| Clones (total) | Total `git clone` operations in the 14-day window |
| Clones (unique) | Unique IP addresses that cloned |
| Views (total) | Total page views on GitHub |
| Views (unique) | Unique visitors |
| Stars | Current star count |
| Forks | Current fork count |
| Referrers | Top traffic sources |

## Manual Trigger

You can trigger a snapshot anytime from the Actions tab or via CLI:

```bash
gh workflow run traffic-stats.yml
```

## Limitations

- GitHub only exposes a **14-day rolling window** — snapshots capture this window weekly to build history
- Clone/view counts are by **IP address**, not GitHub username — you can't see who specifically cloned
- Forks are public and visible at `https://github.com/tzone85/nexus-dispatch/network/members`

# NXD Demo — Full Pipeline Tutorial (Offline-First)
# Shows: doctor → req → resume → dashboard → results
#
# Prerequisites:
#   brew install vhs ffmpeg ttyd
#   go build -o ~/.local/bin/nxd ./cmd/nxd
#   ollama pull gemma4:e4b  (or your configured model)
#
# Usage:
#   cd ~/Sites/misc/nxd-smoke-test && vhs ~/Sites/misc/nexus-dispatch/docs/demo.tape
#
# This produces docs/demo.gif (~30s recording).
# Run from a test project repo (e.g., nxd-smoke-test) with nxd.yaml.
#
# Cleanup after recording:
#   nxd gc && rm -rf ~/.nxd/projects/nxd-smoke-test/

# --- Configuration ---
Output docs/demo.gif
Set FontSize 14
Set Width 1200
Set Height 700
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 35ms
Set Shell "zsh"

# --- Scene 1: Doctor — Check environment ---
Type "echo '── Step 1: Health Check ──'"
Enter
Sleep 500ms

Type "nxd doctor"
Enter
Sleep 4s

# --- Scene 2: Submit a requirement ---
Type "echo '── Step 2: Submit a Requirement ──'"
Enter
Sleep 500ms

Type@60ms 'nxd req "Create a simple HTTP server in main.go with a /health endpoint returning JSON status ok. Add a test using httptest."'
Enter
Sleep 25s

# --- Scene 3: Review the plan ---
Type "echo '── Step 3: Review the Plan ──'"
Enter
Sleep 500ms

Type "nxd status"
Enter
Sleep 3s

# --- Scene 4: Resume — dispatch agents ---
Type "echo '── Step 4: Dispatch Agents (Gemma 4 via Ollama) ──'"
Enter
Sleep 500ms

Type "# nxd resume <req-id>"
Enter
Type "echo 'NXD uses the native Gemma 4 runtime — no tmux needed.'"
Enter
Type "echo 'Agents run locally via Ollama, fully offline.'"
Enter
Sleep 3s

# --- Scene 5: Monitor via dashboard ---
Type "echo '── Step 5: Monitor via Dashboard ──'"
Enter
Sleep 500ms

Type "echo 'TUI:  nxd dashboard'"
Enter
Type "echo 'Web:  nxd dashboard --web'"
Enter
Sleep 2s

Type "nxd dashboard"
Enter
Sleep 8s

# Exit dashboard
Type "q"
Sleep 1s

# --- Scene 6: Check results ---
Type "echo '── Step 6: Results ──'"
Enter
Sleep 500ms

Type "nxd status"
Enter
Sleep 4s

Type "nxd events --limit 10"
Enter
Sleep 4s

# --- Scene 7: Cleanup ---
Type "echo '── Step 7: Cleanup ──'"
Enter
Sleep 500ms

Type "nxd gc --dry-run"
Enter
Sleep 3s

# --- Wrap up ---
Type "echo ''"
Enter
Type "echo 'NXD: Offline-first AI agent orchestration powered by Ollama.'"
Enter
Type "echo 'Same pipeline as VXD, no cloud APIs needed.'"
Enter
Type "echo ''"
Enter
Type "echo 'Full docs: https://github.com/tzone85/nexus-dispatch'"
Enter
Sleep 5s

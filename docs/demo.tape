# NXD Demo Tape
# Generates an animated GIF showing the NXD CLI in action.
#
# Prerequisites:
#   brew install vhs ffmpeg ttyd
#
# Usage (from repo root):
#   vhs docs/demo.tape
#
# This produces docs/demo.gif.
#
# Notes:
#   - The binary is built in a hidden preamble so the viewer only sees CLI usage.
#   - The demo runs in a temp directory so nxd init produces fresh output.
#   - If Ollama is running locally, init will show "Ollama detected".

# --- Configuration ---
Output docs/demo.gif
Set FontSize 14
Set Width 1200
Set Height 700
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 40ms
Set Shell "zsh"

# --- Hidden preamble: build binary, prepare clean workspace ---
Hide
Type "go build -o /tmp/nxd ./cmd/nxd/"
Enter
Sleep 8s
Type "rm -rf /tmp/nxd-demo && mkdir -p /tmp/nxd-demo"
Enter
Sleep 500ms
Type "cp /tmp/nxd /tmp/nxd-demo/ && cd /tmp/nxd-demo"
Enter
Sleep 500ms
Type "PROMPT='> '"
Enter
Sleep 500ms
Type "clear"
Enter
Sleep 500ms
Show

# --- Scene 1: Explore the CLI ---
Sleep 500ms
Type "./nxd --help"
Enter
Sleep 4s

# --- Scene 2: Initialize the workspace ---
Type "./nxd init"
Enter
Sleep 3s

# --- Scene 3: Validate configuration ---
Type "./nxd config validate"
Enter
Sleep 2s

# --- Scene 4: Check pipeline status ---
Type "./nxd status"
Enter
Sleep 3s

# --- Closing ---
Type "echo 'Done! NXD orchestrates AI agents offline -- no API keys needed.'"
Enter
Sleep 4s

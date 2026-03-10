# NXD Demo Tape
# Generates an animated GIF showing the full NXD workflow:
# nxd init -> nxd req -> nxd status -> nxd agents -> nxd events -> nxd dashboard
#
# Prerequisites:
#   brew install vhs ffmpeg ttyd
#   (or: go install github.com/charmbracelet/vhs@latest)
#
# Usage:
#   vhs docs/demo.tape
#
# This produces docs/demo.gif in the current directory.

# --- Configuration ---
Output docs/demo.gif
Set FontSize 14
Set Width 1200
Set Height 600
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 50ms
Set Shell "zsh"

# --- Scene 1: Initialize NXD ---
Type "# Step 1: Initialize NXD in your project (offline-first)"
Enter
Sleep 1s

Type "nxd init"
Enter
Sleep 3s

# --- Scene 2: Submit a requirement ---
Type "# Step 2: Submit a natural-language requirement"
Enter
Sleep 1s

Type@100ms 'nxd req "Add a health check endpoint at GET /healthz that returns 200 OK with service name, version, and uptime"'
Enter
Sleep 5s

# --- Scene 3: Check status ---
Type "# Step 3: Check the pipeline status"
Enter
Sleep 1s

Type "nxd status"
Enter
Sleep 3s

# --- Scene 4: View agents ---
Type "# Step 4: See which agents are assigned"
Enter
Sleep 1s

Type "nxd agents"
Enter
Sleep 3s

# --- Scene 5: View recent events ---
Type "# Step 5: Review the event trail"
Enter
Sleep 1s

Type "nxd events --limit 10"
Enter
Sleep 3s

# --- Scene 6: Launch the dashboard ---
Type "# Step 6: Launch the live TUI dashboard"
Enter
Sleep 1s

Type "nxd dashboard"
Enter
Sleep 5s

# Exit dashboard
Type "q"
Sleep 1s

Type "# Done! NXD orchestrates your AI agents entirely offline — no API keys needed."
Enter
Sleep 3s

# NXD Demo Tape
# Generates an animated GIF showing the NXD CLI in action.
#
# Prerequisites:
#   brew install vhs ffmpeg ttyd
#
# Usage:
#   vhs docs/demo.tape
#
# This produces docs/demo.gif.

# --- Configuration ---
Output docs/demo.gif
Set FontSize 14
Set Width 1200
Set Height 600
Set Theme "Dracula"
Set Padding 20
Set TypingSpeed 50ms
Set Shell "zsh"

# --- Scene 1: Show available commands ---
Type "echo '--- Step 1: Explore the NXD CLI ---'"
Enter
Sleep 1s

Type "nxd --help"
Enter
Sleep 3s

# --- Scene 2: Initialize NXD ---
Type "echo '--- Step 2: Initialize NXD in your project ---'"
Enter
Sleep 1s

Type "nxd init"
Enter
Sleep 3s

# --- Scene 3: Validate configuration ---
Type "echo '--- Step 3: Validate configuration ---'"
Enter
Sleep 1s

Type "nxd config validate"
Enter
Sleep 2s

Type "nxd config show"
Enter
Sleep 3s

# --- Scene 4: Check status ---
Type "echo '--- Step 4: Check the pipeline status ---'"
Enter
Sleep 1s

Type "nxd status"
Enter
Sleep 3s

# --- Scene 5: View events ---
Type "echo '--- Step 5: Review the event trail ---'"
Enter
Sleep 1s

Type "nxd events --limit 10"
Enter
Sleep 3s

# --- Scene 6: Launch the dashboard ---
Type "echo '--- Step 6: Launch the live TUI dashboard ---'"
Enter
Sleep 1s

Type "nxd dashboard"
Enter
Sleep 5s

# Exit dashboard
Type "q"
Sleep 1s

Type "echo 'Done! NXD orchestrates your AI agents entirely offline — no API keys needed.'"
Enter
Sleep 3s

# demo.tape — the README hero GIF, as code.
#
# Renders the two-beat story the README opens with: one-line install, then one
# prompt driving a full agentic loop. Regenerate any time the CLI output or
# story changes:
#
#   brew install vhs          # once; pulls ttyd + ffmpeg
#   vhs scripts/demo.tape     # writes landing/assets/demo.gif (~30s, <1MB)
#
# Recording notes (learned the hard way):
# - The install beat runs the real installer into a throwaway directory
#   (OCTO_INSTALL_DIR), so it never touches the octo binary on your PATH.
# - Re-running the tape can 403: install.sh asks api.github.com for the latest
#   version, and the unauthenticated rate limit is low. Pin OCTO_VERSION in the
#   hidden setup line below when iterating.
# - The agent beat runs against your real ~/.octo config. Check two things
#   before recording: the default provider/model is the one you want on camera
#   (the story says DeepSeek), and your personal rule layers (~/.octo/user.md,
#   octorules.md) don't force a reply language other than English — they apply
#   to this run too.

Output landing/assets/demo.gif

Require curl

Set Shell zsh
Set FontSize 18
Set Width 1200
Set Height 660
Set Padding 16
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms
Set WaitTimeout 240s

# --- hidden setup: sandboxed install dir + a real repo to explore ------------
# Add `export OCTO_VERSION=<latest>;` in front when re-running repeatedly.
# The workspace is a shallow clone of octo itself — beat 2 has the agent
# explore its own implementation.
Hide
Type `export OCTO_INSTALL_DIR="$HOME/.octo-demo-bin"; export PATH="$OCTO_INSTALL_DIR:$PATH"; rm -rf ~/octo-demo-ws; git clone --depth 1 -q https://github.com/open-octo/octo-agent ~/octo-demo-ws; cd ~/octo-demo-ws; clear`
Enter
Sleep 15s
Show

# --- beat 1: one-line install --------------------------------------------------
Type "curl -fsSL https://octo-agent.dev/install.sh | sh"
Sleep 500ms
Enter
Sleep 1s
# Skip the download wait; come back for the "installed octo x.y.z" tail.
Hide
Wait+Screen@120s /installed octo/
Show
Sleep 2.5s

# --- beat 2: one prompt, parallel sub-agent fan-out ----------------------------
# octo explores its own codebase with 3 concurrent sub-agents, then summarizes.
# NOTE: needs a release that includes open-octo/octo-agent#1737 (one-shot forces
# sub-agents synchronous) — on older releases the model may dispatch the
# children async and exit before the summary.
Type `octo "Use 3 sub-agents in parallel to explore octo's three core implementations — 1. the recycle bin, 2. workflows, 3. browser record & replay — then give me a summary."`
Sleep 500ms
Enter
# Show the fan-out starting, then skip the ~2-3 min wait; the sub_agent lines
# carry their real durations, so the cut is honest.
Sleep 12s
Hide
# The one-shot exits back to the shell (VHS prompt is ">") when the loop ends.
Wait@600s />$/
Show
Sleep 6s

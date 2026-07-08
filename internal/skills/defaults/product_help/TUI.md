# TUI (Terminal User Interface) Reference

octo's TUI is a bubbletea-based interactive interface launched by `octo` with no positional argument. It provides multi-turn conversation, live tool rendering, and rich keyboard shortcuts.

## Slash commands

Type `/` to open the completion menu (↑/↓ to navigate, Enter to run, Tab to fill in for arguments). Available commands:

| Command | Description |
|---------|-------------|
| `/help` | List commands and tools |
| `/model` | Switch to another model |
| `/thinking` | Set reasoning effort (off/low/medium/high/xhigh/max) |
| `/compact` | Summarize older history to free up context |
| `/transcript` | Re-print the last (or last N) tool call(s) with full, uncapped output |
| `/goal` | Bare `/goal` shows the current goal (if any); `/goal <objective>` sets one. Manage it with `/goal pause`, `/goal resume`, `/goal clear`, `/goal edit` (prefills the input to change the objective), or `/goal replace <objective>` (replace an unfinished goal). Once set, octo auto-continues turns on its own until the goal completes, is paused, or hits its token/turn budget |
| `/clear` | Wipe the conversation and start fresh |
| `/skills` | List discovered skills (trigger one directly with `/<name>`) |
| `/mcp` | Show connected MCP servers and their surfaces |
| `/workflows` | List available named workflows (run by the model via the `workflow` tool) |
| `/memory` | List what's remembered across sessions |
| `/init` | Analyze repo and generate/update `.octorules` |
| `/save` | Save the session now (also auto-saves after each turn) |
| `/sessions` | List recent sessions |
| `/exit` or `/quit` | Save and exit |

## Keyboard shortcuts

### Input editing

| Key | Action |
|-----|--------|
| **Enter** | Submit message / start turn |
| **Alt+Enter** / **Shift+Enter** / **Ctrl+J** | Insert newline in textarea |
| **↑** / **↓** | Browse input history (when cursor is on first/last line) or navigate lines |
| **Ctrl+V** | Paste image from clipboard as attachment |
| **Esc** | Clear input + discard attachments (idle); interrupt running turn; take back unsent message |

### Turn control

| Key | Action |
|-----|--------|
| **Ctrl+C** | Interrupt running turn; quit when idle |
| **Ctrl+D** | Quit (save and exit) |
| **Ctrl+Q** | Queue current input to run as a future turn |
| **Ctrl+X** | Cancel the most recently queued message (repeat to clear queue) |
| **Esc** (mid-turn, no output yet) | Take back — restore message to input box |
| **Esc** (mid-turn, with output) | Interrupt — stop the current turn |

### Permission mode

| Key | Action |
|-----|--------|
| **Shift+Tab** | Cycle permission mode: interactive → strict → auto → interactive |

### Completion menu

| Key | Action |
|-----|--------|
| **↓** / **↑** | Next / previous item |
| **Enter** | Run the selected command immediately |
| **Tab** | Fill the selected command into the input (to add arguments) |
| **Esc** | Dismiss menu |
| **Type `/`** | Open completion menu (lists all commands and skills) |

### Suggestion (ghost text)

| Key | Action |
|-----|--------|
| **Tab** / **→** | Accept the pending follow-up suggestion |
| **Type anything** | Dismiss suggestion |

### Permission prompt modal

| Key | Action |
|-----|--------|
| **y** | Allow this once |
| **a** | Allow for this session (remembered) |
| **n** / **Esc** | Deny |

### Question modal

| Key | Action |
|-----|--------|
| **↑** / **↓** or **j** / **k** | Move selection |
| **Space** | Toggle multi-select option |
| **Enter** | Confirm selection |
| **Esc** | Cancel |

## Drag and drop

Drag an image file into the terminal — octo detects the path and attaches it automatically (works on terminals that paste the file path as text).

## Status bar

The bottom status bar shows:
- **cwd** — current working directory (abbreviated with `~`)
- **ctx** — context window usage percentage
- **perm** — current permission mode (interactive / strict / auto)
- **elapsed** — turn duration (while running)
- **goal** — a segment appears here when a session goal is active (see `/goal` above), refreshing as it accounts tokens/turns

Contextual hints appear below the status bar depending on state (e.g. "Enter steer · Ctrl+Q queue · Esc interrupt").

After each turn finishes, an always-on footer line prints the elapsed time and tokens spent on that turn (suppressed in `--quiet` mode).

## Artifacts

When the model calls `show_artifact` to present a previewable file (HTML, Markdown, or an image), the TUI renders it as a click-to-open `file://` hyperlink instead of dumping raw content inline.

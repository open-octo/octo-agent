# TUI (Terminal User Interface) Reference

octo's TUI is a bubbletea-based interactive interface launched by `octo` with no positional argument. It provides multi-turn conversation, live tool rendering, and rich keyboard shortcuts.

## Slash commands

Type `/` to open the completion menu (↑/↓ to navigate, Enter to run, Tab to fill in for arguments). Commands: `/help`, `/model`, `/thinking`, `/compact`, `/transcript`, `/goal` (create/edit/pause/resume/clear/replace a standing session objective — see below), `/clear`, `/skills` (trigger one directly with `/<name>`), `/mcp`, `/workflows`, `/memory`, `/init`, `/save`, `/sessions`, `/exit`/`/quit`.

`/goal <objective>` sets a goal; once set, octo auto-continues turns on its own until it completes, is paused, or hits its token/turn budget. `/goal edit` here is **prefill-only** (fills the input box with the current objective to revise) — unlike the web UI/IM where `/goal edit <text>` edits inline in one step.

The Web UI and IM channels each recognize a **different** command set than the TUI (e.g. IM has `/bind`/`/unbind`/`/new` for re-binding a chat to a session; the TUI's `/skills`/`/mcp`/`/init`/etc. don't exist there). Full per-surface command tables and an availability matrix: **https://octo-agent.dev/docs/reference/slash-commands/** (`web_fetch`). The `/goal` feature in depth: **https://octo-agent.dev/docs/guides/goals/**.

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

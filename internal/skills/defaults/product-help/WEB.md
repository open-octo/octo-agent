# Web UI Reference

The Web UI is the browser dashboard served by `octo serve` (default `http://127.0.0.1:8088`) and reused
as-is by the desktop app. It talks to the same agent as the TUI over REST + WebSocket, so both surfaces
drive one session store — a conversation started in the terminal can be continued in the browser.

The composer's mid-turn behaviour mirrors the TUI's input modes; the keys differ because the browser is
not bound by terminal key codes.

## Composer keyboard shortcuts

| Key | Idle (no turn running) | Turn running |
|-----|------------------------|--------------|
| **Enter** | Send — starts a turn | **Steer** — injected into the running turn at its next iteration |
| **Cmd/Ctrl+Enter** | Send (same as Enter — no turn to wait for) | **Queue** — runs as its own turn after this one finishes |
| **Shift+Enter** | Insert newline | Insert newline |
| **↑** / **↓** | Browse input history (caret at start / end of the box) | Same |
| **/** (first character) | Open the command menu — ↑/↓ to navigate, Tab/Enter to pick, Esc to close | Same |

The **Stop** button next to Send interrupts the running turn (the TUI's Esc). Interrupting stops only that
turn — a queued message survives it and then **starts running immediately**, as the next turn. To cancel a
queued message rather than let it run, retract it first (see below). Same behaviour as the TUI.

Attachments: paste an image directly into the box, drag files onto it, or use the paperclip. Images ride
the message inline; other file types are uploaded and referenced by path for the agent to read.

## Mid-turn messages: steer vs. queue

A message sent while a turn is running is shown as a ghost line above the composer, labelled with which
of the two it is, until the server confirms it into the transcript:

- **steering** (Enter) — lands in the running turn's inbox and is picked up at the start of its next
  iteration, so it redirects work already in progress. Consecutive steers fold into one injection.
- **queued** (Cmd/Ctrl+Enter) — parked until the current turn ends completely, then run as a separate
  turn. Each queued message gets its own turn, in the order sent.

The pencil button on a ghost line retracts that message back into the composer for editing (the
counterpart of the TUI's ↑ recall). It fails once the turn has already consumed the message, which the UI
reports rather than silently dropping the text.

## Slash commands

The Web UI recognizes a different command set than the TUI — `/goal edit <text>` edits inline in one step
here, while the TUI's `/skills`, `/mcp`, `/init` are terminal-only. For the per-surface command tables and
availability matrix: **https://octo-agent.dev/docs/reference/slash-commands/** (`web_fetch`).

## Related docs

- Serving it, remote access, and auth: **https://octo-agent.dev/docs/guides/self-host/**
- HTTP + WebSocket API: **https://octo-agent.dev/docs/reference/http-api/**
- Terminal equivalents of everything above: `TUI.md` in this skill directory

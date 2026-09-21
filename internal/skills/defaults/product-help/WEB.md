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

## The artifacts panel

The right-hand panel alternates between **Artifacts** — every previewable file this session wrote
(HTML, Markdown, images), with a preview/code toggle — and **Diff**, the review of the working
tree.

An HTML artifact previews inside a sandboxed frame on its own origin, so nothing on the page can
reach the API or the conversation. What it *can* do is publish a snapshot of itself — a one-line
digest, optional structured extras, an optional screenshot — with `window.octo.pushState(...)`.
That snapshot is what the agent reads through its `artifact_state` and `view_artifact` tools, and
`insert_into_artifact` hands a file back the other way to a page that registered
`window.octo.onDelivery(...)`. An eye icon in the panel's title bar means the page open there is
publishing — that is what "can the agent see this?" looks like. Snapshots live in memory only,
scoped to the session that wrote the file, and disappear when the frame closes.

Not publishing is the default: a page that never calls `pushState` is invisible to the agent. The
`artifact-design` skill is what tells the agent to make the pages it writes publish.

## Slash commands

The Web UI recognizes a different command set than the TUI — `/goal edit <text>` edits inline in one step
here, while the TUI's `/skills`, `/mcp`, `/init` are terminal-only. For the per-surface command tables and
availability matrix: **https://octo-agent.dev/docs/reference/slash-commands/** (`web_fetch`).

## Settings → Data → Profiles

Lists the user-data profiles on this machine (`~/.octo`, `~/.octo-<name>`) with path, size, a **current**
tag on the one this backend runs under, and a **running** tag (with pid) on any whose backend is up.
Create a new profile by name; delete one by typing its name into the confirmation — deletion is
permanent and skips the recycle bin. The default profile, the current one, and a running one cannot
be deleted from here (stop that backend first with `octo serve --profile <name> stop`). The panel does
not switch profiles: that restarts the backend, so it lives in the desktop tray menu or a new
`octo serve --profile <name>` launch. CLI twin: `octo profiles` (`CLI.md`); guide:
**https://octo-agent.dev/docs/guides/profiles/**.

## The start screen

The new-session page (four starter cards, optional hero, Light App shortcut chips) is customizable
via `~/.octo/landing/config.json` — `cards` replaces the built-in four wholesale (omit `cards` to
keep them), `hero` takes an image file beside the config or one embedded Light App, `apps` lists
Light App slugs. The built-in cards' text lives in the web UI's i18n, not in any user-readable file;
the guide lists it. Full schema and the built-in four:
**https://octo-agent.dev/docs/guides/start-screen/** (`web_fetch`).

## Related docs

- Serving it, remote access, and auth: **https://octo-agent.dev/docs/guides/self-host/**
- HTTP + WebSocket API: **https://octo-agent.dev/docs/reference/http-api/**
- Terminal equivalents of everything above: `TUI.md` in this skill directory

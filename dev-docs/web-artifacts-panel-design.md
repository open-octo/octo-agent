# Web Artifacts Panel

A collapsible right sidebar in the web session view that collects previewable files the agent produces during a session — HTML pages, Markdown documents, and images — and renders them in place. The flagship case is a skill like `web-artifacts-builder` emitting a self-contained `bundle.html`: the user sees it rendered next to the conversation instead of hunting for a path in tool output.

## Goals

- Surface every previewable file the agent writes in a session, live and after reload.
- Render HTML interactively without giving agent-generated code access to the app origin.
- Zero changes to tools, skills, or the model-facing surface — existing skills trigger the panel as-is.

## Non-goals

- Not a file browser: only files this session's agent wrote appear, nothing else on disk.
- No artifact persistence layer of its own — the session transcript is the source of truth.
- Code files (`.go`, `.ts`, …) don't enter the panel; diffs and editors are a different feature.
- No editing inside the preview.

## Artifact model

An artifact is identified by its absolute path. It qualifies by extension:

| Kind | Extensions | Preview |
|---|---|---|
| html | `.html`, `.htm` | sandboxed iframe |
| markdown | `.md`, `.markdown` | `marked` render (same pipeline as chat messages) |
| image | `.png`, `.jpg`, `.jpeg`, `.gif`, `.svg`, `.webp` | `<img>` via blob URL |

A write to an already-listed path updates that entry (and refreshes the preview if it is open) rather than appending a duplicate. The list orders by last-write time, newest first. Artifacts are per-session; switching sessions swaps the list.

## Detection: ride the ui_payload stream

Both transport paths already deliver everything the panel needs; detection is frontend-only.

- **Live**: the WS `tool_result` event carries `ui_payload` (`ws_handlers.go`, `wsEventToolResult.UIPayload` in `ws_types.go`). `write_file` emits `{type: "write", path, size_bytes}` (`internal/tools/write_file.go`); `edit_file` emits `{type: "edit", path, occurrences, diff}` (`internal/tools/edit_file.go`).
- **History**: `GET /api/sessions/:id/messages` reconstructs the same `tool_result` events from persisted `ContentBlock.UI` (`handlers.go`), so the panel rebuilds identically on page reload and session switch.

A single frontend hook inspects each `ui_payload` with `type` of `write`, `edit`, or `artifact`, matches the path extension against the table above, and upserts the artifact entry. No backend state.

**Script-produced files** (built rather than written through the file tools — e.g. web-artifacts-builder's Parcel-bundled `bundle.html`) don't pass through `write_file`, so the `show_artifact` tool (`internal/tools/artifact.go`) covers them: the model calls it with the file's absolute path, it validates existence and previewability, and emits the `{type: "artifact", path, size_bytes}` payload the same hook ingests. The previewable-extension table is owned by the tools package (`tools.ArtifactContentType`) so the tool's validation and the endpoint's gate cannot drift apart.

## Content endpoint

The payload carries only the path; the panel needs bytes.

```
GET /api/sessions/{id}/artifacts?path=<absolute path>
```

**Authorization beyond the access key: the path must be one this session's agent wrote or explicitly presented.** The handler loads the session transcript and scans `tool_use` blocks (persisted with their `Input` maps, `internal/agent/content.go`) for `write_file`/`edit_file`/`show_artifact` calls; the requested path must equal one of their `path` inputs after `filepath.Clean`. Anything else is 404. This derives the whitelist from the transcript on each request — no extra state, survives server restarts, and caps disclosure at "files the user already watched the agent write in this very session".

Response headers:

- `Content-Type` from the extension (`text/html`, `text/markdown`, `image/*`)
- `X-Content-Type-Options: nosniff`
- `Content-Security-Policy: sandbox` — defense in depth should anyone open the URL directly in a tab; the primary isolation is the iframe sandbox below
- Size cap 10 MB (artifact HTML bundles run 200 KB–2 MB); larger files return 413 and the panel shows a download-only entry

## Rendering security

Agent-generated HTML is untrusted by definition (prompt injection can author it). It must never execute in the app origin, where it could read the access key and drive the session API.

- **HTML**: fetched as text, injected into `<iframe sandbox="allow-scripts" srcdoc=…>`. No `allow-same-origin` — scripts run, but in an opaque origin with no cookies, no `localStorage`, no reach back into the app. This is the same model claude.ai artifacts use.
- **Markdown**: rendered with the existing `marked` pipeline used for assistant chat messages — the same trust class (model-authored content) with the same posture.
- **Images**: fetched as a blob, shown via object URL; never interpreted as HTML (nosniff + explicit Content-Type).

## UI

```
┌──────┬──────────────────────────┬───────────────┐
│ left │   conversation           │ Artifacts  ⟨⟩ │
│ side │                          │ ┌───────────┐ │
│ bar  │  [🛠 write bundle.html]  │ │ bundle.html│ │
│      │                          │ │ report.md  │ │
│      │                          │ ├───────────┤ │
│      │                          │ │  preview   │ │
│      │                          │ │  (iframe)  │ │
└──────┴──────────────────────────┴───────────────┘
```

- Mirrors the existing left `<aside id="sidebar">` pattern on the right of `<main id="main">`: a `<aside id="artifacts-panel">` that is hidden until the session has at least one artifact, then shows a header toggle with a count badge.
- Collapsed by default; auto-opens the first time an artifact appears in a **live** turn (not on history replay — reloading an old session shouldn't pop the panel).
- List rows: kind icon, basename, relative time. Row actions: preview (default), open raw in new tab, download.
- Preview fills the lower pane; HTML previews get a refresh button (re-fetch + re-render).
- Narrow viewports (≤768px, the existing mobile breakpoint): the panel becomes an overlay like the left sidebar's mobile mode.
- New i18n keys under `artifacts.*` (en + zh).

## Files touched

| File | Change |
|---|---|
| `internal/server/artifact_handler.go` (new) | content endpoint + transcript-derived path whitelist |
| `internal/server/server.go` | route registration |
| `internal/server/static/artifacts.js` (new) | panel module: collection, list, preview |
| `internal/server/static/sessions.js` | one hook in the live `tool_result` path and one in `_renderHistoryEvent` calling `Artifacts.observe(ui_payload)`; session-switch reset |
| `internal/server/static/index.html` | `<aside id="artifacts-panel">` skeleton |
| `internal/server/static/app.css` | panel layout, breakpoint behavior |
| `internal/server/static/i18n.js` | `artifacts.*` keys |

## Test plan

- **Handler**: whitelist accepts a path written by `write_file` in the transcript and one edited by `edit_file`; rejects unwritten paths (404), other sessions' paths (404), traversal attempts, and oversize files (413). Content-Type per extension. Same `t.Setenv(HOME)` + `mustServer` pattern as `skill_import_handler_test.go`.
- **Manual e2e**: run a session that writes `bundle.html` + `report.md` + a PNG; verify live appearance, preview rendering, same-path rewrite refresh, reload reconstruction, and that an `alert(document.cookie)` inside artifact HTML cannot reach the app origin.

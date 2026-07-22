# Mobile UI: an independent view tree over the shared data layer

The mobile client currently ships the desktop web UI verbatim — `bundle-web.mjs`
copies the built `internal/server/webdist` into the Capacitor `www/` and injects
the `boot.ts` shim. That gets a working app onto a phone, but the desktop
two-pane layout (a fixed sidebar plus ten views) is the wrong shape for a small
touch screen.

This document specifies the replacement: a **separate Svelte view tree for
mobile**, mounted on the **same store / WebSocket / API layer** the desktop UI
already uses. The two are different *views*, not different apps. The remote is
still plain `octo serve` — nothing here touches `internal/server`, and it builds
on the transport in
[`mobile-managed-tunnel-design.md`](mobile-managed-tunnel-design.md).

## Positioning

The phone is a **remote control and an inbox**, not a second agent. You reach
for it to handle the things that need you — a reply, an approval, a finished
result — on a machine that is running `octo serve` elsewhere. The information
architecture follows from that:

- A **bottom tab bar**: Sessions / Tasks / Config / Settings.
- The Sessions tab is a **status-card feed, not a message list** — cards are
  grouped by what they need from you (needs-reply, needs-approval, running,
  done), not listed chronologically.
- Approvals are **inline on the card** (approve / reject without opening a
  detail), and each activity opens a **typed detail view** (chat / approval /
  progress / result) rather than one uniform transcript.

A mobile prototype established this IA and interaction model; this document maps
it onto the codebase. `Browser` and `Channels`, two of the ten desktop views,
are intentionally dropped on mobile.

## Principle

**Independent UI shell, still Svelte, reusing the data layer — business logic is
never duplicated.** Only the view shell is mobile-specific; the WebSocket
protocol, session state, and REST calls are the same ones the desktop uses.
Duplicating the data layer is the main hazard of a "separate UI" and is
explicitly avoided.

## Mounting and build

- **View split.** The entry point checks `mobileShell` (already in
  `stores.ts` — it detects the Capacitor global). When true it mounts the mobile
  view tree; when false, the existing desktop `App.svelte`. This is a top-level
  `{#if}` split inside one Vite build: `web/src/lib` imports resolve normally
  and the shim/boot path is unchanged; the mobile shell is simply a component
  tree that never coexists with the desktop one.
- **No `stores.ts` split.** `stores.ts` mixes *data* stores (`sessions`,
  `chatMessages`, `chatStreaming`, `questionModals`, …, reused on mobile) with
  *desktop-UI* stores (`sidebar`, `cmdkOpen`, `artifactsOpen`, …, unused on
  mobile). An earlier draft split the desktop-only ones into a `stores.ui.ts`;
  that was dropped. Store definitions are side-effect-free, so the mobile tree
  simply imports the data-store subset it needs and never references the
  desktop-UI stores — splitting the file would rewrite every desktop
  `from './stores'` import for no real benefit. No change to an existing web
  file is required here.

## Component map

### Reused as-is — data / protocol layer
| Asset | Role on mobile |
|---|---|
| `ws.ts`: `ws` (WsManager), `wsState`, `wsReconnect` | Live stream + the connection-state model (three states plus reconnect info) |
| `api.ts`: `listSessions` / `createSession` / `updateSession` / `deleteSession` / `branchSession` / `editMessage` + group/pin | Feed data, new session, session actions |
| `stores.ts` data stores: `sessions`, `activeSessionId`, `chatMessages`, `chatStreaming`, `chatProgress`, `chatBgTasks`, `chatTodos`, `questionModals`, `confirmModal`, `globalPermissionMode`, `pendingPrompt`, `running`, `wsDown` | Feed grouping, detail rendering, approvals |
| `types.ts` (`Session` incl. `pending_question`, `QuestionModalEntry`), `confirm.ts`, `markdown.ts` | Types, confirm flow, message rendering |

### Reused business components (may need light touch/theming)
| Component | Placement |
|---|---|
| `chat/Composer.svelte` | ChatDetail input (attachments/send already present) |
| `chat/ToolGroup`, `SubAgentsCard`, `WorkflowsCard`, `BackgroundProcesses` | ProgressDetail tool / sub-agent / progress rendering |
| `overlays/QuestionModal.svelte` | Approval / needs-reply detail state (consumes `questionModals`) |

### Reused UI atoms (after theming)
`ui/Segment` (task filter), `ui/StatCard` (task stats), `ui/StatusTag`
(needs-approval / running / done), `ui/Switch` (task toggle), `ui/QrCode`
(pairing).

### New — mobile shell and views
`MobileApp.svelte` (root; holds tab/view/activeId navigation), `TabBar`, `Fab`
(new-session, Sessions tab only), `DetailHeader` (back + title + status),
`DeviceBanner` (device + connection state), `Feed` (three sections, below),
`SessionCard` (typed reply/approval/running/done), `detail/ChatDetail`,
`detail/ApprovalDetail`, `detail/ProgressDetail`, `detail/ResultDetail`,
`TasksView` / `ConfigView` / `SettingsView`, `NewTask`.

## Feed state aggregation

The three-section feed needs a status per session. A new derived store
`feedGroups.ts` (over `sessions` + `questionModals` + `chatStreaming` +
`Session.pending_question`) classifies each session:

- **needs-approval** — a permission-type `questionModals[id]` / `confirmModal`
- **needs-reply** — a question-type `questionModals[id]`, or `pending_question`
- **running** — `chatStreaming[id] === true`
- **done** — otherwise, ordered by last activity

and emits three sections: **To-do** (needs-approval + needs-reply, manual pins
on top), **Active** (running), **Recent** (done — the latest 3–5, with the rest
behind a second-level history page). The history/search page reuses
`listSessions` plus the existing group/pin APIs.

## Connection state

`ws.ts` already exposes `wsState` and `wsReconnect`, so `DeviceBanner` binds
directly: `connected` → "Connected to <host> · N sessions"; `connecting` /
reconnecting → "Reconnecting…"; `disconnected` → "Offline · last synced N min
ago". While not `connected`, `SessionCard` and `ProgressDetail` render the last
known state with a "will update on reconnect" note instead of playing a live
pulse. The full push-woken reconnect loop depends on the push wakeups from the
managed-tunnel design; the state slots are in place now regardless.

## Theming

The mobile UI ships **its own neutral token set** (roughly twenty tokens:
backgrounds, text tiers, borders, tag/status colors), kept independent of the
web theme so the two view trees evolve separately. The accent stays `#1677FF`
for cross-surface consistency, and both light and dark are defined. Tokens live
in a mobile-local `theme.css`.

## Delivery batches

- **Batch 0 — groundwork.** Mobile `theme.css` (neutral tokens); `MobileApp`
  shell + a `mobileShell` branch in `App.svelte`. (Done.)
- **Batch 1 — session flow (usable first).** `TabBar` + `Fab` + `Feed`
  (three sections + `feedGroups.ts`) + `SessionCard` + `ChatDetail` (reusing
  Composer) + `DeviceBanner`. Result: view sessions, open a conversation, send
  and receive on a phone.
- **Batch 2 — typed details.** `ApprovalDetail` (inline approval card +
  `questionModals`/`confirmModal`) + `ProgressDetail` (timeline + reused tool
  components) + `ResultDetail` (artifacts).
- **Batch 3 — remaining tabs.** `TasksView` (scheduled-task store + cron API) +
  `ConfigView` (skills / MCP / workflows / memory / trash entries) +
  `SettingsView` + `NewTask`.
- **Batch 4 — polish.** Full offline handling (awaits push), multi-device
  labeling, light/dark polish, on-device walkthrough on a Mac.

## Open points (settled during implementation)

- **Build shape** — one Vite build with a top-level split (recommended) vs. a
  separate mobile entry.
- **Touch adaptation** — hit areas and long-press behavior on reused components
  (`Composer`, `ToolGroup`), walked through per component.
- **Dark tokens** — the prototype is light-only; dark values are filled in
  (octo supports light/dark across surfaces).

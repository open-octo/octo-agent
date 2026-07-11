# Wails Desktop Shell

A native desktop application for octo, built with [Wails v3](https://v3.wails.io/), that wraps the existing web UI in an OS window and adds the native integrations a browser can't give: an OS folder dialog, a system tray, launch-at-login, and native notifications. It is octo's sixth interface — alongside CLI, TUI, web, and the IM bridges — not a replacement for any of them.

The desktop shell reuses the web stack wholesale: the same Svelte frontend (`internal/server/webdist`) and the same Go HTTP handlers (`internal/server`). It adds a thin native layer, no second UI codebase.

## Goals

- Give local users a double-clickable app: no `octo serve`, no URL or port to remember, an icon in the dock/taskbar and tray.
- Make "point the agent at a folder" feel native — an OS folder dialog returning a real path — building directly on the [web folder picker](web-folder-picker-design.md).
- Reuse the existing web frontend and server handlers unchanged; the desktop build is a new entry point, not a fork of the UI.
- Keep the CLI and `octo serve` exactly as they are: still a single static Go binary, still self-hostable and reachable from a browser or phone.

## Non-goals

- Not a replacement for the web interface. `octo serve` stays the answer for self-hosting and remote/mobile access; the desktop app is the answer for a local workstation. The two share all their code.
- Not a new UI. Zero Svelte views are rewritten for the desktop; the window renders the same app.
- Linux ships as a build-verified AppImage, but is a step behind macOS/Windows in polish: it relies on the host's GTK4/WebKitGTK 6.0 (the AppRun launcher preflights and guides the user if absent) and hasn't been run-tested on a real desktop.
- No offline/embedded model runtime, no bundled provider — the app talks to the same configured providers the CLI does.

## Why a shell, not a rewrite

octo's positioning rests on being open, self-hostable, and a single zero-runtime binary. Replacing the web UI with a desktop-only app would forfeit the self-host/remote reach that is a core wedge. So the desktop app is *additive*:

- **The CLI stays pure Go.** Wails needs CGO and a platform webview; that cost is confined to the desktop build target and never touches `go build ./cmd/octo`. `make build` still produces the same static binary.
- **`octo serve` stays.** Remote and multi-user deployments are unaffected — they keep using the browser against a served instance.
- **Zero-runtime holds where it can.** macOS ships WKWebView and Windows ships the evergreen WebView2 runtime, so on those two the app carries no bundled runtime of its own. Linux needs GTK4 + WebKitGTK 6.0 + libsoup 3.0 on the host — a newer, less-ubiquitous stack — so the AppImage takes them from the host and its launcher guides the user to install them if missing.

## Architecture: in-process server + native bridge

The desktop app runs octo's existing `server.Server` in-process on a loopback port and points a Wails window at it. Native capabilities are exposed to the frontend through a small Go interface the Wails layer implements — not through Wails' JavaScript bindings, because the page is served by octo's own server, not Wails' asset server.

```
┌─ octo-desktop process ───────────────────────────────────────┐
│                                                               │
│  Wails v3 app (main)                                          │
│   ├─ window  ── loads ──▶  http://127.0.0.1:<port>            │
│   ├─ system tray, single-instance lock, autostart            │
│   └─ implements NativeBridge (dialogs, notifications)        │
│                    │ injected at construction                 │
│                    ▼                                          │
│  server.Server (internal/server)  ── listens 127.0.0.1:port  │
│   ├─ go:embed webdist  (same Svelte frontend)                │
│   ├─ /api/*, /ws        (same handlers, unchanged)           │
│   └─ /api/native/*      (new; delegate to NativeBridge)      │
└───────────────────────────────────────────────────────────────┘
```

Why load a loopback URL instead of Wails' embedded asset server:

- **WebSocket just works.** octo's live updates run over `/ws`. A real `http.Server` upgrades the connection normally; routing WS through Wails' asset-server pipeline is an unverified hijack risk we avoid entirely.
- **Maximal reuse, minimal Wails surface.** The frontend loads exactly as it does under `octo serve` — same origin shape (`http://127.0.0.1:<port>`), same auth path (the loopback exemption already permits it), same everything. Wails contributes only the window, the tray, and the native bridge.

### The shared hub: same backend as `octo serve`

While it runs, the desktop app **is** the one `octo serve` backend for the machine — not a private per-window server. It binds the fixed loopback port `127.0.0.1:8088` (the same default `octo serve` uses) and runs the server in-process, so the Web UI, VS Code, Obsidian, and the CLI all connect to this instance over the same HTTP/WebSocket API. See [desktop-hub-design.md](desktop-hub-design.md) for the full design and the decisions behind it; in brief:

- **One backend owns the port at a time.** The app joins the `~/.octo/serve.pid` protocol (`internal/serveproc`) that `octo serve -d` uses: on launch it takes over a running daemon (after asking) or binds the port itself, writing its own pid. A headless `octo serve -d` remains the option for GUI-less machines.
- **Channels are opt-in per machine.** The hub can run the IM bridges, but does not start them just because the GUI launched — a persisted "run channels on this machine" toggle (default off, `~/.octo/desktop.json`) gates `initChannels`, flipped at runtime through `PUT /api/native/channels` → the server's `SetChannelsEnabled`. Launching the GUI never silently starts a bridge.
- **Window lifecycle ≠ backend lifecycle.** Closing the window hides to the tray and keeps the hub serving other clients (default; a per-machine setting can make close quit instead). "Quit Octo" fully stops the backend and confirms first when channels are running.

LAN exposure stays a CLI concern (`octo serve -addr :8088`); the desktop app binds loopback only.

### The native bridge

Native calls do **not** rely on Wails' JS runtime being injected into the page (it isn't, since octo serves the page). Instead they travel the path the frontend already speaks — HTTP — and terminate in Go:

```go
// internal/server: injected at construction; nil in `octo serve`, non-nil in desktop.
type NativeBridge interface {
    PickFolder(ctx context.Context, startDir string) (path string, cancelled bool, err error)
    Notify(title, body string)
}
```

- The Wails `main` implements `NativeBridge` using Wails runtime dialogs and the notifications service, and passes it into `server.New` via config.
- New endpoints — `POST /api/native/pick-folder`, and internal use of `Notify` — are registered **only when a bridge is present**, so `octo serve` exposes no new surface.
- The frontend learns it's in desktop mode from a capability flag on an existing response (e.g. `native: true` on `/api/version`). When set, the folder picker's "Browse…" calls `/api/native/pick-folder` (OS dialog) instead of opening the in-app directory tree; when unset (plain `serve`), it keeps the in-app tree from phase 1. Both paths end in the same `PATCH /api/sessions/{id}/working_dir`.

This is the whole bridge: a two-method Go interface and one conditional route group. Tray, single-instance, and autostart live entirely in the Wails `main` and need no frontend or server change.

## Native capabilities (first release)

- **Folder dialog** — `runtime` open-directory dialog via `NativeBridge.PickFolder`, seeded at the session's current working dir. This is the native fulfilment of the phase-1 picker.
- **System tray** — Wails v3 system-tray menu (`v3/pkg/services`): show/hide window, quick "new session", quit. Window can attach to the tray icon.
- **Native notifications** — the server already decides when to notify (a session needs an answer / finished replying while the user is away; see the web notification path). In desktop mode that trigger calls `NativeBridge.Notify` → Wails `v3/pkg/services/notifications` (cross-platform, title/body, actions) instead of the browser Notification API. ⚠️ Wails has an open notifications bug on Windows/Linux ([#4449](https://github.com/wailsapp/wails/issues/4449)) — validate on Windows during implementation; fall back to a tray balloon if needed.
- **Single-instance lock** — Wails v3 single-instance manager: a second launch focuses the running window instead of starting a second server on another port.
- **Launch-at-login (autostart)** — Wails v3's built-in `app.Autostart` (`Enable`/`Disable`/`Status`), exposed through `NativeBridge` + `GET`/`PUT /api/native/autostart` and a Settings toggle shown only in the desktop shell.

## Frontend changes

Deliberately tiny:

- A `native` capability flag read once at startup into a store.
- The folder picker's "Browse…" branches on it: native dialog vs the phase-1 in-app tree.
- The notification path branches server-side, so the frontend's existing notification code is untouched in desktop mode (or short-circuited by the capability flag).

No new views, no restyle. The desktop window is the web app.

## Build & distribution

- **Separate nested module.** The desktop main lives in `cmd/octo-desktop/` as its own Go module (`cmd/octo-desktop/go.mod`, `replace` back to the parent). The parent's `go build ./...` / `go vet ./...` / `go test ./...` skip a nested module entirely, so the Wails v3 dependency (`github.com/wailsapp/wails/v3`) and its CGO/webview transitive deps never enter the CLI's `go.mod` — the CLI stays a pure static binary and the existing Go 1.25 × {Linux, macOS, Windows} matrix is unaffected. (Cleaner than a `//go:build desktop` tag, which would keep the deps in the main module and be fragile under `go mod tidy`.)
- **Built on native per-OS runners** (Wails' guidance; matches octo's CI). A dedicated `Desktop` workflow builds all three: macOS (CGO), Windows (`-H windowsgui`, pure-Go), and Linux (CGO + GTK4/WebKitGTK 6.0). Packaging is our own scripts — `scripts/package-desktop-macos.sh` (`.app`), `scripts/package-desktop-linux.sh` (AppImage) — plus Inno Setup for the Windows installer; `wails3 build` is available but not required by this reuse-the-web-frontend design. Windows also cross-compiles from any host (`GOOS=windows`, no CGO).
- **Windows** relies on the evergreen WebView2 runtime (present on current Windows); the Inno Setup installer ships the app + CLI + uv.
- **Linux** ships as an AppImage — the install-free Linux equivalent of the mac/Windows installers (download, `chmod +x`, run). It carries no separate installer: the `octo` CLI comes from the `curl | sh` script instead, and the AppRun launcher offers **once** to register a menu entry (writes `~/.local/share/applications/octo-desktop.desktop` pointing at `$APPIMAGE`) so Octo appears in the app grid like a native install.
- **macOS** bundle is ad-hoc signed for local use; Developer ID notarization is a later release step (tracked with the `.pkg` installer).
- The desktop artifact is **separate** from the CLI binary — shipping it does not change `octo`'s single-binary story or its release assets.

## Security

- The in-process server binds loopback only; the window is the sole client. The existing hardened loopback auth exemption (DNS-rebinding + Origin gates) already covers this origin.
- `/api/fs/list` (phase 1) and `/api/native/*` are localhost-only by construction — everything is same-machine here.
- The `NativeBridge` and its routes exist only in the desktop build; `octo serve` is byte-for-byte unchanged in what it exposes.

## Risks & open questions

- **Wails v3 is alpha.** API is stable and used in production, but pre-beta churn is possible; pin a known-good alpha and bump deliberately.
- **Windows notifications** — the open bug above; needs on-device validation, tray-balloon fallback ready.
- **Windows install flow + `install.ps1`** aren't run-verified from a macOS dev machine; validated on a real Windows box before release.
- **macOS notarization** gates a friction-free install; tracked with the existing installer effort.
- **Linux** AppImage isn't run-tested on a real desktop yet; the host GTK4/WebKitGTK 6.0 dependency is the main friction.

## References

- Wails v3 status (alpha, API-stable): https://v3.wails.io/status/
- Custom `http.Handler` in the asset server (the mechanism we deliberately *don't* need, having chosen the loopback approach): https://wails.io/docs/guides/dynamic-assets/
- Cross-platform build guidance (native runners recommended): https://v3.wails.io/guides/build/cross-platform/
- Notifications service: https://pkg.go.dev/github.com/wailsapp/wails/v3/pkg/services/notifications
- Single-instance guide: https://v3alpha.wails.io/guides/single-instance/

# Desktop App as the Shared Serve Hub

**Status: Design agreed, not yet implemented.** This document fixes the design for making the desktop app the single backend that every other octo interface shares. All decisions below are settled; what remains is implementation. It builds on [wails-desktop-design.md](wails-desktop-design.md).

## Problem

octo has five client interfaces, and four of them already share one backend: the Web UI, the IM bridges (which run *inside* `octo serve`), the VS Code extension, and the Obsidian plugin all talk to a single `octo serve` daemon over the same HTTP/WebSocket API at `127.0.0.1:8088`.

The desktop app is the one exception. It embeds its *own* `server.Server` on an ephemeral loopback port and sets `NoChannel: true`. Two consequences:

- **Configured-but-inert channels.** The desktop window renders the shared frontend, including the Channels view, so a user can configure an IM channel there — but the desktop's private server never starts channels, so nothing happens. This is confusing: it is the only feature that is fully wired in the UI yet dead in the backend.
- **A split backend.** Sessions, live config, and running channels in the desktop app are a separate world from a `octo serve` the same user might run. Starting a session in VS Code and expecting to see it in the desktop window doesn't work, because they aren't the same server.

## The shift

Make the desktop app **the hub**: when it runs, it owns the shared server on the fixed port (`127.0.0.1:8088`), runs channels, and keeps its native bridge. The Web UI, VS Code, Obsidian, and the CLI messenger all connect to *it*. One backend, one set of sessions, one live config, channels actually running.

This is the smallest change that unifies all five interfaces, because it keeps the desktop's native bridge exactly as it is (see [Why B over a headless-daemon hub](#why-b-over-a-headless-daemon-hub)).

## Non-goals

- Not removing the headless daemon. `octo serve -d` stays for GUI-less machines (servers, CI, SSH boxes). The desktop-as-hub and the headless daemon are two front-doors to the *same* `server.Server`, mutually exclusive per machine (one owns the port).
- Not changing the frontend or the HTTP/WS API. The plugins keep their existing client protocol too — but they must meet the coordination contract in [Multiple backend-starters](#multiple-backend-starters) (probe `8088` + reconnect on takeover), which they are expected to already satisfy.
- Not a new remote/multi-user story. The hub is still loopback-by-default with the same auth model; exposing it on a LAN is the same opt-in (`-addr :8088` + access key) as `octo serve` today.

## Current state (recap)

- `octo serve` defaults to `127.0.0.1:8088`; `-d` writes `~/.octo/serve.pid` + `~/.octo/serve.log` and refuses to start a second daemon if one is alive.
- The desktop app binds `127.0.0.1:0` (ephemeral) and constructs `server.New(Config{NoChannel: true, Native: bridge, …})`, serving only its own window.
- Native capabilities (folder/file dialog, notifications, autostart) are `/api/native/*` routes registered only when a `NativeBridge` is present, loopback-gated.

## Target architecture

```
                     desktop app process  (owns 127.0.0.1:8088)
                     ┌───────────────────────────────────────────┐
                     │  Wails main                                │
   native window ───▶│   ├─ window → http://127.0.0.1:8088        │
                     │   ├─ tray (shows "backend running" state)  │
                     │   └─ implements NativeBridge               │
                     │  server.Server                             │
                     │   ├─ /api/*, /ws     (shared handlers)     │
                     │   ├─ IM channels     (running — NoChannel  │
                     │   │                    dropped)            │
                     │   └─ /api/native/*   (loopback-gated)      │
                     └───────────────────────────────────────────┘
                          ▲          ▲          ▲          ▲
                          │          │          │          │
                    Web browser  VS Code    Obsidian   CLI messenger
                    (localhost)  extension   plugin    (octo -p …)
```

On a headless machine the same `server.Server` is fronted by `octo serve -d` instead, with no window, no native bridge, and channels running the same way.

## Locked decisions

### 1. One hub at a time, coordinated through the pid protocol

The desktop app joins the existing `~/.octo/serve.pid` protocol so a headless daemon and the desktop app can never both own `127.0.0.1:8088`.

On launch the desktop app:

1. Reads `~/.octo/serve.pid`. If a **live** daemon is there (stale pids, from a crashed hub, are ignored via `isProcessAlive` and cleared), it does **not** silently bind — it **offers to take over**: a prompt "A background octo backend is already running. Stop it and run Octo as the hub?" On yes, it runs the equivalent of `octo serve --stop` and then binds; on no, the app exits (it will not run as a windowed client against a server it can't attach its native bridge to). One clear prompt, no silent port fight.
2. Otherwise binds `127.0.0.1:8088`, writes its own pid to `serve.pid`, and becomes the hub.
3. On full quit, removes the pid file (same as `octo serve --stop`).

`octo serve` / `octo serve -d` started while the desktop app is the hub hits the same "already running" guard that a second daemon hits today — no new failure mode, just the desktop participating in the existing one.

### 2. Native bridge is unchanged — and now serves all local clients

Because the desktop still owns its server, the `/api/native/*` bridge stays byte-for-byte as designed: HTTP routes terminating in the Wails-implemented `NativeBridge`. No dependency on injecting Wails' JS runtime into the page.

The one new fact: those routes are now reachable by *any* local client of the hub (a browser tab, VS Code), not just the desktop's own window. They remain **loopback-gated**, so only same-machine clients can call them, but the semantics change from "the window's private bridge" to "the local machine's native bridge." That is acceptable and even useful (a browser tab on the same box can trigger a real folder dialog), but the gating must stay strict: the native endpoints are never exposed to a non-loopback peer even when the hub is bound to a LAN address.

### 3. Channels run in the hub — opt-in per machine

The hub *can* run channels (unlike today's hard `NoChannel: true`), but does not start them just because the GUI launched. A per-machine **"Run channels on this machine"** toggle (default **off**) gates `initChannels`:

- **Off (default):** the Channels view shows the configured channels plus a clear banner — "Channels are not running on this machine. Turn on 'Run channels here' to start them, or run `octo serve -d` on an always-on machine." No silent inertness: the UI states *why* nothing is bridging.
- **On:** `initChannels` runs exactly as it does under `octo serve`, reading the same `~/.octo` config; configured channels start. Turning it off stops them without quitting the app.

This resolves the original configured-but-inert confusion the honest way: not by hiding the UI (the config is real and shared with `octo serve`), and not by silently starting an IM bridge every time someone opens the window (which the [laptop caveat](#laptop-caveat) makes actively bad). Launching the GUI never starts a bridge on its own; the user opts in with one visible toggle.

### 4. Fixed port, shared auth

The hub binds the same `127.0.0.1:8088` default and inherits `server.Server`'s full auth model (loopback exemption, access key for wider binds, Origin/DNS-rebinding gates). Nothing about the security surface is new relative to `octo serve`; the desktop app simply *is* an `octo serve` with a window and a native bridge.

### 5. Window lifecycle is decoupled from backend lifecycle — explicitly

Closing the window **hides to the tray** and keeps the backend running, because other clients (VS Code, a phone on the LAN, channels) may be actively using the hub. A **"Keep Octo running in the background when the window is closed"** setting (default **on**) governs this; turning it off makes close-window quit the app outright for users who don't want a background hub. This power is dangerous if it is invisible, so it is made explicit:

- The tray icon **always reflects backend state**: e.g. "Octo — backend running · N channels · M clients." The user can see at a glance that closing the window left a server up.
- **Quit** (the destructive action that stops the server, kills channels, and disconnects every client) is distinct from **close window**, and prompts for confirmation when channels are running or external clients are connected: "Quitting stops the octo backend. VS Code / Obsidian / channels will disconnect."

The intent is that "launching the GUI turns my machine into a hub" is a *visible, reversible* state, not a silent side effect — the opposite of the removed serve-on-login surrogate.

### 6. LAN exposure stays a CLI concern

The desktop app binds loopback (`127.0.0.1:8088`) only; it exposes no "bind wider" option in its settings. Serving on a LAN address (`-addr :8088`) with an access key remains an `octo serve` / CLI action, keeping the desktop app's surface loopback-simple. A user who wants the hub reachable from a phone runs the headless daemon, or launches the desktop app and separately understands that LAN exposure is a CLI knob.

## Multiple backend-starters

The clients are not all passive. The VS Code extension and the Obsidian plugin, on startup, **probe for a running backend and start one themselves if none exists**. So there are several actors that can bring the hub into being — the desktop app, either plugin, and a manual `octo serve` — not one designated starter. The design does not try to make one of them "the" starter; it makes them all coordinate through a single contract:

> **The contract: `127.0.0.1:8088` + `~/.octo/serve.pid`.** Every backend-starter probes that address and respects that pid file. The first to find nothing acquires the pid and becomes the hub; everyone else attaches as a client. Whoever starts it, it is the same `server.Server` over the same `~/.octo` config and sessions, so which actor won the race doesn't change what a client sees.

Because a plugin-started backend is a *headless* daemon (no native bridge) while a desktop-started one is a full hub (with the bridge), the order of arrival matters:

| Order | What happens | Plugin change needed |
|---|---|---|
| Desktop app first, then a plugin | Plugin probes, finds the hub on 8088, connects | None — the common case |
| Plugin only (no desktop app) | Plugin self-starts a headless daemon and uses it | None — the correct no-GUI fallback |
| Plugin starts a daemon first, then the desktop app launches | Desktop app finds a live daemon and offers to take over (decision #1); on yes it stops that daemon and binds as the hub | **Plugin must reconnect** when its backend is replaced |
| Desktop app is the hub, plugin probes | Plugin finds it and connects; never self-starts | None |

This keeps the plugins' self-bootstrap — it is exactly right hub-client behavior and the only way the no-desktop, headless, or server-side cases work. It must **not** be removed.

Two requirements fall on the plugins for the coordination to hold. Both are expected to already be satisfied; they are called out here as the contract the plugin repos (`octo-vscode`, `octo-obsidian`) must meet, and are worth verifying there:

1. **Same discovery contract.** A plugin must probe `127.0.0.1:8088` and, when self-starting, launch a standard `octo serve` that writes `~/.octo/serve.pid` — not a private port and not a pid-less process. Anything else produces two coexisting backends instead of one shared hub.
2. **Reconnect on backend replacement.** When the desktop app takes over a plugin-started daemon, the plugin's WebSocket drops and must reconnect to the new hub. Ordinary drop-and-reconnect logic covers this; a plugin that treats its self-started daemon as permanent would lose its connection after a takeover.

## Why B over a headless-daemon hub

The alternative — keep the desktop app a thin client and make a headless `octo serve -d` the permanent hub — is architecturally cleaner for always-on channels but pays a real cost the desktop-as-hub avoids:

- **Native bridge.** A headless daemon has no `NativeBridge`, so the desktop's folder dialog / tray / notifications could no longer travel the `/api/native/*` HTTP path. They would have to move to Wails JS bindings injected into the webview — which the current design deliberately avoids and which is unverified for an externally-served URL. Desktop-as-hub keeps the shipped, working bridge untouched. That is the decisive reason.

The tradeoff we accept in return is [the laptop caveat](#laptop-caveat).

## Laptop caveat

IM channels are inherently "always-on service" things, and the desktop-as-hub ties them to a GUI app's lifecycle. On an always-on workstation (a desktop Mac, a Mac mini, a dev box) that is fine. On a **laptop**, closing the lid (sleep) or quitting the app stops the channels — a bridge that comes and goes with the laptop.

Guidance, not a code path: users who need channels reliably online should run `octo serve -d` on an always-on machine and treat the desktop app as a client of it. The docs should say this plainly. The desktop-as-hub is the right default for the common "one workstation, occasional channels" case, not for "24/7 IM gateway on a laptop."

## Impact / what changes

- `cmd/octo-desktop/main.go`: bind `127.0.0.1:8088` (not `:0`) via the pid protocol; add the take-over prompt when a live daemon holds the pid; gate `initChannels` on the "run channels here" toggle (default off) instead of the hard `NoChannel`; hide-to-tray on window close (setting-gated) with the tray reflecting backend state; quit confirmation when channels/clients are active.
- `internal/server`: no API changes; a small helper so the desktop and `octo serve` share the exact same pid-file acquire/release path; a way to start/stop channels at runtime (for the toggle) rather than only at construction.
- Frontend: the Channels view gains a "Run channels on this machine" toggle and an off-state banner; the "native" capability flag continues to gate native affordances; a desktop-only "keep running in background" setting.
- Docs: reconcile [wails-desktop-design.md](wails-desktop-design.md) (its "not a shared server / runs no channels" statements become false) and the README's interface descriptions.
- Cross-repo: verify `octo-vscode` and `octo-obsidian` meet the [Multiple backend-starters](#multiple-backend-starters) contract (probe `8088` + reconnect on takeover). No protocol change expected, but it must be confirmed, not assumed.

## Security

- The hub is the same `server.Server` with the same auth as `octo serve`; being launched from a GUI changes nothing about the wire surface.
- `/api/native/*` stays loopback-gated and must never be reachable from a non-loopback peer, even when the hub is bound to a LAN address — a remote client must not be able to pop a native dialog or read a local path on the host.
- The pid-file protocol must be robust to stale pids (a crashed hub) so the desktop app doesn't refuse to start against a dead daemon — reuse the existing `isProcessAlive` check.

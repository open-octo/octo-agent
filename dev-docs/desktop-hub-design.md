# Desktop App as the Shared Serve Hub

**Status: Draft / proposal.** Not implemented. This document fixes the design decisions for making the desktop app the single backend that every other octo interface shares, and flags the decisions still open. It builds on [wails-desktop-design.md](wails-desktop-design.md).

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
- Not changing the frontend, the HTTP/WS API, or the plugins. They already speak to `127.0.0.1:8088`; the hub just changes *which process* answers.
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

1. Reads `~/.octo/serve.pid`. If a live daemon is there, it does **not** silently bind — it surfaces the conflict (see [Open decisions #1](#open-decisions) for takeover vs. attach).
2. Otherwise binds `127.0.0.1:8088`, writes its own pid to `serve.pid`, and becomes the hub.
3. On full quit, removes the pid file (same as `octo serve --stop`).

`octo serve` / `octo serve -d` started while the desktop app is the hub hits the same "already running" guard that a second daemon hits today — no new failure mode, just the desktop participating in the existing one.

### 2. Native bridge is unchanged — and now serves all local clients

Because the desktop still owns its server, the `/api/native/*` bridge stays byte-for-byte as designed: HTTP routes terminating in the Wails-implemented `NativeBridge`. No dependency on injecting Wails' JS runtime into the page.

The one new fact: those routes are now reachable by *any* local client of the hub (a browser tab, VS Code), not just the desktop's own window. They remain **loopback-gated**, so only same-machine clients can call them, but the semantics change from "the window's private bridge" to "the local machine's native bridge." That is acceptable and even useful (a browser tab on the same box can trigger a real folder dialog), but the gating must stay strict: the native endpoints are never exposed to a non-loopback peer even when the hub is bound to a LAN address.

### 3. Channels run in the hub

The desktop `Config` drops `NoChannel`. `initChannels` runs exactly as it does under `octo serve`, reading the same `~/.octo` config. The Channels view in the desktop window is now truthful: what you configure there actually starts. This directly resolves the configured-but-inert problem — not by hiding the UI, but by making the backend honor it.

### 4. Fixed port, shared auth

The hub binds the same `127.0.0.1:8088` default and inherits `server.Server`'s full auth model (loopback exemption, access key for wider binds, Origin/DNS-rebinding gates). Nothing about the security surface is new relative to `octo serve`; the desktop app simply *is* an `octo serve` with a window and a native bridge.

### 5. Window lifecycle is decoupled from backend lifecycle — explicitly

Closing the window does **not** stop the backend; it hides to the tray, because other clients (VS Code, a phone on the LAN, channels) may be actively using the hub. But this power is dangerous if it is invisible, so:

- The tray icon **always reflects backend state**: e.g. "Octo — backend running · N channels · M clients." The user can see at a glance that closing the window left a server up.
- **Quit** (the destructive action that stops the server, kills channels, and disconnects every client) is distinct from **close window**, and prompts for confirmation when channels are enabled or external clients are connected: "Quitting stops the octo backend. VS Code / Obsidian / channels will disconnect."

The intent is that "launching the GUI turns my machine into a hub" is a *visible, reversible* state, not a silent side effect — the opposite of the removed serve-on-login surrogate.

## Why B over a headless-daemon hub

The alternative — keep the desktop app a thin client and make a headless `octo serve -d` the permanent hub — is architecturally cleaner for always-on channels but pays a real cost the desktop-as-hub avoids:

- **Native bridge.** A headless daemon has no `NativeBridge`, so the desktop's folder dialog / tray / notifications could no longer travel the `/api/native/*` HTTP path. They would have to move to Wails JS bindings injected into the webview — which the current design deliberately avoids and which is unverified for an externally-served URL. Desktop-as-hub keeps the shipped, working bridge untouched. That is the decisive reason.

The tradeoff we accept in return is [the laptop caveat](#laptop-caveat).

## Laptop caveat

IM channels are inherently "always-on service" things, and the desktop-as-hub ties them to a GUI app's lifecycle. On an always-on workstation (a desktop Mac, a Mac mini, a dev box) that is fine. On a **laptop**, closing the lid (sleep) or quitting the app stops the channels — a bridge that comes and goes with the laptop.

Guidance, not a code path: users who need channels reliably online should run `octo serve -d` on an always-on machine and treat the desktop app as a client of it. The docs should say this plainly. The desktop-as-hub is the right default for the common "one workstation, occasional channels" case, not for "24/7 IM gateway on a laptop."

## Open decisions

These are the choices this draft intentionally leaves for confirmation before implementation:

1. **When a headless daemon already owns 8088, what does the desktop app do?**
   - (a) *Attach as a client* — point the window at the existing `:8088`, but then the desktop has no native bridge on that server (folder dialog / notifications degrade). Simplest, but loses native features exactly when another backend is up.
   - (b) *Offer to take over* — prompt "A background octo backend is running. Stop it and run Octo as the hub?" → on yes, `octo serve --stop` then bind. Preserves native features; one clear prompt.
   - (c) *Refuse with guidance* — "Stop the running backend first (`octo serve --stop`), then reopen Octo." Most conservative, most friction.
   - *Leaning (b).*

2. **Default when the window is closed: hide-to-tray (keep serving) or quit (stop serving)?**
   - Hide-to-tray matches the hub intent but risks the "I closed it but it's still running" surprise; mitigated by decision #5's tray state + quit confirmation.
   - Quit-on-close is least surprising but defeats the point of a shared hub (VS Code loses its backend the moment you close the window).
   - *Leaning hide-to-tray, gated on the visibility guarantees in decision #5.* Possibly a setting: "Keep Octo running in the background when the window is closed" (default on).

3. **Should channels be on by default in the hub, or opt-in per machine?**
   - On by default = the Channels view is fully live, but any configured channel starts whenever the app launches (the laptop caveat bites harder).
   - Opt-in ("Run channels on this machine") = the app is a hub for the UI/sessions but only bridges IM when the user asks. Safer default, one more toggle.
   - *Leaning opt-in*, so launching the GUI never silently starts an IM bridge.

4. **LAN exposure from the desktop app.** Do we expose the `-addr :8088` (bind wider) option in desktop settings, or keep LAN binding strictly a CLI/`octo serve` concern? *Leaning CLI-only* to keep the desktop app's surface loopback-simple.

## Impact / what changes

- `cmd/octo-desktop/main.go`: bind `127.0.0.1:8088` (not `:0`) via the pid protocol; drop `NoChannel` (or gate it on decision #3); add takeover/attach handling; tray reflects backend state; quit confirmation.
- `internal/server`: no API changes; possibly a small helper so the desktop and `octo serve` share the exact same pid-file acquire/release path.
- Frontend: the Channels view needs no change once channels actually run; the "native" capability flag continues to gate native affordances. If channels are opt-in (decision #3), a single "run channels on this machine" toggle.
- Docs: reconcile [wails-desktop-design.md](wails-desktop-design.md) (it currently states the desktop app is *not* a shared server and runs no channels — that becomes false) and the README's interface descriptions.

## Security

- The hub is the same `server.Server` with the same auth as `octo serve`; being launched from a GUI changes nothing about the wire surface.
- `/api/native/*` stays loopback-gated and must never be reachable from a non-loopback peer, even when the hub is bound to a LAN address — a remote client must not be able to pop a native dialog or read a local path on the host.
- The pid-file protocol must be robust to stale pids (a crashed hub) so the desktop app doesn't refuse to start against a dead daemon — reuse the existing `isProcessAlive` check.

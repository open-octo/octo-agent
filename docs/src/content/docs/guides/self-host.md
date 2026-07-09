---
title: Self-host octo serve
description: Run the web dashboard and IM bridge as a long-lived service.
---

```bash
octo serve                      # binds 127.0.0.1:8088 by default
octo serve -d                   # run in the background
octo serve --stop               # stop a background instance
octo serve -addr :8088          # expose on the LAN
```

## Access control

`127.0.0.1` is loopback and trusted implicitly — no key needed, and that's the default bind. The
moment you bind wider (`-addr :8088` or any non-loopback address), every API and WebSocket request
from a non-loopback client must present an access key:

```bash
octo serve -addr :8088 --access-key <key>
```

Omit `--access-key` and octo reads `OCTO_ACCESS_KEY`, then `config.yml`, then auto-generates and
persists one — startup prints a ready-to-open URL with the key embedded
(`http://<host>:<port>/?access_key=...`).

See the full boundary — what's defended, what's explicitly out of scope — in the
[security model](/docs/reference/security/).

## Restarting

By default `octo serve` runs as a **supervisor + worker** pair: the supervisor spawns the actual
worker process, and if the worker exits with code `42` (the same "restart requested" contract from
the [CLI reference](/docs/reference/cli/)), the supervisor re-resolves the binary path — so a
replaced binary takes effect — and respawns it. Any other exit code is left alone.

A restart can be triggered by `POST /api/restart` (returns immediately, `202`), or by the model
calling the `restart_server` tool — which is pinned to the `ask` permission class explicitly, so it
can never end up on an allow-list by accident. Either path waits for in-flight turns to finish (or a
30-second timeout, whichever comes first) before the process actually exits; new turns started
during that drain window are refused with a message asking you to try again in a moment, on every
transport including IM.

`--no-supervisor` skips all of this and runs the worker directly, so your own init system owns
restarts instead of octo's:

## Running as a service

`octo serve` is a single long-running process; the usual approach is to hand it to your init system
rather than run it in a terminal:

```ini
# systemd (Linux) — ~/.config/systemd/user/octo.service
[Unit]
Description=octo serve

[Service]
ExecStart=/usr/local/bin/octo serve --no-supervisor
Restart=on-failure

[Install]
WantedBy=default.target
```

`--no-supervisor` lets your init system own restarts instead of octo's own self-restart supervisor
duplicating that job. On macOS, a `launchd` plist with the equivalent `ProgramArguments` and
`KeepAlive` works the same way — and is exactly what the `.pkg` installer registers automatically.

## Logs and diagnostics

Foreground (`octo serve`) writes straight to the terminal it was started in. Daemon mode (`-d`) has
no terminal to write to, so output — including IM channel connection errors, since the bridge runs
in the same process — goes to `~/.octo/serve.log` instead:

```bash
octo serve --status   # is the daemon running, and what's its pid
tail -f ~/.octo/serve.log
octo serve --stop
```

The daemon's pid is tracked in `~/.octo/serve.pid`; `--status`/`--stop` read it directly rather than
scanning the process table. A stale pid (pointing at a process that's already dead) is cleared
automatically on the next `--status` or start.

Next: put a reverse proxy in front for TLS/a real domain, then [bridge chat apps](/docs/guides/channels/)
to the same running instance.

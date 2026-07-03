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

Next: put a reverse proxy in front for TLS/a real domain, then [bridge chat apps](/docs/guides/channels/)
to the same running instance.

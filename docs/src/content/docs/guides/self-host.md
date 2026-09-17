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

## Profiles

The paths below are for the default profile, whose service data is stored in `~/.octo`. Launch `octo serve --profile work` to use the isolated `work` profile-data root `~/.octo-work`; machine-managed helper tooling stays shared in `~/.octo/bin`.

The default profile always binds `127.0.0.1:8088` — the number every client ships with. A named profile can't share it, so the first `octo serve --profile work` takes the lowest free port from 8089 up and records it in `~/.octo-work/serve.addr`. Every later start reuses that exact address, so a phone, an Obsidian plugin or a VS Code window only has to be told the port once:

```bash
octo serve --profile work -d
# octo serve daemon started (pid 41288), ready at http://127.0.0.1:8089

octo serve --profile work status
# octo serve daemon: running (pid 41288) at http://127.0.0.1:8089
```

If something else has taken the recorded port, `octo serve` stops and says so rather than moving to the next one — a backend that silently relocates is a backend none of your clients can find. Free the port, or move the profile on purpose with `--addr`, which re-records it:

```bash
octo serve --profile work --addr 127.0.0.1:9100
```

Profiles are covered in full in [Run more than one octo](/docs/guides/profiles/). In short: the desktop app opens one profile at a time — the data root is process-global, which is what lets every path in octo resolve it without being handed one. Pick the profile from the tray's **Profile** submenu, which lists the roots that exist; the app records the choice in `~/.octo/desktop-profile` and restarts into it. The submenu appears once there is more than one profile to choose between. If octo is mid-turn or waiting on an answer, it says so before restarting, since that work goes with the restart.

`octo-desktop --profile work` still works for a launch from a terminal, and deliberately does not change what double-clicking the icon opens.

## Environment variables

Configuring octo entirely through the environment — nothing in `config.yml` — takes **two**
variables, not one: `OCTO_PROVIDER` names the vendor and that vendor's key authenticates to it. A
key on its own says which vendors you *can* reach, never which one you meant, so octo does not
guess; without `OCTO_PROVIDER` it treats the install as unconfigured and asks for setup.

Some environment variables (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `OCTO_ACCESS_KEY`,
`OCTO_LOG_LEVEL`, search keys like `TAVILY_API_KEY`, …) control how octo behaves at runtime.
Normally your shell profile exports them — but **GUI-launched processes don't inherit those**:
the desktop app, a launchd agent, and a `.desktop` session all start with a minimal
environment that skips `~/.bashrc` / `~/.zprofile`.

Drop a `~/.octo/serve.env` file to cover every default-profile launch mode uniformly. A named profile uses the matching path below its data root — for example, `octo serve --profile work` loads `~/.octo-work/serve.env`:

```bash
cat > ~/.octo/serve.env << 'EOF'
TAVILY_API_KEY=tvly-xxxxx
OCTO_PROVIDER=anthropic
ANTHROPIC_API_KEY=sk-ant-xxxxx
OCTO_LOG_LEVEL=debug
EOF
chmod 600 ~/.octo/serve.env
```

octo loads it at startup (before any tool or channel reads the environment):

- **Simple `KEY=VALUE` lines**, one per line.
- `#` comments and blank lines are skipped.
- An optional `export ` prefix is tolerated (so you can copy-paste from a shell rc file).
- Keys are **whitespace-trimmed**; a value with `=` inside it is preserved (`KEY=val=ue` works).
- **Variables already set in the process environment are NOT overridden** — explicit
  `FOO=bar octo serve`, systemd `Environment=`, and launchd `SetEnvironmentVariable`
  all win over the file. This keeps the file as a safe fallback, not a surprise override.

The systemd/launchd packaging templates point the default-profile service at this file via
`EnvironmentFile=%h/.octo/serve.env` (`packaging/systemd/octo.service`). To run a named profile,
the unit must invoke `octo serve --profile work` and use the matching
`EnvironmentFile=%h/.octo-work/serve.env`; the existing unit does not select a profile by itself.
The desktop app and TUI resolve the selected profile's matching file too.

Proxy variables (`HTTPS_PROXY` and friends) go in this same file — see
[Choose a provider · Reaching endpoints through a proxy](/docs/getting-started/choose-a-provider/#reaching-endpoints-through-a-proxy).

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

The agent can't take the crude route instead: it runs inside the very server process, so shell
commands that would kill `octo serve` or its supervisor (`kill <pid>`, `pkill octo` — including
indirect forms that only become visible after shell expansion, like `kill $P`) are refused with a
message pointing the model at `restart_server`. This guards against the model reflexively killing
its own host and dropping your session; it is not a sandbox — for real confinement see
[Sandbox the agent](/docs/guides/sandbox-the-agent/).

> The `restart_server` tool relies on a supervisor respawn contract. The desktop build runs the
> server in-process with no supervisor, so the tool is omitted there — channel config is instead
> applied via hot-reload (`POST /api/channels/<platform>/reload`), and other changes take effect when
> you restart the app.

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
EnvironmentFile=%h/.octo/serve.env
ExecStart=/usr/local/bin/octo serve --no-supervisor
Restart=on-failure

[Install]
WantedBy=default.target
```

`--no-supervisor` lets your init system own restarts instead of octo's own self-restart supervisor
duplicating that job. The unit above is for the default profile; for `work`, set
`EnvironmentFile=%h/.octo-work/serve.env` and use `ExecStart=/usr/local/bin/octo serve --profile work --no-supervisor`.
Port selection happens in that same top-level process, so a named-profile unit binds the port
recorded in `~/.octo-work/serve.addr` with no extra flags — add `--addr` only to choose it yourself.
(`OCTO_SERVE_WORKER` is internal to octo's own supervisor; setting it in a unit skips port
resolution entirely and the profile would collide with the default on 8088.)
On macOS, a `launchd` plist with the equivalent `ProgramArguments` and `KeepAlive` works the same
way — and is exactly what the `.pkg` installer registers automatically.

## Logs and diagnostics

Foreground (`octo serve`) writes straight to the terminal it was started in. Daemon mode (`-d`) has
no terminal to write to, so output — including IM channel connection errors, since the bridge runs
in the same process — goes to the default-profile path `~/.octo/serve.log` instead; named profiles
use the matching path, such as `~/.octo-work/serve.log`:

```bash
octo serve --status   # is the daemon running, and what's its pid
tail -f ~/.octo/serve.log
# For work: octo serve --profile work --status; tail -f ~/.octo-work/serve.log
octo serve --stop
```

The daemon's pid is tracked at the default-profile path `~/.octo/serve.pid`; named profiles use the
matching path, such as `~/.octo-work/serve.pid`. `--status`/`--stop` read it directly rather than
scanning the process table. A stale pid (pointing at a process that's already dead) is cleared
automatically on the next `--status`, `--stop`, or start.

If the desktop app disappears instead of reporting an error, look in the default-profile path
`~/.octo/crash.log` (`%USERPROFILE%\.octo\crash.log` on Windows); named profiles use the matching
path below `~/.octo-NAME`. A GUI process has no terminal to print a crash to, so
the app points its stderr at that file at startup; each run appends a banner line with its version
and pid, followed by the crash trace if there was one. Attach it when reporting the crash — but read
it first: stderr is also where MCP servers and their child processes write their own diagnostics, so
the file can hold more than stack frames.

Running the desktop binary from a terminal skips the redirection, leaving crashes on the terminal
where a developer will see them.

Next: put a reverse proxy in front for TLS/a real domain, then [bridge chat apps](/docs/guides/channels/)
to the same running instance.

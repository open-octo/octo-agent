---
title: Run more than one octo
description: Profiles give octo a second set of identity, memory, sessions and credentials on the same machine.
---

Everything octo remembers about you lives under `~/.octo`: who it thinks you are, what it has
learned, every session, your keys, your skills. One directory, one octo.

A profile gives you another one. `--profile work` puts that whole set under `~/.octo-work`
instead, and the two never see each other.

```bash
octo --profile work
```

That is the entire feature. What makes it worth having is what ends up on each side of the line.

## What a profile separates

Each profile gets its own:

- **Identity and memory** — `soul.md`, `user.md`, `octorules.md`, `memories/`
- **Sessions** — `sessions/`, session groups, the trash, input history
- **Credentials and config** — `config.yml` (provider, model, endpoint, API key), `serve.env`
- **Capabilities** — `skills/`, `workflows/`, `agents/`, and the built-in sets materialized beside them
- **Connections** — `mcp.json` and its OAuth tokens, `channels.yml` and IM credentials, the tunnel identity
- **Governance** — `permissions.yml`, `audit.log`, hooks and their trust store
- **Runtime state** — the backend's pid, logs, uploads, scheduled tasks, Light Apps, browser recordings

Two things stay shared on purpose:

- **`~/.octo/bin`** — the helper binaries the installers stage there, like `uv`. It is machine-level
  tooling, not your data; a second profile shouldn't mean a second Python toolchain.
- **Project-local `.octo/`** — a repo's own `hooks.yml`, worktrees and Light Apps belong to the
  repo, and follow it whichever profile you open it from.

A brand-new profile is not empty, either. The skills, workflows and expert agents that ship with
the binary materialize into it on first run, exactly as they did for your first profile. What's
empty is the part that was yours: config, memory, sessions, anything you installed yourself.

## Naming

Letters, digits, `-` and `_`, starting with a letter or digit.

```bash
octo --profile team-1     # fine
octo --profile "my work"  # rejected
octo --profile _lead      # rejected — can't start with an underscore
```

Nothing validates that a profile *exists*, because creating one is just using it. Which also means
a typo silently opens a third, empty octo rather than failing, so it is worth checking what you
actually have:

```bash
ls -d ~/.octo*
```

## On the command line

The flag is global — it works in any position, on any subcommand, in either form:

```bash
octo --profile work                       # interactive
octo --profile=work "summarise this repo" # one-shot
octo config --profile work                # set that profile's provider and model
octo skills list --profile work
```

There is also `OCTO_PROFILE`, which is what makes an alias worth setting up:

```bash
alias octow='OCTO_PROFILE=work octo'
alias octop='OCTO_PROFILE=home octo'
```

One thing to know about the environment variable: octo passes it down to everything it spawns. A
command the agent runs with the `terminal` tool inherits it, including a nested `octo`. That is
usually what you want — a sub-agent stays in the same data root — but it does mean a script run
from inside a `work` session reads `work`'s config, not your default one.

The TUI shows no indication of which profile it is in. If you are unsure, `octo serve` prints it,
or just look at the directory listing above.

## Running a backend

`octo serve` is where profiles stop being a private matter, because two backends can't share a
port.

The default profile keeps `127.0.0.1:8088` — the number every client ships with. A named profile
takes the lowest free port from 8089 up the first time it starts, and then keeps it:

```bash
$ octo serve --profile work -d
octo serve daemon started (pid 96701), ready at http://127.0.0.1:8089

$ octo serve --profile home -d
octo serve daemon started (pid 96711), ready at http://127.0.0.1:8090
```

The choice is recorded in `serve.addr` under the profile's data root and reused verbatim on every
later start. That is the point: an address you typed into a phone, an Obsidian plugin or a VS Code
window is only worth anything if it survives a restart.

Because it is a commitment rather than a preference, a recorded port that turns out to be taken is
an error — octo will not quietly move to the next one and strand every client that knows the old
number:

```
$ octo serve --profile work
octo serve: profile "work" is pinned to 127.0.0.1:8089, but that address is in use (listen tcp 127.0.0.1:8089: bind: address already in use)
  pinned by: /Users/you/.octo-work/serve.addr
  if this profile's own backend is already up: octo serve --profile work status
  to move this profile somewhere else: octo serve --profile work --addr 127.0.0.1:<port>
```

Free the port, or move the profile deliberately. `--addr` always wins, and re-records:

```bash
octo serve --profile work --addr 127.0.0.1:9100
```

Daemon control is per profile, and `status` tells you where a profile is listening — for an
automatically chosen port, it is the only place to look:

```bash
octo serve --profile work status   # octo serve daemon: running (pid 96701) at http://127.0.0.1:8089
octo serve --profile work stop
```

Under a service manager, the unit has to name the profile itself — nothing infers it:

```ini
ExecStart=/usr/local/bin/octo serve --profile work --no-supervisor
EnvironmentFile=%h/.octo-work/serve.env
```

The Web UI shows the active profile as a small badge next to the version in the sidebar footer, and
`GET /api/version` carries a `profile` field for named profiles. Both are there for the same
reason: with several backends up, the tab in front of you gives no other clue which data it is
looking at.

## In the desktop app

Double-clicking an icon passes no arguments, so the desktop app can't be told which profile to open
the way the CLI can. It remembers instead.

Open the tray menu, pick from the **Profile** submenu, and the app records the choice and restarts
into it. The submenu lists the profiles that exist on disk, and only appears once there is more
than one — it is also the only place to see your profiles without a terminal.

One app, one profile. Switching restarts, and a restart loses whatever octo was in the middle of,
so it asks first — but only when there is something to lose. An idle backend switches without a
dialog. If octo is working on something, or waiting on an answer from you, it says which before
going ahead.

Launching from a terminal still works and still wins:

```bash
octo-desktop --profile work
```

That one is deliberately not remembered. A one-off launch shouldn't redefine what double-clicking
the icon opens.

## What you'd actually use it for

**Work and personal.** The clearest case, and the one the identity files are there for. Two
different `soul.md` files, two sets of memories that never contaminate each other. Under a coding
CLI this would be pointless; for something that is supposed to remember who you are, mixing your
employer's context with your own is the thing you want to avoid.

**Separate keys and endpoints.** Your company's Anthropic key on one side, your own DeepSeek or a
local Ollama on the other. No more editing config between runs.

**Two IM identities.** `channels.yml` and the IM credentials are single-copy per profile, so before
profiles a machine could only wear one bot identity. Now the company Feishu bot and your personal
Telegram can both be live, on two backends, at two ports.

**A strict profile and a loose one.** `permissions.yml` and `audit.log` are per profile, so one can
run in `strict` with everything audited — a client environment, production access — while another
runs in `auto` for your own tinkering.

**A throwaway.** `--profile scratch` is a fresh octo: no memory, no config, onboarding from the
top. Good for reproducing someone's bug, recording a demo, or taking documentation screenshots
without touching your real setup. `rm -rf ~/.octo-scratch` when you're done. Cleaner than faking
`HOME`, since the shared `~/.octo/bin` means you don't reinstall a toolchain to get there.

## Limits worth knowing

- **It is organisation, not security.** Same user, same file permissions. A profile keeps a client's
  credentials *separate*; it does not protect them from anything running as you.
- **Nothing moves between profiles.** A new profile starts empty of your own material. Copying a
  skill or an agent across is a `cp`.
- **Nothing searches across profiles.** Memory and session search stop at the boundary. That is the
  trade you are making.
- **The desktop app opens one at a time.** Two CLI backends can run side by side; two desktop apps
  cannot.
- **There is no `octo profiles list`.** `ls -d ~/.octo*` on the command line, the tray submenu in
  the desktop app.

# octo CLI Reference

Precedence is **CLI flag > env var > `~/.octo/config.yml` > built-in default**. Run `octo <command> --help` for any subcommand's full flag list.

## `octo [message]`

No positional message in a terminal → interactive TUI. A message (or piped stdin) → headless one-shot: full agentic tool loop, then exit.

Frequently-used flags: `-c`/`--continue [id]` (resume), `--provider anthropic|openai|custom`, `--model <name>`, `--no-tools`, `--no-save`, `--no-memory`, `--sandbox`, `--permission-mode interactive|strict|auto`, `--reasoning-effort low|medium|high|xhigh|max`, `--show-reasoning` (web UI trace display, default off — the terminal never renders it regardless), `--quiet`/`--verbose`.

## Subcommands

| Command | Purpose |
|---|---|
| `octo config` [`show`\|`path`] | Interactive setup wizard; print effective settings; print the config file path |
| `octo init` | One-shot run that writes/improves `.octorules` for the current repo |
| `octo memory list`\|`path` | List or locate the project's and inherited memory files |
| `octo skills list`\|`add`\|`update`\|`path` | Manage discovered skills — see `SKILLS.md` |
| `octo hooks list` | List configured lifecycle hooks (not shown in top-level `--help`, but real) — see `HOOKS.md` |
| `octo sessions` | List saved sessions |
| `octo profiles list`\|`create`\|`rm`\|`path` | Manage user-data profiles — see the section below |
| `octo serve` | Start the HTTP server (REST + WebSocket + Web UI + IM bridge) |
| `octo workflows list`\|`path`\|`update` | Manage named multi-step workflows the model runs by name |
| `octo browser setup` | Configure Chrome DevTools Protocol automation |
| `octo upgrade` [`--check`] [`--force`] | Install the latest release in place, or just check |
| `octo completion bash`\|`zsh`\|`fish`\|`powershell` | Print a shell completion script |
| `octo version` | Print version information |
| `octo help [command]` | Top-level help, or a command's detailed help/examples |

### Profiles (`--profile` and `octo profiles`)

A profile is a separate user-data root: the default is `~/.octo`, a profile named `work` is `~/.octo-work`, with its own config, API keys, sessions, memory, skills, `channels.yml` and IM credentials, `permissions.yml`, logs and `serve.addr`. `~/.octo/bin` (helper binaries) is shared by all profiles. Select one for any command with the global `--profile <name>` flag (any position, `--profile=name` also works) or the `OCTO_PROFILE` env var, which octo passes to every child process it spawns. A named profile's first `octo serve` picks the first free port from 8089 and pins it in `serve.addr`; `octo serve --profile <name> status|stop` controls that profile's daemon.

- `octo profiles` / `octo profiles list` — every root on disk with size, path, which one is current, and which has a running backend (pid)
- `octo profiles create <name>` — make an empty `~/.octo-<name>` (a profile is also created implicitly the first time anything runs under its name)
- `octo profiles rm <name> --yes` — permanently delete the root and everything in it; no recycle bin. Without `--yes` it only prints what would go. Refused for the default profile, the profile the command runs under, and any profile whose backend is alive — detected by a live pid in its `serve.pid` or by its pinned address (`serve.addr`) answering, so a foreground `octo serve` counts too (`octo serve --profile <name> stop` first)
- `octo profiles path [name]` — print a profile's data root (no name = the current one)

Interactive `octo --profile <name>` TUI sessions are not detected by `rm`; close them first. Names: letters, digits, `-`, `_`, starting with a letter or digit; `default` is reserved (it labels the unnamed `~/.octo` root and `path`/`rm` accept it as an alias for it). The Web UI has the same list/create/delete under Settings → Data → Profiles (`WEB.md`). Switching the desktop app between profiles is the tray menu's **Profile** submenu (it restarts the app). Full guide: **https://octo-agent.dev/docs/guides/profiles/**.

### `octo serve` flags

`-addr` (default `127.0.0.1:8088`), `--access-key` (required for non-loopback clients), `--no-channel` (skip IM bridges), `-d`/`--daemon` (background), `--stop` (stop a background instance), `--cors`.

## Environment variables

| Variable | Purpose |
|----------|---------|
| `OCTO_PROVIDER` | Which vendor to use (`anthropic`\|`openai`\|`deepseek`\|…). Required when config.yml names none — octo never picks one from a key alone |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | Required for the chosen provider |
| `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` | Override the endpoint |
| `ANTHROPIC_MODEL` / `OPENAI_MODEL` | Default model override |
| `CUSTOM_API_KEY` + `CUSTOM_BASE_URL` | Self-hosted/third-party endpoint (`--provider custom`) |
| `OCTO_ACCESS_KEY` | `octo serve` access key for non-loopback clients |

Full flag-by-flag reference (every `octo [message]` flag, compaction thresholds, `octo serve` self-restart contract): **https://octo-agent.dev/docs/reference/cli/** (`web_fetch`). Self-hosting `octo serve` as a service: **https://octo-agent.dev/docs/guides/self-host/**. Named workflows in depth: **https://octo-agent.dev/docs/guides/workflows/**. Browser automation in depth: **https://octo-agent.dev/docs/guides/browser-automation/**.

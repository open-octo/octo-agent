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
| `octo serve` | Start the HTTP server (REST + WebSocket + Web UI + IM bridge) |
| `octo workflows list`\|`path`\|`update` | Manage named multi-step workflows the model runs by name |
| `octo browser setup` | Configure Chrome DevTools Protocol automation |
| `octo upgrade` [`--check`] [`--force`] | Install the latest release in place, or just check |
| `octo completion bash`\|`zsh`\|`fish`\|`powershell` | Print a shell completion script |
| `octo version` | Print version information |
| `octo help [command]` | Top-level help, or a command's detailed help/examples |

### `octo serve` flags

`-addr` (default `127.0.0.1:8088`), `--access-key` (required for non-loopback clients), `--no-channel` (skip IM bridges), `-d`/`--daemon` (background), `--stop` (stop a background instance), `--cors`.

## Environment variables

| Variable | Purpose |
|----------|---------|
| `OCTO_PROVIDER` | Default provider (`anthropic`\|`openai`\|`custom`) |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | Required for the chosen provider |
| `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` | Override the endpoint |
| `ANTHROPIC_MODEL` / `OPENAI_MODEL` | Default model override |
| `CUSTOM_API_KEY` + `CUSTOM_BASE_URL` | Self-hosted/third-party endpoint (`--provider custom`) |
| `OCTO_ACCESS_KEY` | `octo serve` access key for non-loopback clients |

Full flag-by-flag reference (every `octo [message]` flag, compaction thresholds, `octo serve` self-restart contract): **https://octo-agent.dev/docs/reference/cli/** (`web_fetch`). Self-hosting `octo serve` as a service: **https://octo-agent.dev/docs/guides/self-host/**. Named workflows in depth: **https://octo-agent.dev/docs/guides/workflows/**. Browser automation in depth: **https://octo-agent.dev/docs/guides/browser-automation/**.

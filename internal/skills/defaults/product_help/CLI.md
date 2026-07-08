# octo CLI Reference

## Commands

### `octo [prompt]`
Start an interactive REPL/TUI session, or run a single headless agentic turn and exit (with a prompt argument or piped stdin).

Common flags:
- `-c, --continue [id]` — Resume a session ('last', short ID, or substring; no ID = pick from a list)
- `--take-over` — When resuming, take over a session bound to another entry
- `--no-tools` — Disable built-in tools (terminal, edit_file, …) + MCP/skills
- `--provider <name>` — `anthropic` (default) | `openai` | `custom`
- `--model <name>` — Override the default model for the provider
- `--system <path>` — Path to a custom system-prompt file
- `--no-save` — Don't auto-save the session to `~/.octo/sessions`
- `--no-memory` — Disable cross-session memory injection
- `--sandbox` — OS-enforced confinement for terminal commands (macOS/Linux)
- `--permission-mode <mode>` — `interactive` (default; prompts on ask) | `strict` | `auto`
- `--reasoning-effort low|medium|high|xhigh|max` — Enable extended reasoning (empty/default = off)
- `--show-reasoning` — Surface the reasoning trace for the web UI to display (default off). The terminal itself never renders it regardless of this flag.
- `--quiet` / `--verbose` — Less / more status chrome

### `octo config [subcommand]`
Manage persisted configuration (`~/.octo/config.yml`).

Subcommands:
- `setup` / `init` (default, bare `octo config`) — Interactive wizard to set provider, model, and options
- `show` / `get` — Display current effective configuration and where each value comes from
- `path` — Print the config file path

### `octo init`
Analyze the repo and generate/update `.octorules`. Flags: `-provider`, `-model`, `-permission-mode` (default `strict`), `-sandbox` (+ `-sandbox-allow-net`, `-sandbox-read`, `-sandbox-write`), `-plain` (one-line tool status instead of rich cards).

### `octo sessions`
List recent saved sessions. Resume one with `octo -c <id>` / `octo -c last`, or run bare `octo -c` to pick from an interactive list.

### `octo memory [list|path]`
Manage cross-session memory. `list` shows the current project's memory files; `path` prints the memory directory. See `MEMORY.md`.

### `octo skills [subcommand]`
Manage skills.

Subcommands:
- `list` — List available skills (default, user, project)
- `add <owner/repo[/sub/path] | github.com tree URL> [--force]` — Install a skill from a GitHub repo path (e.g. `octo skills add anthropics/skills/skills/docx`)
- `update` — Refresh default skills from the binary
- `path` — Show skill search paths

See `SKILLS.md` for the embedding/precedence mechanism.

### `octo workflows [subcommand]`
List and manage named workflows (multi-step scripts the model runs by name via the `workflow` tool).

Subcommands:
- `list` — List discovered workflows (default/embedded, user, project) with their args
- `path` — Show workflow search paths
- `update` — Refresh embedded default workflows from the binary

`/workflows` in the TUI shows the same listing.

### `octo browser setup`
Guide through enabling Chrome/Edge remote debugging, verify the connection, and save `browser.connect_port` to config so the `browser` tool can drive your logged-in browser (record/replay, self-heal, vision screenshots).

### `octo hooks list`
Show currently configured hooks (built-ins plus anything from `hooks.yml`). See `HOOKS.md` for the event/config format — note `octo hooks` isn't in the top-level `--help` listing, but it is a real, working feature.

### `octo serve`
Start the HTTP server (REST + WebSocket + embedded web dashboard), and the IM bridge (unless `-no-channel`).

Notable flags: `-addr` (default `127.0.0.1:8088`), `-access-key` (required for non-localhost clients), `-cors`, `-model`, `-max-tokens`, `-no-channel` (disable IM), `-no-memory`, `-d`/`-daemon` (background, pid at `~/.octo/serve.pid`), `-no-supervisor`.

### `octo upgrade`
Download and install the latest release in place. `--check` only compares versions without installing; `--force` proceeds despite a dev build or an already-latest version.

### `octo completion <shell>`
Print a shell-completion snippet. Shells: `bash`, `zsh`, `fish`, `powershell`.

### `octo version`
Print version information.

### `octo help [command]`
Print the top-level help, or a command's detailed help/examples (e.g. `octo help mcp`). `octo <command> --help` prints just that command's flags.

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `OCTO_PROVIDER` | Default provider (`anthropic` \| `openai` \| `custom`) |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | Required for the chosen provider |
| `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` | Override the endpoint (proxies, compatible servers) |
| `ANTHROPIC_MODEL` / `OPENAI_MODEL` | Default model override |
| `CUSTOM_API_KEY` + `CUSTOM_BASE_URL` | Self-hosted/third-party endpoint (`--provider custom`; wire protocol picked in `octo config`) |
| `OCTO_ACCESS_KEY` | `octo serve` access key for non-localhost clients |

# octo CLI Reference

## Commands

### `octo [prompt]`
Start an interactive REPL session or run a single prompt and exit.

Flags:
- `--provider` — Override the default provider (`anthropic` or `openai`)
- `--model` — Override the default model
- `--system` — Path to a custom system-prompt file
- `--reasoning-effort low|medium|high|xhigh|max` — Enable extended reasoning
- `--no-reasoning` — Disable reasoning trace display

### `octo config [subcommand]`
Manage persisted configuration (`~/.octo/config.yaml`).

Subcommands:
- `setup` / `init` (default) — Interactive wizard to set provider, model, and options
- `show` / `get` — Display current effective configuration and where each value comes from
- `path` — Print the config file path

### `octo sessions`
List recent saved sessions. Resume one with `octo -c <id>` / `octo -c last`,
or run bare `octo -c` to pick from an interactive list.

### `octo skills [subcommand]`
Manage skills.

Subcommands:
- `list` — List available skills (default, user, project)
- `path` — Show skill search paths
- `update` — Refresh default skills from the binary

### `octo serve`
Start the web UI server.

### `octo version`
Print version information.

## Environment Variables

| Variable | Purpose |
|----------|---------|
| `OCTO_PROVIDER` | Default provider (`anthropic` or `openai`) |
| `ANTHROPIC_API_KEY` / `OPENAI_API_KEY` | API key for the respective provider |
| `ANTHROPIC_BASE_URL` / `OPENAI_BASE_URL` | Custom endpoint URL |
| `ANTHROPIC_MODEL` / `OPENAI_MODEL` | Default model override |

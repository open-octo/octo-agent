# Troubleshooting

Common issues and their fixes.

## Installation

### `octo: command not found`

- Ensure the binary is on your `$PATH`:
  ```bash
  export PATH="$PATH:/path/to/octo/bin"
  ```
- Or use the full path: `./octo`

### macOS "cannot be opened because the developer cannot be verified"

- Right-click the binary → Open, or run:
  ```bash
  xattr -d com.apple.quarantine /path/to/octo
  ```

## Configuration

### `octo` says "no API key"

- Check that the env var is set: `echo $ANTHROPIC_API_KEY` (or `OPENAI_API_KEY`)
- Or run `octo config` to store the key in `~/.octo/config.yml` (mode 0600)
- Check `octo config show` to see where the provider/model resolve from

### Provider/model not what I expected

Precedence (highest first): CLI flag > env var > config file > built-in default.

- `--provider` / `--model` flags override everything
- `OCTO_PROVIDER` / `ANTHROPIC_MODEL` env vars override the config file
- The config file's model is only honored when the resolved provider matches

## TUI

### TUI doesn't start (falls back to plain REPL)

- TUI requires a TTY. Piped input (`echo "hello" | octo`) runs headless
- Check `octo --help` for `--no-tui` or TTY-related flags

### Image paste (Ctrl+V) doesn't work

- Only works on **macOS** today (uses AppleScript to read clipboard)
- Ensure the clipboard actually contains an image, not a file reference
- Try dragging the image into the terminal instead

### Input history not persisting

- History is saved to `~/.octo/history` (or `$OCTO_HISTORY_FILE`)
- Ensure `~/.octo` is writable

## Serve (Web UI / IM bridge)

### Web UI or IM bot isn't responding

- Web UI and IM bridge both run inside the same `octo serve` process — check it's actually up: `octo serve --status`
- Foreground (no `-d`) prints errors straight to the terminal it was started in
- Started with `-d`/`--daemon`? There's no terminal, so output goes to `~/.octo/serve.log` instead — tail it for the real error:
  ```bash
  tail -f ~/.octo/serve.log
  ```
- The daemon's pid is tracked in `~/.octo/serve.pid`; `octo serve --status`/`--stop` read it directly

### IM channel (Feishu/WeChat/Telegram/DingTalk/WeCom/Discord) not connecting

- The bridge has no log of its own — connection and credential errors land in the same `~/.octo/serve.log` as the rest of `octo serve`
- Credentials live in `~/.octo/channels.yml`; edits made through the Web UI's Channels panel hot-reload immediately, but a direct edit to the file (no filesystem watcher) needs `octo serve --stop` + restart to take effect — see the `channel-manager` skill
- For a precise per-platform status instead of grepping logs, use the `channel-manager` skill's `doctor` subcommand, which reads `/api/channels`
- `octo serve --no-channel` disables the bridge entirely — useful to isolate whether an issue is the bridge or the API server

### "daemon already running" but nothing responds

- A stale pid pointing at a dead process clears itself on the next `--status`/start
- If the pid is alive but the port doesn't answer, the worker is likely stuck mid-startup — check the tail of `~/.octo/serve.log` for the last line logged before it stalled

### Port already in use

- `octo serve --status` tells you whether the process holding the port is octo's own daemon
- Use a different port (`-addr :<port>`) or stop the existing instance (`octo serve --stop`) first

## MCP

### "No MCP servers connected"

- Verify `~/.octo/mcp.json` exists and is valid JSON
- Check that the server command is on `$PATH` (or use absolute path)
- Run with `--verbose` to see connection errors

### MCP server keeps disconnecting

- Some servers (especially stdio-based) crash on malformed input — check their logs
- OAuth servers: delete `~/.octo/mcp-tokens/<server>.json` to force re-auth

## Permissions

### "permission_denied" on safe operations

- Check current mode with `Shift+Tab` (cycles interactive → strict → auto)
- In strict mode, all unmatched operations are denied — switch to interactive
- Or add an allow rule to `~/.octo/permissions.yml`

### Accidentally denied "always allow this session"

- Restart octo — session memory is cleared on exit
- Or switch permission mode to auto (Shift+Tab) temporarily

## Sessions

### Session not found when resuming

- Sessions are saved to `~/.octo/sessions/`
- Resume with `octo -c <session-id>`
- List recent sessions with `/sessions` in TUI

### Session file corrupted

- Sessions are JSON — validate with `python3 -m json.tool ~/.octo/sessions/<id>.json`
- Corrupted sessions are skipped at load; remove the file to clear the error

## Performance

### Slow first turn / long "Thinking" spinner

- Large repos: the initial `.octorules` + file tree scan can take time
- Use `--quiet` to reduce rendering overhead
- Check context usage in the status bar — high ctx% triggers compaction which pauses the stream

### "Context too long" error

- History compaction runs automatically when context usage is high
- If it still fails, start a new session (`/exit` then `octo`)
- Or use a model with a larger context window

## Git

### Co-authored-by not appearing

- Check `octo config show` — `coauthor` should be "on"
- Only applies to commits written by octo (via `terminal: git commit`)
- The trailer is appended automatically; if you edit the message manually, it may be lost

## Getting help

- Run `octo help <command>` for detailed command docs
- Run `/help` in the TUI for keyboard shortcuts
- Check the status bar hints — they change based on current state

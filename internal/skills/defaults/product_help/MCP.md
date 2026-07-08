# MCP (Model Context Protocol) Configuration

octo supports MCP servers that expose external tools, resources, and prompts to the agent. MCP servers are configured via JSON and loaded on session start.

## Config files

Two files are read and merged:

| File | Scope |
|------|-------|
| `~/.octo/mcp.json` | User-global — shared across all projects |
| `<project>/.octo/mcp.json` | Project-local — overrides user-global by server name |

Missing files are silently ignored. Project-local entries with the same name as a user-global entry win.

## Config format

```json
{
  "mcpServers": {
    "server-name": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": {"FOO": "bar"}
    },
    "remote-api": {
      "url": "https://example.com/mcp",
      "headers": {"Authorization": "Bearer ..."},
      "auth": "oauth"
    }
  }
}
```

## Server types

### stdio (local subprocess)

| Field | Required | Description |
|-------|----------|-------------|
| `command` | yes | Executable to spawn |
| `args` | no | Arguments passed to the command |
| `env` | no | Extra environment variables |

Example: filesystem server via npx
```json
{
  "mcpServers": {
    "fs": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/Users/me/projects"]
    }
  }
}
```

### HTTP (remote endpoint)

| Field | Required | Description |
|-------|----------|-------------|
| `url` | yes | MCP endpoint URL |
| `headers` | no | Extra HTTP headers |
| `auth` | no | `"oauth"` for OAuth (Authorization Code + PKCE), or omit |

Example: remote API with OAuth
```json
{
  "mcpServers": {
    "my-api": {
      "url": "https://api.example.com/mcp",
      "auth": "oauth"
    }
  }
}
```

## Auth strategies

- `""` (default) — No automatic auth. Pass tokens manually via `headers`.
- `"oauth"` — Runs RFC-compliant OAuth (Authorization Code + PKCE) on first connect and on every 401. Tokens are cached at `~/.octo/mcp-tokens/<server>.json`.

## Disabling a server

Add `"disabled": true` to keep the config but skip loading it:

```json
{
  "mcpServers": {
    "broken": {
      "command": "npx",
      "args": ["-y", "@some/broken-server"],
      "disabled": true
    }
  }
}
```

## Verification

After editing `mcp.json`, start `octo` and look for the startup line:

```
Connected 2 MCP servers: fs, my-api
```

If a server fails to connect, the error is printed to stderr and that server is skipped — the session continues with the others.

## Tool Search

MCP servers with many tools can push a lot of schema tokens into every request. Tool Search defers those *schemas*: built-in tools stay visible, and every connected MCP tool's name + one-line description stays listed in the system prompt the whole time — only the full parameter schema is loaded on demand, via `mcp_describe` → `mcp_call`.

Control it under `tools.tool_search` in `~/.octo/config.yml`:

```yaml
tools:
  tool_search:
    enabled: auto          # auto | on | off
    threshold_pct: 10      # auto: activate when deferred schemas >= N% of context window
```

- `auto` (default) — enable only when deferred MCP schemas would occupy at least `threshold_pct` of the model's context window.
- `on` — always defer MCP schemas when any MCP tool is connected.
- `off` — upload every MCP schema upfront (legacy behavior).

## Troubleshooting

| Symptom | Fix |
|---------|-----|
| `command not found` | Ensure the binary is on `$PATH` or use an absolute path |
| `connection refused` | Check the URL and network; verify the server is running |
| OAuth loop | Delete `~/.octo/mcp-tokens/<server>.json` to force re-auth |
| Server not appearing | Check JSON syntax; validate with `python3 -m json.tool mcp.json` |

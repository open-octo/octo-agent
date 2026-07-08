# MCP (Model Context Protocol)

octo connects to MCP servers for extra tools — databases, ticket trackers, internal APIs. Tools are on by default; each configured server connects at session start and its tools ride alongside octo's built-ins as `mcp__<server>__<tool>`.

Declared in `~/.octo/mcp.json` (user-global) and `./.octo/mcp.json` (project-local, overrides by server name):

```json
{
  "mcpServers": {
    "github": { "command": "npx", "args": ["-y", "@modelcontextprotocol/server-github"] },
    "linear": { "url": "https://mcp.linear.app/sse", "auth": "oauth" }
  }
}
```

`"auth": "oauth"` on a Streamable HTTP entry runs Authorization Code + PKCE automatically on first `401` — a browser tab opens for consent, the token caches at `~/.octo/mcp-tokens/<server>.json` and auto-refreshes. Check what's connected with `/mcp` in the TUI. A server that fails to start or times out (10s) is logged and skipped; the session continues with the others.

## Tool Search

When MCP servers expose many tools, uploading every schema every turn wastes context. Tool Search keeps every connected tool's name + one-line description listed in the system prompt, and defers the full schema behind `mcp_describe` → `mcp_call`, loaded only on demand. Control it under `tools.tool_search` in `~/.octo/config.yml`:

```yaml
tools:
  tool_search:
    enabled: auto          # auto (default, activates past threshold_pct) | on | off
    threshold_pct: 10
```

Full reference (troubleshooting a broken server, the exact OAuth discovery flow): **https://octo-agent.dev/docs/guides/connect-mcp-servers/** (`web_fetch`).

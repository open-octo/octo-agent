---
name: mcp-creator
description: Configure and connect MCP (Model Context Protocol) servers through guided conversation — find the right server package or endpoint, build the config entry, write it to ~/.octo/mcp.json, and verify the connection. Use when the user wants to add, set up, or connect an MCP server, e.g. "add an MCP server", "connect X via MCP", "set up the filesystem MCP", "添加 MCP", "接入 MCP 服务".
---

# Configure an MCP server

octo connects to MCP servers declared in two config files, both using the
Claude Code-compatible `mcpServers` shape:

- `~/.octo/mcp.json` — user-global; this is where you write.
- `.octo/mcp.json` in a project — project-local; read-only from the web UI,
  only edit it when the user explicitly wants a project-scoped server.

Your job is to turn "I want my assistant to talk to X" into a working entry in
that file. Not every user knows what MCP is — briefly explain terms if in doubt.

## Config schema

```json
{
  "mcpServers": {
    "filesystem": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp"],
      "env": {"KEY": "value"}
    },
    "remote-api": {
      "url": "https://example.com/mcp",
      "headers": {"Authorization": "Bearer …"},
      "auth": "oauth"
    }
  }
}
```

Each entry is **exactly one** of two transports — setting both `command` and
`url` is invalid:

| Field | Transport | Meaning |
|-------|-----------|---------|
| `command` | stdio | Executable to launch (e.g. `npx`, `uvx`, a binary path) |
| `args` | stdio | Argument list, one element per arg |
| `env` | stdio | Extra environment variables for the child process |
| `url` | http | Streamable-HTTP endpoint URL |
| `headers` | http | Static headers, e.g. an API-key `Authorization` |
| `auth` | http | `"oauth"` to run the device-flow OAuth on connect; omit otherwise |
| `disabled` | both | `true` keeps the entry but skips connecting |

Server names must have no whitespace and must not contain `__` (reserved as
the tool-name separator). Prefer short kebab-case names — they prefix every
tool the server exposes.

## Workflow

1. **Understand the goal.** What service or capability does the user want?
   If they name a concrete server/package, skip ahead; if they describe a need
   ("I want it to read my Postgres DB"), help them find a server first.

2. **Find and verify the server.** Search the web or registries for an MCP
   server that fits (the official `modelcontextprotocol` servers, vendor docs,
   or community packages). Verify the package actually exists before writing
   config — check the npm/PyPI registry or the vendor's docs rather than
   guessing a package name. Note what it needs: API keys, paths, account setup.

3. **Choose the transport.**
   - npm package → `"command": "npx", "args": ["-y", "<package>", …]`
   - Python package → `"command": "uvx", "args": ["<package>", …]`
   - Hosted endpoint → `"url"`, plus `headers` for static keys or
     `"auth": "oauth"` when the vendor documents OAuth.

   stdio servers run locally, so the command must exist on this machine —
   check (`npx --version`, `uvx --version`) and help install if missing.

4. **Collect secrets carefully.** If the server needs an API key, ask the user
   where it comes from and put it in `env` (stdio) or `headers` (http). Never
   invent placeholder keys without flagging them as placeholders.

5. **Write the config.** Read `~/.octo/mcp.json` first (create it with
   `{"mcpServers": {}}` if absent), merge the new entry in, and write it back.
   Preserve existing entries verbatim. Echo the final entry back to the user.

6. **Connect and verify.** Tell the user to open the **MCP Servers** panel in
   the web UI and click **Reload** — it re-reads the config and connects every
   server, and the card shows connected status plus the tool count. OAuth
   servers additionally show an **Authorize** button for the device flow. In
   the CLI, `/mcp reload` does the same. If the connection errors, read the
   error off the card (or ask the user to paste it) and iterate.

## Troubleshooting

- **"must set either 'command' or 'url'"** — the entry mixed transports or
  set neither; keep exactly one.
- **stdio server exits immediately** — run the command by hand in the
  terminal to see its real error; usually a missing dependency or API key.
- **401/403 from an http server** — wrong/missing header, or the server wants
  `"auth": "oauth"` instead of a static key.
- **Name conflicts** — entries are keyed by name; writing an existing name
  overwrites that entry. Confirm with the user before replacing one.

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
| `auth` | http | `"oauth"` to run OAuth (Authorization Code + PKCE) on connect; omit otherwise |
| `disabled` | both | `true` keeps the entry but skips connecting |

Server names must have no whitespace and must not contain `__` (reserved as
the tool-name separator). Prefer short kebab-case names — they prefix every
tool the server exposes.

## Editing an existing server

The web UI's per-row "Edit with Agent" button routes here too — for any
existing server, user- or project-scoped alike. This skips most of the
Workflow below; there's no new package to find or transport to choose:

1. **Locate the entry** — check `~/.octo/mcp.json` first, then the current
   project's `.octo/mcp.json` if it's not in the user config.
2. **Show the user the current entry** before changing anything.
3. **Ask what they want changed.** Apply the smallest edit that satisfies
   the request — preserve every other field and every other server entry
   verbatim.
4. **Write the file back**, then do step 6 of the Workflow below (verify,
   tell them to Reload).

If the entry lives in a project's `.octo/mcp.json`: that file is normally
checked into the project's git repo and shared by the whole team. Editing
it through this skill is expected — it's the supported way to change a
project-scoped server (the web UI never writes there directly, by design).
After writing, remind the user the file is version-controlled: they should
review the diff and commit + push (or open a PR) themselves. Don't run git
commands on their behalf unless they ask you to.

## Workflow

Use this for adding a brand-new server. (Editing one? See above.)

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
   - Docker image → `"command": "docker", "args": ["run", "-i", "--rm", "<image>", …]`
     — copy the exact flags from the vendor's docs (env vars via `-e`, volume
     mounts via `-v`, the image tag).
   - Already-installed binary (not npm/Python/Docker) → `"command": "<binary>"`
     with whatever `args` it needs.
   - Hosted endpoint → `"url"`, plus `headers` for static keys or
     `"auth": "oauth"` when the vendor documents OAuth.

   stdio servers run locally, so the command must exist on this machine —
   check (`npx --version`, `uvx --version`, `docker --version`, or the
   binary's own `--version`/`--help`) and help install if missing. The web
   UI's own import API restricts `command` to a short allowlist
   (npx/npm/node/uvx/uv/python/python3/cargo/go/ruby) with no opt-in — that
   restriction doesn't apply here, since you write the config file directly
   rather than calling that endpoint.

   **A bare binary is not always the launch command.** Many tools ship a
   single executable with several subcommands (e.g. `init`, `index`, `serve`)
   and only start an MCP server under a specific one — running the binary with
   no args just prints help and exits, which surfaces later as
   `mcp: initialize: mcp: connection closed`. Before writing `"command": "foo"`
   with no args, run `foo --help` and look for the MCP/serve subcommand and any
   flag it needs (commonly `foo serve --mcp` or `foo mcp`). Verify against the
   tool's docs; don't assume the binary name alone is enough.

4. **Collect secrets carefully.** If the server needs an API key, ask the user
   where it comes from and put it in `env` (stdio) or `headers` (http). Never
   invent placeholder keys without flagging them as placeholders.

5. **Write the config.** Default to `~/.octo/mcp.json` (create it with
   `{"mcpServers": {}}` if absent). If the user explicitly wants this server
   scoped to the current project instead, write to `.octo/mcp.json` in the
   project root (create the file/directory if absent), and afterward remind
   them this file is normally checked into git — they should review the diff
   and commit + push (or open a PR) themselves. Either way: merge the new
   entry in, preserve existing entries verbatim, and echo the final entry
   back to the user.

6. **Verify before handing off.** Don't just write the config and trust it. For
   a stdio server, do a quick smoke test yourself first — launch the exact
   command+args you wrote and confirm the process stays up waiting on stdin
   (an MCP server does not exit immediately; if it prints help and returns, the
   command is wrong — go back to step 3). Only after that, tell the user to open
   the **MCP Servers** panel and click **Reload** (CLI: `/mcp reload`) — it
   re-reads the config and connects every server, and the card shows connected
   status plus the tool count. OAuth servers additionally show an **Authorize**
   button that opens a browser tab to complete the authorization code + PKCE
   flow. If the card still errors, read the error off it
   (or ask the user to paste it) and iterate.

## Troubleshooting

- **"must set either 'command' or 'url'"** — the entry mixed transports or
  set neither; keep exactly one.
- **stdio server exits immediately** (`mcp: connection closed` on connect) —
  run the command by hand in the terminal to see its real error. Usually a
  missing dependency or API key, or a bare binary that needs its MCP/`serve`
  subcommand and flags (see step 3) rather than being launched with no args.
- **401/403 from an http server** — wrong/missing header, or the server wants
  `"auth": "oauth"` instead of a static key.
- **Name conflicts** — entries are keyed by name; writing an existing name
  overwrites that entry. Confirm with the user before replacing one.

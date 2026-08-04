# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Stars](https://img.shields.io/github/stars/open-octo/octo-agent?style=flat-square)](https://github.com/open-octo/octo-agent/stargazers)
[![Discussions](https://img.shields.io/github/discussions/open-octo/octo-agent?style=flat-square&label=discussions)](https://github.com/open-octo/octo-agent/discussions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">简体中文</a> · <a href="README_EN.md">English</a>
</p>

<p align="center">If you find octo useful, give it a ⭐ on GitHub!</p>

> **An open-source, single-binary, self-hosted AI agent.** A coding agent on par
> with Claude Code; as a personal assistant, lighter than OpenClaw — one MIT-licensed
> Go binary, no Node / Python / Ruby, running on **any model** (DeepSeek, Kimi,
> Anthropic, OpenAI, or anything compatible), with the server and your data staying
> on your own machine.

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # single binary — no Node / Ruby / Python
octo config                                            # pick a provider, paste a key (DeepSeek / Kimi / …)
octo "Add a --json flag to 'octo config show' and run the tests"   # one prompt → full agentic loop
```

<p align="center">
  <img src="docs/assets/octo-demo-2.gif" alt="Octo dispatching three sub-agents to explore TUI, IM, and Mobile modules in parallel" width="100%">
</p>

## Install

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
- **Restricted networks** — if github.com is unreachable, use the download mirror (installs and `octo upgrade` also fall back to it automatically):
  `curl -fsSL https://dl.octo-agent.dev/install.sh | sh` (Windows: `irm https://dl.octo-agent.dev/install.ps1 | iex`)
- **Desktop app** — grab the installer from the [latest release](https://github.com/open-octo/octo-agent/releases/latest):
  `octo-setup.pkg` (macOS), `octo-setup.exe` (Windows), `Octo-x86_64.AppImage` (Linux)
- **Go** — `go install github.com/open-octo/octo-agent/cmd/octo@latest`

Upgrade any time with `octo upgrade`. Platform details — Gatekeeper / SmartScreen
warnings, uninstall, building from source — are in the
[install guide](https://octo-agent.dev/docs/getting-started/install/).
The installers aren't code-signed yet; the full policy and how to verify
releases by hash are in [SECURITY.md](SECURITY.md#code-signing-policy).

## Quick start

```bash
octo config                # one-time: pick provider/model, paste an API key
octo "explain this repo"   # headless one-shot: prompt → agentic tool loop → exit
octo                       # interactive TUI in a terminal; octo -c resumes a session
octo serve -d              # Web UI + IM bridge at http://127.0.0.1:8088
```

Built-in tools (shell, file read/edit, search), MCP servers, and skills are on by
default, so a single message can actually do work. Next steps:
[quickstart](https://octo-agent.dev/docs/getting-started/quickstart/) ·
[choose a provider](https://octo-agent.dev/docs/getting-started/choose-a-provider/) ·
[CLI reference](https://octo-agent.dev/docs/reference/cli/).

## Why octo

octo isn't another agent framework you have to "raise." It sits closer to Codex
or WorkBuddy: **download and use, user-friendly**, while keeping model choice,
data ownership, and the runtime firmly in your hands.

### 1. Works out of the box — no "raising" required

Projects like OpenClaw or Hermes often need environment tuning, rule writing, and
skill configuration before the agent runs smoothly. octo enables shell, file
read/write/edit, search, MCP servers, skills, and sub-agents by default, so one
message after install is enough for it to actually do work.

### 2. Native model support, without cache degradation

DeepSeek, Kimi, Qwen, Anthropic, OpenAI, or any OpenAI / Anthropic-compatible
endpoint — octo supports them natively. Prompt caching is tuned per provider;
measured hit rates for Kimi, DeepSeek, and Qwen are all **95%+**. Unlike setups
that front Claude Code with a third-party model and see cache hit rates collapse
from misconfiguration, octo keeps your token bill predictable.

### 3. Eight interfaces, everywhere you are

- **CLI / TUI** — terminal interaction and headless one-shots
- **Web UI** — `octo serve` local dashboard
- **Desktop app** — native window + system tray on macOS / Windows / Linux
- **IM bridge** — WeChat iLink, Feishu, DingTalk, WeCom, Discord, Telegram
- **VS Code extension** — [`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)
- **Obsidian plugin** — [`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)
- **Go SDK** — [`pkg/octoagent`](pkg/octoagent), embed the agent loop in your own programs
- **Mobile** — iOS + Android developer preview ([`mobile/`](mobile/))

Few other agent projects cover this many entry points at once.

### 4. Minimal core: a single ~40 MB Go binary

One command to download, copy to any server, and run. No Node / Python / Ruby
dependency tree; no npm mirror, node-gyp build failure, or version conflict
headaches.

### 5. Zero telemetry

Except for the model API calls you configure, octo sends no outbound traffic on
its own. It collects no IP, device model, model choice, or usage behavior.

### 6. Desktop installer is only about 100 MB

Because of the single-binary + zero-telemetry design, the desktop installer
is around **100 MB**. Compare that to Codex desktop and WorkBuddy, which
often weigh in around **1 GB**. A thin agent harness shouldn't need that much
space.

### 7. Stable and safe — won't edit itself dead, won't go rogue

- The **terminal** tool refuses any `kill` / `pkill` / `killall` aimed at octo's
  own process, including detours like `kill $(pgrep octo)`.
- **restart_server** is hard-wired to ask permission first — a browser modal on
  the web, an explicit reply on IM. Even after approval, the restart is graceful:
  the current turn drains so your reply reaches you before the supervisor
  respawns the server and clients reconnect.
- Every delete is validated. Catastrophic commands like `rm -rf /` and `rm -rf ~`
  are **hard-coded denies** that custom permission rules cannot override. Ordinary
  file deletes and `write_file` / `edit_file` overwrites are first backed up to a
  recycle bin (default 14 days / 10 GiB), so nothing is ever truly gone.

### 8. Only octo does all of the above

Any one of these points might be covered by some other product. But only octo
combines out-of-the-box usability, native multi-model support, eight interfaces,
a single binary, zero telemetry, a small footprint, high stability, and strong
safety.

### 9. One last word

If you already have reliable access to a Codex or Claude subscription, keep using
it — they remain the best agent harnesses on the planet. Otherwise, octo is worth
a serious look.

## Interfaces

**Stable (1.0)** already covers CLI, Web UI, Desktop, IM bridge, VS Code / Obsidian
extensions, and Go SDK. The eighth — a mobile app (iOS + Android) — is implemented
and in **developer preview**: buildable from source today against a self-hosted
relay (see [`mobile/`](mobile/)), with a hosted relay and app-store builds next.

What you can build on is declared in [COMPATIBILITY.md](COMPATIBILITY.md); the security
boundary in [SECURITY.md](SECURITY.md).

## Learn more

The full documentation lives at **[octo-agent.dev/docs](https://octo-agent.dev/docs/)**:

- [Skills](https://octo-agent.dev/docs/guides/use-skills/) — Claude Code-compatible SKILL.md; symlink `~/.claude/skills` and reuse what you have
- [Sandboxing & recycle bin](https://octo-agent.dev/docs/guides/sandbox-the-agent/) — OS-enforced confinement (Seatbelt / Landlock), plus a file-level trash that backs up agent deletes and overwrites
- [MCP servers](https://octo-agent.dev/docs/guides/connect-mcp-servers/) — stdio + HTTP, OAuth, and Tool Search for large tool sets
- [Memory](https://octo-agent.dev/docs/guides/memory/) · [Sub-agents](https://octo-agent.dev/docs/guides/sub-agents/) · [Workflows](https://octo-agent.dev/docs/guides/workflows/) — persistence and multi-agent orchestration
- [Browser automation](https://octo-agent.dev/docs/guides/browser-automation/) — CDP record / replay / self-heal
- [IM channels](https://octo-agent.dev/docs/guides/channels/) — hook octo up to your chat apps
- [Configuration](https://octo-agent.dev/docs/reference/config-file/) · [Permissions](https://octo-agent.dev/docs/reference/permissions/) · [Tools](https://octo-agent.dev/docs/reference/tools/)
- [Architecture](https://octo-agent.dev/docs/architecture/system-layers/) — the layered design, provider protocols, and how to extend it

## Community

- **Bugs / feature requests** — [GitHub Issues](https://github.com/open-octo/octo-agent/issues)
- **Questions / discussion** — [GitHub Discussions](https://github.com/open-octo/octo-agent/discussions), public and searchable so the next person finds the answer
- **WeChat group** (Chinese-speaking users) — scan to join the user group. The QR code
  expires every 7 days and gets refreshed; if it no longer scans, ping us in Discussions:

<p align="left">
  <img src="docs/assets/wechat-group-qr.jpg" alt="octo-agent WeChat group QR code" width="200">
</p>

## Development

```bash
make build         # ./octo
make test          # go test -race ./...
```

Project conventions live in [`CLAUDE.md`](CLAUDE.md) and [`.octorules`](.octorules);
the PR workflow in [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Prior art & acknowledgements

octo stands on the shoulders of two projects and doesn't pretend otherwise:
**[Claude Code](https://code.claude.com)**, whose agent loop, tool set, SKILL.md
format, and harness behavior shaped octo's internal design; and
**[OpenClacky](https://github.com/clacky-ai/openclacky)**, which inspired much of
the UI and interaction design. Any bugs or bad decisions are octo's own.

## Contributors

Thanks to everyone who has contributed to octo:

<!-- Written out by hand rather than generated by contrib.rocks: that single
     image goes through GitHub's camo proxy, whose edges cache inconsistently,
     so different visitors can see a different number of people at the same
     moment and a new contributor may stay invisible for an unbounded time.
     Leaving someone out is worse than maintaining this list. Add a line here
     when someone new lands a change. -->
<p>
  <a href="https://github.com/Leihb"><img src="https://avatars.githubusercontent.com/u/28055438?v=4&s=64" width="64" height="64" alt="Leihb" title="Leihb" /></a>
  <a href="https://github.com/eternalweightlessness"><img src="https://avatars.githubusercontent.com/u/210714574?v=4&s=64" width="64" height="64" alt="eternalweightlessness" title="eternalweightlessness" /></a>
  <a href="https://github.com/kunyuanhe-sudo"><img src="https://avatars.githubusercontent.com/u/292632541?v=4&s=64" width="64" height="64" alt="kunyuanhe-sudo" title="kunyuanhe-sudo" /></a>
</p>

The full record lives in the [contributors graph](https://github.com/open-octo/octo-agent/graphs/contributors).

## License

MIT. See [`LICENSE.txt`](LICENSE.txt).

# octo-agent

[![Go CI](https://img.shields.io/github/actions/workflow/status/open-octo/octo-agent/go.yml?label=ci&style=flat-square)](https://github.com/open-octo/octo-agent/actions)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Go](https://img.shields.io/badge/go-%3E%3D%201.25-00ADD8?style=flat-square)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">简体中文</a> · <a href="README_EN.md">English</a>
</p>

> **An open-source, single-binary, self-hosted AI agent.** A coding agent on par
> with Claude Code and a personal assistant lighter than OpenClaw — one MIT-licensed
> Go binary, no Node / Python / Ruby, running on **any model** (DeepSeek, Kimi,
> Anthropic, OpenAI, or anything compatible), with the server and your data staying
> on your own machine.

<p align="center">
  <img src="landing/assets/demo.gif" alt="octo demo" width="760">
</p>

```bash
curl -fsSL https://octo-agent.dev/install.sh | sh     # single binary — no Node / Ruby / Python
octo config                                            # pick a provider, paste a key (DeepSeek / Kimi / …)
octo "Add a --json flag to 'octo config show' and run the tests"   # one prompt → full agentic loop
```

## Install

- **Linux / macOS** — `curl -fsSL https://octo-agent.dev/install.sh | sh`
- **Windows** — `irm https://octo-agent.dev/install.ps1 | iex`
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

octo isn't trying to out-feature the big agents; it's the **open, self-hostable,
vendor-neutral** take on the same idea — an opinionated one, in the Rails spirit:
convention over configuration, omakase defaults over infinite choice.

|  | **octo-agent** | Claude Code |
|---|---|---|
| License / cost | **MIT, free, self-hosted** | proprietary; most surfaces need a Claude subscription |
| Runtime | **one self-contained Go binary** | native install tied to an Anthropic account |
| Models | **both protocols + any compatible endpoint** (DeepSeek/Kimi/Bailian/OpenRouter/vLLM) | Anthropic-first |
| Deployment / data | **fully self-hosted — server and data stay yours** | Anthropic-managed for most surfaces |
| Skills | same SKILL.md format — reuse your Claude Code skills | native (the format's origin) |

On the personal-assistant side, [OpenClaw](https://github.com/openclaw/openclaw)
is the closest kin. octo covers the same ground — self-hosted, MIT, reaches you on
the chat apps you already use — but as one static binary instead of a Node.js app
with a dependency tree, and with a full coding-agent core built in.

## A note from the author

I'm a heavy Claude Code user myself, and I think it's the best coding agent there
is. octo exists because a great agent experience shouldn't depend on things that
have nothing to do with your skills: whether you can afford a $20–200/month
subscription, whether your credit card and network can reach the vendor at all
(for many users — in China especially — they can't), and whether your code is
allowed to leave your machine.

Three beliefs drive the project:

- **Open models are good enough now.** DeepSeek, Kimi, and Qwen have closed most
  of the day-to-day gap — what's usually missing is the harness around them: the
  tool loop, permission gating, skills, memory, sub-agents. octo gives them the
  same harness at one to two orders of magnitude lower cost, with both wire
  protocols implemented natively rather than through a compatibility shim.
- **The harness shouldn't degrade off-Anthropic.** A popular route is plugging
  third-party models into Claude Code through a router (cc-switch and kin), but
  two things quietly break: prompt caching is tuned for Anthropic's own endpoint,
  so hit rates — and your token bill — suffer; and server-side capabilities like
  web search and tool search simply stop working. octo does both on its own side:
  caching is tuned per provider (measured 95%+ hit rates on Kimi, DeepSeek, and
  Qwen), and web search / tool search are built into the harness, working the
  same on every model.
- **Your data should only pass through your own machine.** No cloud, no accounts,
  no telemetry in the codebase — the only outbound traffic is the model API you
  configure and GitHub for update checks. For people working inside a corporate
  network where code cannot leave, this is a precondition, not a nice-to-have.
- **The agent should live where you already are.** IM bridges (WeChat iLink,
  Feishu, DingTalk, WeCom, Discord, Telegram) are core features, not
  afterthoughts — assign a task at your desk, follow up from your phone.
- **The agent must not saw off the branch it's sitting on.** My most infuriating
  OpenClaw/Hermes memory: chatting with the agent from my phone, mid-discussion
  it decides to edit code and restart itself — then silence until I got home to
  revive it. octo defends this scenario specifically: the terminal tool refuses
  any kill aimed at octo's own process (including `kill $(pgrep octo)`-style
  detours); the only restart path is a dedicated `restart_server` tool,
  hard-wired to ask permission first (browser modal on web, explicit reply on
  IM); and even an approved restart drains the current turn so the reply
  reaches you before the supervisor respawns the server and clients reconnect.
- **A probabilistic model will eventually go insane on you.** One day the agent
  decides "the environment is broken", "everything I wrote is wrong", "this
  file is a virus" — and really deletes a day of your work. On octo every
  delete is checked: catastrophic commands (`rm -rf /`, `rm -rf ~`) are
  hard-coded denies that even your own permission rules cannot re-allow;
  ordinary deletes are copied to a recycle bin before they run, and file
  overwrites are backed up the same way. Within the default 14-day / 10 GiB
  window, nothing the agent deletes is ever truly gone.
- **Frontier features, without the gates.** In June 2026 Claude Code shipped
  dynamic workflows and Codex shipped record & replay (macOS only). octo has
  both, on its own terms: a workflow fans out at most 8 concurrent sub-agents
  (the rest queue — every task still completes, the token bill stays bounded),
  and browser record / replay, self-healing included, treats Windows and Linux
  as first-class.

And one honest word: if you have a Claude subscription and you're happy with it,
keep using Claude Code — it earns its price. octo is for everyone the
subscription doesn't reach. The SKILL.md format is shared, so you can even run
both: Claude Code for the heavy lifting, octo on DeepSeek for everything else.

## Interfaces

**Stable (1.0).** Eight interfaces are planned — one per arm of the octopus — and seven are live:

- **CLI** — interactive TUI in a terminal, headless one-shot everywhere else
- **Web UI** — `octo serve`, a local dashboard over REST + WebSocket
- **Desktop app** — native window + system tray (macOS / Windows / Linux)
- **IM bridge** — WeChat iLink, Feishu, DingTalk, WeCom, Discord, Telegram, inside `octo serve`
- **VS Code extension** — [`open-octo/octo-vscode`](https://github.com/open-octo/octo-vscode)
- **Obsidian plugin** — [`open-octo/octo-obsidian`](https://github.com/open-octo/octo-obsidian)
- **Go SDK** — [`pkg/octoagent`](pkg/octoagent), embed the agent loop in your own programs

The eighth, a mobile app, is landing next. What you can build on is declared in
[COMPATIBILITY.md](COMPATIBILITY.md); the security boundary in [SECURITY.md](SECURITY.md).

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

<a href="https://github.com/open-octo/octo-agent/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=open-octo/octo-agent" alt="Contributors" />
</a>

## License

MIT. See [`LICENSE.txt`](LICENSE.txt).

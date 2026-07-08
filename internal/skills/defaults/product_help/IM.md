# IM bridge — chat with octo from WeChat, Feishu, Telegram, etc.

octo can run as a chat bot on WeChat (iLink), Feishu, DingTalk, WeCom, Discord, and Telegram. The bridge runs **inside `octo serve`** (not a separate process) — each platform has its own adapter under `internal/channel/adapters/`, and every adapter shares the same agent loop, tools, skills, memory, and permission engine as the CLI/web surfaces.

## Setup

The primary path is the **Channels panel in the web UI** (`octo serve`, then open the dashboard): it walks through connecting each platform — for WeChat iLink this is scan-to-login (a QR code), other platforms are configured with their own credentials (bot token, app ID/secret, etc., depending on the platform).

Credentials persist to `~/.octo/channels.yml` (mode 0600):

```yaml
channels:
  telegram:
    bot_token: "..."
  discord:
    bot_token: "..."
```

Each platform's config keys are its own (`PlatformConfig` is an open map), set via the web wizard rather than documented as a fixed schema here — use the Channels panel rather than hand-authoring this file. Channel config hot-reloads — enabling/editing a platform in the web UI takes effect without restarting `octo serve`.

## What a chat gets

Each IM chat is a session like any other — per-user sessions, slash commands, tool use, skills, and (if enabled) a session goal all work the same as the TUI. A sustained "typing…" indicator is kept alive on the platforms that support it while an agentic turn is in progress. After each turn, the chat gets a short summary line: `⏱ <elapsed>, <tokens> tokens`.

## Proactive messaging (`send_message` / `send_file`)

A normal reply only reaches the chat the current turn is already talking to. To reach a **different** chat — e.g. the user is on the web UI and asks octo to message their WeChat — the model uses the `send_message` (text) or `send_file` (file) tools, addressed by `platform` + `chat_id`. If the model doesn't know the `chat_id`, calling the tool with just a platform (or no arguments) returns the list of chats it can currently reach; a chat only appears once it has messaged the bot at least once (that's how the bot learns its `chat_id`). These tools are only registered when a messenger is active (i.e. `octo serve` is running with channels enabled) — they don't appear in a plain CLI/TUI session.

## Disabling the bridge

`octo serve -no-channel` starts the web server without the IM bridge.

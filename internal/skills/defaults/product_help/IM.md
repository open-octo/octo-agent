# IM bridge — chat with octo from WeChat, Feishu, Telegram, etc.

octo can run as a chat bot on WeChat (iLink), Feishu, DingTalk, WeCom, Discord, and Telegram. The bridge runs **inside `octo serve`** — no separate process. Each platform is connected, tested, and can send a one-off test message from the web UI's **Channels** panel (WeChat is scan-to-login; the rest use app/bot credentials). Credentials persist to `~/.octo/channels.yml` and hot-reload — no restart needed after editing a platform in the panel.

Each chat is a session like any other — per-user history and permission context, slash commands (a different set than the TUI/web; see the reference below), attachments bridge both ways, and a session goal works the same as elsewhere.

## Proactive messaging (`send_message` / `send_file`)

A normal reply only reaches the chat the current turn is already talking to. To reach a **different** chat — e.g. the user is on the web UI and asks octo to message their WeChat — the model uses `send_message`/`send_file`, addressed by `platform` + `chat_id`. Calling with just a platform (or no arguments) returns the list of chats it can currently reach; a chat only appears once it has messaged the bot at least once. These tools only appear when a messenger is active (`octo serve` running with channels enabled).

`octo serve -no-channel` starts the web server without the IM bridge.

Full platform setup notes, IM-specific slash commands (`/bind`, `/unbind`, `/new`, `/stop`, `/status`, `/list`), and attachment handling: **https://octo-agent.dev/docs/guides/channels/** (`web_fetch`).

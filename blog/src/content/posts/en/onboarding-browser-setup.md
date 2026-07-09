---
title: "Octo Onboarding Series (9): Browser in Practice — Hand octo Your Own Browser"
description: "The browser tool isn't another headless framework — it attaches to your real, already-logged-in Chrome, and a click or form submit is treated as a real action that needs approval."
pubDate: 2026-07-17
author: "octo-agent team"
tags: ["onboarding", "octo-agent", "browser"]
locale: en
originalSlug: onboarding-browser-setup
---

# Octo Onboarding Series (9): Browser in Practice — Hand octo Your Own Browser

> The first eight posts all covered tasks with something to call — files, shell, MCP, workflows, goals. But a lot of everyday work has no API at all: clicking through an internal admin panel, filling in a form on a site that only has a UI. octo can only help there once it has a real browser to drive. This post sets that up.

---

## Not another headless-browser framework

octo's `browser` tool drives a real Chrome tab directly over the Chrome DevTools Protocol (CDP) — no Puppeteer, no Playwright, no extra runtime to install. That's not an implementation detail, it's a capability difference: it operates on **the Chrome you're already signed into**, not a blank, cookie-less automation profile. Whatever you're logged into on some internal system, octo can just use it.

## One command to wire it up

```bash
octo browser setup
```

This walks you to `chrome://inspect/#remote-debugging` and has you tick "Allow remote debugging for this browser instance" (Chrome 144+ disabled the old `--remote-debugging-port` flag on the default profile, so the inspect-page checkbox is the supported path today), restarting the browser if needed. Once ticked, `octo browser setup` probes port 9222 in a loop — and it checks more than "did it connect": it makes an actual page-level CDP call, because on recent Chrome a browser-level connection can succeed while page control still fails. Once that succeeds, the port is saved to your config, so every future `octo` run reuses it without repeating this step.

If you don't finish right away, that's fine — the command waits for you to confirm the toggle is on, retries on Enter, or quits any time on `q`. Run `octo browser setup` again whenever you're ready.

## Connect order: three paths, none silently downgrade into the next

The `browser` tool tries, in a fixed order, to get hold of a page it can drive:

1. **A known debug port** (`browser.connect_port` in config) — connects directly; a failure here does not fall through to the next option. This is the path `octo browser setup` wires up for you.
2. **Attach to an already-running, remote-debugging-enabled Chrome** (`browser.attach_running: true`) — reuses your logged-in session, but only ever attaches to a browser that already opted into debugging; it will never hijack an ordinary Chrome window that didn't.
3. **Launch a brand-new temporary profile** — the fallback when neither of the above is configured or reachable, with none of your logins.

The matching config lives in `~/.octo/config.yml`:

```yaml
browser:
  attach_running: true   # reuse your logged-in Chrome instead of a temp profile
  connect_port: 9222      # or attach via --remote-debugging-port
  headless: false          # off by default — interactive workflows need to be watchable/interruptible
  user_data_dir: ""
  exec_path: ""
  download_dir: ""
```

Every field has a matching CLI flag for a one-off override without touching the config file.

Whichever path connects, octo always opens a **brand-new tab** rather than reusing one you already have open (including its own web UI's tab) — to drive an existing tab, call `pages`/`select_page` explicitly. The connection is reused for the whole session, and the debugger authorization prompt only appears once, not on every navigation.

## Just say what you need — it figures out where to click

You don't need to know a single CSS selector; just describe the task:

```text
Open the internal ticketing system and mark ticket #4821 as done.
```

Under the hood, octo roughly: `navigate`s to the target page → `observe`s (or, at a lower level, `ax` — the accessibility tree) to see which elements are actually interactive and what their real selectors are → `click`/`type`/`select`s to carry out the action → `wait`s for async content, either a fixed timeout or `network_idle`, which settles once the page's fetch/XHR traffic actually stops — much more robust than waiting a fixed delay and hoping.

A few actions worth calling out specifically:

- **Uploading a file** goes through `upload`, passing an absolute path straight to the file `<input>`, rather than clicking an upload button — a click opens a native OS file dialog that can't be automated.
- **Reading something inside shadow DOM, or anything `click`/`type` can't reach**, `eval` runs a raw JS expression and gets you past what CSS selectors can't pierce.
- If a step opens a new tab (say, a link that opens in a new window), subsequent actions automatically follow onto that new tab — no manual `select_page` needed.

## Permissions and vision

Browser actions go through the exact same permission engine as every other tool — clicking or submitting a form on a logged-in account is treated as a real, high-impact action that needs approval, not waved through as read-only. Screenshots only actually hand an image to the model when the active model supports vision; a text-only model gets a plain text note instead of an image block it would just reject.

---

## Next: record it once, replay it forever

Once the browser is wired up, a one-off task is fine to let octo watch-and-click through live. But if you do the same thing every week — filling out a weekly report, exporting an invoice — having the model re-observe-and-decide every single time is wasted work. The next post covers **recording** that sequence once, distilling it into a script that replays deterministically and can even fix its own selectors.

**Previous in the series**: [Octo Onboarding Series (8): Goal in Practice — Set a Standing Objective and Let It Find Idle Time to Push On](/blog/posts/en/onboarding-goal-long-running-migration/)
**Next in the series**: [Octo Onboarding Series (10): Record & Replay in Practice — Record Your Actions Once, Let octo Replay Them](/blog/posts/en/onboarding-browser-record-and-replay/)

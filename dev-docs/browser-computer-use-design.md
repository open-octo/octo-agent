# Browser Computer-Use & Skill Recording

octo's first-class capability for **operating a real browser** and for
**recording a human demonstration into a replayable skill** — the foundation for
replacing brittle RPA with something an LLM can run, repair, and a human can edit.

It is a core pillar, so it is octo-owned and in-binary: a pure-Go CDP client
(`internal/browser`) drives the user's own Chrome over the DevTools Protocol
(websocket + JSON, no CGO, cross-platform). The only native dependency is the
user's Chrome. The agent reaches it through one action-multiplexed `browser`
tool (`internal/tools/browser.go`).

## Goals / Non-goals

**Goals**
- A Go-native browser backend with no extra runtime (no Node), driving the
  user's real logged-in Chrome.
- **Record** a human demonstration and compile it into an inspectable, editable,
  version-controllable skill.
- **Replay** deterministically without an LLM, self-checking each step, and hand
  to an LLM-driven repair only when a step actually fails.
- Replay that survives ordinary page change (layout drift, links opening in new
  tabs, SPA timing) rather than breaking on the first difference.

**Non-goals**
- Desktop (native app) automation — see `desktop-computer-use-design.md`.
- Pixel/coordinate-based interaction, visual target matching, or
  native-computer-use model integration. Targets are resolved through the DOM
  (selector + recorded text), not screenshots.
- Guaranteed evasion of anti-bot detection — best-effort, see Reliability.

## Architecture

```
┌────────────────────────────────────────────────────────────┐
│  Agent loop  ──  browser tool (action-multiplexed)           │
│                  record / replay engine (internal/browser)   │
└───────────────────────────┬──────────────────────────────────┘
                            │ CDP (websocket + JSON)
                      ┌─────┴─────┐
                      │  Chrome   │  user's real, logged-in instance
                      └───────────┘
```

The session holds one Chrome connection, package-global in `internal/tools`
(`browserSession`) and reused across tool calls and chat sessions, so
`navigate → click → …` share one page and Chrome's remote-debugging
authorization prompt appears once per process.

## Connecting to Chrome

Chrome 144+ disabled `--remote-debugging-port` on the default profile, so the
supported path is the **chrome://inspect checkbox**: open
`chrome://inspect/#remote-debugging`, tick *"Allow remote debugging for this
browser instance"*, which serves CDP on `127.0.0.1:9222`. The debug port is
fixed at **9222** (the checkbox's port); it is not user-editable in the setup UI
— a custom port only via config (`browser.connect_port`).

`browserPage` (`internal/tools/browser.go`) resolves the connection by config:
`connect_port` → `ConnectByPort` (`/json/version`, falling back to the fixed
`/devtools/browser` ws path); `attach_running` → `DiscoverRunningChrome` /
`ConnectViaProfile` (reading the profile's `DevToolsActivePort`); otherwise
launch a fresh Chrome. It then **always opens its own tab** (`NewPage`) — never
hijacking a tab the user already has open, including octo's own web UI. Cookies
and login are profile-wide, so a new tab still carries the logged-in session.

`octo browser setup` (`cmd/octo/browser.go`) is the onboarding helper: it prints
the inspect-page steps, probes the attach (connect + a page-level check), and on
success persists `browser.connect_port`.

**Connection liveness.** `browserPage` separates *page* lifetime from
*connection* lifetime. A cached page is probed (a cheap `Eval`) before reuse; if
its CDP target is gone (a tab opened in an earlier session has since closed) it
opens a fresh tab **on the existing browser connection** — no redial, so Chrome
does not re-prompt for authorization. It only redials when the connection itself
is dead (Chrome closed/restarted).

## The `browser` tool

One action-multiplexed tool. Actions: `navigate`, `back`, `click`, `hover`,
`type`, `select`, `key`, `scroll`, `wait`, `screenshot`, `observe`, `ax`,
`cookies`, `upload`, `download`, `pages`, `select_page`, `close`, `eval`,
`record_start`, `record_stop`, `replay`.

Notable behaviours:
- **`observe`** returns a text digest of the page's interactable elements
  (URL/title + element → selector). Model-agnostic, the cheap way to look at an
  unfamiliar page; works on any model with no vision.
- **`screenshot`** returns an image for a vision-capable model. It is gated on
  the active model's vision capability (`tools.SetBrowserVision`, set from the
  model config); a text-only model gets a text note instead of an image block
  its endpoint would reject. Vision is opt-in and never forced.
- **`cookies`** returns the current page's cookies including HttpOnly (CDP
  `Network.getCookies`) for session reuse / token extraction.
- **Click follows new tabs.** A click that opens a tab (`target=_blank` /
  `window.open`) switches the active page to the new tab. Without this a click
  that spawns a tab looks like a no-op because the original page is unchanged.
- **Per-action timeout** bounds every action (45s default; 5min for `replay`
  / `download`) so a stuck CDP call fails instead of hanging the turn.
- **Selectors** accept plain CSS or a Playwright-flavored subset translated to a
  CDP-evaluable query (`internal/browser/selector.go`): the text engines
  (`:has-text` / `:text` / `:contains` / `:text-is`, the `text=` prefix), the
  `:visible` pseudo, and the `xpath=` / `css=` engine prefixes. Models trained on
  Playwright emit these, so they just work.

## Recording

`record_start` installs a recorder (`internal/browser/recorder.go`) on the
current page; `record_stop <name>` compiles what was captured into a skill.

**Recording captures the USER's demonstration, not the model's actions.** After
`record_start` the agent hands control to the user, who performs the steps in
their own browser and says when done. The recorder injects capture-phase
`click` / `change` DOM listeners that report through a `Runtime.addBinding` →
`Runtime.bindingCalled` channel, re-installed on every future document
(`Page.addScriptToEvaluateOnNewDocument`) so it survives navigation. It also
subscribes to `Page.frameNavigated` (top frame only) to capture top-level
navigations as steps — so a "go to /hot, then click an item" demonstration
records both the navigation and the click, not just the click.

Each captured event carries: action type (`click` / `change`(→`type`/`select`) /
`upload` / `navigate`), a best-effort CSS selector, the element's visible text,
tag, an iframe selector when framed, the typed/selected value, and the URL. The
selector is **anchored at the nearest id-bearing ancestor**
(`#panel > div > a`) rather than a free `nth-of-type` chain from `<body>` —
shorter and far more stable across layout changes.

Known limit: actions performed *inside a new tab opened during recording* are
not captured (the recorder stays attached to the origin target). The common
"click opens an article" flow is covered because the click is captured on the
origin page and replay follows the popup.

## Recording artifact

A recording compiles to an editable YAML recording at
`~/.octo/browser-recordings/<name>.yaml` (`tools.BrowserRecordingsDir`,
`$OCTO_BROWSER_RECORDINGS_DIR` to override, `$OCTO_BROWSER_SKILLS_DIR` still honored for migration):

```yaml
name: open-zhihu-top
description: Open Zhihu's hot list and click the first item
params:
  - { name: query, description: "...", default: "" }
steps:
  - action: navigate
    url: https://www.zhihu.com/hot
  - action: click
    selector: "section:nth-of-type(1) > div:nth-of-type(2) > a"
    label: "Top story title"          # recorded visible text — the text anchor
```

`Step` fields: `action`, `url`, `frame`, `selector`, `value`, `label`,
`timeout_ms` (for `wait`), `verify`. `{{param}}` placeholders in `url`/`value`
are substituted at replay from `params` (falling back to declared defaults). The
skill is plain YAML — human-readable, hand-editable, git-versionable.

**Distillation.** `record_stop` runs `GenerateRecording`:
- `CompileRecording` produces a deterministic baseline: navigations and gestures in
  capture order; it drops an `about:blank` start URL (the throwaway tab octo
  opened), collapses initial-load navigation echoes, and removes a step
  identical to its immediate predecessor (a jittery double-fire) — conservative,
  exact-consecutive-dupes only.
- When a model sender is wired, an LLM **refines** that baseline: drops
  back-and-forth detours, parameterizes user-specific values into `{{param}}`,
  and writes a short description. A precision guard (`selectorsSubset`) rejects
  any refined output that invents a selector not in the baseline, falling back to
  the deterministic version — so the LLM only ever cleans up real captured
  events, never hallucinates targets.

## Replay, verification, self-heal

`replay <name>` loads the YAML and runs `ReplayRecording` (`internal/browser/
recording.go`) deterministically — no LLM in the common case. Each step implicitly
waits for its target, executes, and checks any `verify`. Robustness is built
into the engine:

- **Text-anchored clicks.** Positional selectors drift and, worse, after a
  layout change can silently resolve to the *wrong* element. When a step has the
  recorded `label` text, `resolveClickTarget` polls for, in order: the original
  positional element when its text still contains the anchor (unchanged-page fast
  path, preserving the exact original target), else any element carrying the
  anchor text (drift recovery), else the positional selector unchanged (a real
  miss then reaches the healer). This stops silent wrong-element clicks and lets
  replay survive layout change without the LLM.
- **Popup following.** A click that opens a new tab swaps the active page for the
  subsequent steps; `ReplayRecording` returns the final page so the tool keeps the
  session pointed at the right tab.
- **Type verification.** After a `type`, if a non-empty value left the field
  empty (a disabled / re-rendering / framework-controlled input swallowed the
  programmatic insert), the field is cleared and re-typed once before failing.
  The check is non-empty, not exact-match, so masked/formatted inputs are not
  wrongly flagged.
- **`wait`** waits for a selector, or a fixed `timeout_ms` — the SPA-settling
  primitive.

```
for each step:
    resolve target (text-anchored for clicks), waiting for it to appear
    execute via the CDP backend (deterministic, no LLM); follow any new tab
    verify (implicit target presence + any explicit verify)
    if it fails AND a healer is wired:
        healer inspects the page and repairs the step, then retry once
        if the repair changed the step, write the corrected skill back
```

**Self-heal** (`app.MakeBrowserHealer`) is the only LLM call in replay and only
on a hard failure: it shows the model the page's current interactable elements (a
text digest — model-agnostic, no vision) plus the intended action + label, and
asks for the corrected selector, which is written into the step for retry and
persisted (`replay` saves the healed skill back). This self-heal is the
difference between this and a brittle RPA macro.

## Managing recordings (web Browser view)

A dedicated **Browser** view (`web/src/views/BrowserView.svelte`,
`GET/PUT/DELETE /api/browser/recordings`) owns the connection status (Connected /
Unreachable / Not set up + port, with a single Set up / Reconfigure button) and
the recordings list. Each recording offers **Replay**, **Edit**, **Delete**, and
the section has a **Record** button.

All three actions are **agentic and consistent**: Replay, Record, and Edit each
open a fresh agent session (`openAgentSession`) whose first message drives the
flow — Replay runs `replay` through the full agent replay+heal path; Record
walks the demonstrate-then-stop flow; Edit reads the YAML and edits it
conversationally. There is no server-side replay endpoint and no raw-YAML modal
editor — both would be inconsistent with the rest of the agentic-first UI. (The
after-turn message suggestion is suppressed in these single-purpose sessions.)

**Recordings are replay-only.** They run when explicitly replayed (the Replay
button or `replay`); they are not keyword-triggerable. (A companion-`SKILL.md`
mechanism that auto-triggered a recording by phrase was tried and removed: it was
unreliable and led the agent to over-promise auto-execution that often did not
fire. A recording is an automation the user replays, distinct from a SKILL.md the
model reads as knowledge.)

## Permissions & safety

Browser actions (click, type, navigate, upload) are high-impact — they take real
actions on the user's logged-in accounts — and route through octo's permission
gate with clear action summaries. Screenshots may capture sensitive on-screen
content; they are produced only for a vision-capable model and on demand.

## Reliability & anti-detection (honest limits)

Anti-bot detection (CDP/headless fingerprinting) is orthogonal to replay
robustness and is an arms race with no guarantee. Mitigations: trusted `Input`
gestures (a real user gesture — also what triggers file dialogs), the user's real
logged-in profile rather than a headless instance, and human-paced timing. Some
sites still detect automation and may **ban accounts**; this is surfaced to the
user, not silently absorbed.

## web-access skill

`web-access` is a thin exploratory-browsing skill over the native `browser` tool
plus `web_search` / `web_fetch` — Node-free, no proxy. Its value is the browsing
methodology (the see→decide→act loop, tool-selection guidance, login judgment,
per-site memory), not an execution layer; the `browser` tool's primitives
supersede the old proxy.

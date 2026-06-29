# Browser Computer-Use & Skill Recording

octo's first-class capability for **seeing and operating a browser**, and for
**recording a human demonstration into a replayable skill** — the foundation
for replacing brittle, complex RPA.

This is a core pillar, so it is octo-owned and in-binary: a pure-Go CDP client
drives Chrome directly (CDP is websocket + JSON, no CGO, cross-platform). The
only native dependency is the user's own Chrome. The existing `web-access` skill
is not the foundation — it is demoted to (a) a reference for logged-in-Chrome
discovery and (b) an exploratory-browsing skill that later rebases onto this
backend.

## Goals / Non-goals

**Goals**
- A backend-agnostic **action vocabulary** the agent and the replay engine both
  speak (browser backend now; desktop backend later via the same interface).
- A Go-native browser backend with no extra runtime (no Node), reusing the
  user's real logged-in Chrome.
- **Record** a human demonstration and compile it into an inspectable,
  editable, version-controllable skill.
- **Replay** deterministically without an LLM, self-checking each step, and
  hand back to an LLM-driven loop only when a step fails.
- Robust target resolution across standard web apps, anti-scraping sites, and
  Canvas-rendered apps.

**Non-goals (this round)**
- Desktop (native app) automation — designed for, not built here.
- Guaranteed evasion of anti-bot detection — best-effort, see Reliability.

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│  Agent loop / Replay engine                                   │
│  (both consume the Action Vocabulary; never touch CDP)        │
└───────────────┬───────────────────────────┬──────────────────┘
                │  Action Vocabulary (interface)                 │
        ┌───────┴────────┐                ┌──┴──────────────┐
        │ Browser backend │                │ Desktop backend │  (future:
        │ (Go-native CDP) │                │ (MCP / sidecar) │   MCP/sidecar)
        └───────┬────────┘                └─────────────────┘
                │ CDP (websocket+JSON)
          ┌─────┴─────┐
          │  Chrome   │  (user's real, logged-in instance)
          └───────────┘
```

The agent and the replay engine never branch on backend or protocol. A backend
implements the action vocabulary; the same recorded skill replays against any
backend that resolves its targets.

## Action vocabulary (backend-agnostic)

The contract both live computer-use and replay speak. Deliberately small:

| Action | Notes |
|---|---|
| `navigate(url)` / `back()` | browser-specific; desktop backend may no-op |
| `click(target)` | resolve `target` (see Target Resolution), then trusted click |
| `type(text)` | type into the focused element; no target needed |
| `key(combo)` | physical key / combo (`cmd+a`, `enter`, `escape`) |
| `scroll(target?, dx, dy)` | |
| `screenshot()` | full or region; the visual primitive |
| `set_files(target, paths)` | file inputs |
| `eval(js) -> value` | DOM read / verify primitive (browser backend only) |
| `ax_tree() -> nodes` | accessibility tree snapshot (verify + target resolution) |

A **`target`** is never a single CSS string — it is a multi-representation
descriptor (see below) resolved at action time.

## Browser backend: Go-native CDP

A hand-rolled minimal CDP client, consistent with octo's convention of
hand-writing wire adapters (the anthropic/openai providers are hand-rolled, not
SDKs). `web-access`'s `cdp-proxy.mjs` is ~25KB of logic; the Go equivalent is
small and keeps the dependency tree minimal (over pulling chromedp/go-rod).

CDP domains required:
- `Target.*` — attach to targets (`attachToTarget {flatten:true}`), create/close.
- `Page.*` — navigate, lifecycle, `captureScreenshot`.
- `Runtime.*` — `evaluate` (DOM read / eval-verify), `addBinding` /
  `bindingCalled` (recorder event channel).
- `DOM.*` — `querySelector`, box model, `setFileInputFiles`.
- `Input.*` — `dispatchMouseEvent` / `dispatchKeyEvent` (trusted input: counts
  as a real user gesture, triggers file dialogs, helps against anti-automation).
- `Accessibility.getFullAXTree` / `getPartialAXTree` — the AX layer.

**Logged-in Chrome takeover.** Reuse the user's real Chrome profile (not a fresh
headed/headless instance) so login state, cookies, and fingerprint are real.
Discover or launch the debugging endpoint by porting `web-access`'s
browser-discovery logic (browser detection, `--remote-debugging-port`, profile
handling) — that discovery work is `web-access`'s genuine accumulated value and
is borrowed rather than rebuilt from scratch.

## Target resolution — tiered hybrid (core)

A single selector strategy is insufficient: Canvas apps (e.g. 企业微信文档) have
no usable DOM, and anti-scraping sites (e.g. 小红书) randomize/obfuscate class
names. Targets are therefore **captured as multiple representations at record
time** and **resolved by descending reliability at replay/action time**.

| Tier | Mechanism | Wins for |
|---|---|---|
| **0** | Focus + keyboard (`type`/shortcuts; no element locate) | text entry, Canvas doc editing |
| **1** | Semantic selector: ARIA role+accessible-name, text content, stable `data-*`, structural path. **Never raw CSS classes** | standard web apps |
| **2** | AX-tree node (`Accessibility.getFullAXTree`) | hostile-CSS sites / Canvas apps that expose a11y |
| **3** | **Visual anchor**: a screenshot crop around the target captured at record time; at replay relocate via template match or VLM ("click the thing that looks like this / says X"), then `Input` trusted click at the resolved coordinates | **Canvas, heavy anti-scrape — when DOM is useless** |

Visual (Tier 3) is a **first-class fallback, not a backup plan** — it is exactly
how commercial RPA handles Canvas (image match + OCR + coordinates). Embracing it
is on-thesis for "replace complex RPA."

**Verify conditions** mirror the tiers: `text_content`, `ax_element`, and
`visual` strategies. On Canvas, only `visual` is available.

## Recorder

Validated approach (co-attach proof-of-concept passed): a recorder CDP client
attaches to the **same Chrome** that the driver/proxy is using — two clients,
two `sessionId`s, concurrent, no conflict. Capture works via an injected DOM
listener that reports through `Runtime.addBinding` → `Runtime.bindingCalled`.

Per captured action, the recorder records **all representations at once** so
replay has fallbacks regardless of site type:
- semantic selector candidates (role/name/text/`data-*`/structural path)
- AX node (role, name, path)
- coordinate (viewport + page)
- screenshot crop of the target region
- action kind + payload (text typed, key combo, scroll delta)
- the active URL / frame

Injection covers future navigations (`Page.addScriptToEvaluateOnNewDocument`)
and the live document (`Runtime.evaluate`). Keyboard input is captured for
`type`/`key` so text entry replays faithfully even on Canvas.

## Skill artifact format

Extend octo's existing CC-compatible skill format (SKILL.md + YAML frontmatter)
rather than the mruby workflow DSL — the DSL is for code orchestration, not GUI
steps, and a SKILL.md is human-readable, hand-editable, and git-versionable.

```markdown
---
name: <kebab-case>
description: <one line>
metadata:
  browser_skill:
    source: recorded
    inputs: [ { name: query, default: "" } ]
---

## Steps

### 1. <title>
action: click
target:
  selector: role=button[name="搜索"]
  ax: { role: button, name: "搜索" }
  coord: { x: 412, y: 88 }
  visual: crops/step1.png
verify:
  strategy: visual            # text_content | ax_element | visual
  ref: crops/step1-after.png
```

`{{param}}` placeholders parameterize typed text and URLs. Crops live beside the
SKILL.md. Stored separately from octo's own skills (own directory) since the
on-disk shape differs.

## Replay + verify + self-heal

```
for each step:
    resolve target by descending tier (0→3), using the captured representations
    execute action via the backend (deterministic, no LLM)
    evaluate verify condition
    if verify fails:
        hand control to the live computer-use loop (LLM) starting from this step
        the LLM observes (AX digest or screenshot), completes the step, and may
        repair the recorded step (update selector / re-crop) before resuming
```

Deterministic in the common case (no LLM, no token cost); self-healing only when
a step actually fails. This self-heal is the difference between this and a
brittle RPA macro.

## Live computer-use loop

When there is no recorded skill (or during self-heal), the agent drives the
browser directly. octo is multi-provider, so **the loop's model dependence is
itself tiered** — the common path runs on any tool-use model; only the pure-
visual corner needs a specially-trained model.

There are two distinct kinds of "computer use", with very different model
requirements:

1. **Native computer-use tool (model-specific).** Claude, OpenAI
   (computer-use-preview / Operator), and Gemini each ship a tool type whose
   model is *trained* to ground a target in a screenshot to precise coordinates.
   This is protocol-specific (Anthropic tool type + beta header, OpenAI
   Responses `computer_use`, Gemini's own) and lives in the provider adapter.
   Only those specific models have it.
2. **Generic vision + custom action tool (any multimodal model).** Define our
   own `click(x,y)`/`type(text)` tools and feed a screenshot to any tool-use-
   capable multimodal model. The catch: general VLMs are **weak at raw
   coordinate grounding** — they describe what they see but mis-place `(x,y)`.

The technique that makes generic models viable is to **turn grounding into
selection**: never ask for raw coordinates.
- **AX/DOM-digest selection** — give the model an enumerated list of interactive
  elements (role + name + text); it picks by reference. No vision grounding at
  all when DOM/AX exists.
- **Set-of-Marks** — overlay numbered boxes on detectable elements, screenshot
  it, ask "click 7"; map the index back to a real element/coordinate. Works on
  weaker models — *provided elements can be enumerated to mark them*.

Mapping to the resolution tiers:

| Path | Model requirement |
|---|---|
| **DOM/AX available** (Tier 0–2) | model only *selects* among enumerated targets → **any tool-use model; vision optional**. Cheap, broadly compatible. This is the same ordinary tool-use octo already supports — no provider-protocol work. |
| **Pure-visual** (Tier 3: Canvas / hostile, no DOM) | needs real visual grounding. **Native computer-use model is best.** A generic model can be rescued by Set-of-Marks *only if elements are detectable* — on true Canvas there are no DOM boxes to mark, so a generic model falls back to raw-coordinate prompting (a known **quality cliff**) unless an OCR/detection layer proposes regions to mark. |

Design stance:
- **Default to a model-agnostic, selection-based loop** (AX/DOM digest + SoM) —
  maximizes provider compatibility and is the lowest-integration path.
- **Treat the visual tier as a capability check**: if the bound model advertises
  native computer-use, use it to upgrade Tier 3 grounding; otherwise use SoM;
  true Canvas + a weak model is the acknowledged weak corner (optionally close
  it with an OCR/detection step).
- **Native computer-use is an enhancement, never a hard requirement.** It is
  done per-provider in the adapter; present → stronger, absent → not fatal.

Self-heal inherits this: a recorded skill that drops into the live loop on a
Canvas page is only as reliable as the bound model's visual grounding.

## Permissions & safety

Browser actions (click, type, navigate, file upload) are high-impact and route
through octo's existing permission gate. Operating logged-in sessions can take
real actions on the user's accounts; gating and clear action summaries are
mandatory. Recorded screenshots may capture sensitive on-screen content — they
are stored locally with the skill and only sent to a model during generation or
self-heal.

## Reliability & anti-detection (honest limits)

Anti-bot detection (CDP/headless fingerprinting) is orthogonal to target
resolution and is an **arms race with no guarantee**. Mitigations: trusted
`Input` gestures, the user's real logged-in profile (not headless), human-like
timing. Some sites still detect and may **ban accounts** — surfaced to the user,
not silently absorbed. Pixel-based interaction does not help with detection;
only with locating targets once you can drive the page.

## web-access evolution

`web-access` is not built upon. It stays a pristine, upstream-tracked vendored
skill for now, contributing only its browser-discovery logic (borrowed). Once
the owned backend is first-class, `web-access` rebases onto it — becoming a thin
exploratory-browsing skill over octo's native browser-use rather than its own
Node proxy.

## Implementation slices

Spike the real product risk first; the technical co-attach unknown is already
cleared.

1. **Owned CDP backend + action vocabulary.** Hand-rolled Go CDP client; attach
   to a logged-in Chrome; implement navigate/click/type/key/scroll/screenshot/
   eval/ax_tree. Acceptance: drive a trivial page end-to-end from Go, no Node.
2. **Recorder + trivial record→replay.** Co-attach capture; multi-representation
   per action; compile to SKILL.md; deterministic replay of a Tier-1 flow.
   Acceptance: record and replay a standard web-app task.
3. **The real risk — robust resolution + self-heal.** Tiers 2–3 (AX + visual
   anchor), visual verify, and the self-heal handoff. Acceptance: a recorded
   skill survives class-name churn (small DOM change) and runs on a Canvas page
   (企业微信文档) via the visual tier.
4. **Live computer-use loop.** DOM/AX-first with screenshot+VLM fallback;
   provider-agnostic, Claude-native computer-use as enhancement.
5. **Desktop backend (future).** Same action vocabulary behind an MCP/sidecar
   backend; no core changes.

## Open questions

- Visual matching engine: in-Go template match vs always-VLM vs OCR-assisted —
  decide during slice 3 against real Canvas pages.
- AX-tree coverage of target Canvas apps (企业微信文档): confirm whether it
  exposes a usable a11y mirror; if not, Tier 3 is the only path there.
- Visual-grounding on the weak corner: for true Canvas + a model without native
  computer-use, decide whether to add an OCR/element-detection step to enable
  Set-of-Marks, or to require a native computer-use model for those tasks and
  degrade explicitly otherwise.
- Native computer-use integration: which providers' computer-use tools to wire
  into the adapter first (Claude / OpenAI / Gemini), and the capability-
  advertisement mechanism the live loop checks.
- Skill storage/sharing: whether recorded browser skills flow through octo's
  existing skill import/export.

# Desktop Computer-Use (generalizing record/replay to native apps)

A forward design for extending octo's record→replay→self-heal pillar from the
browser to native desktop applications. It is a **proposal and feasibility
analysis**, not a built feature; the browser pillar
(`browser-computer-use-design.md`) is the shipped reality this builds on.

## Thesis: the paradigm generalizes, the substrate does not

octo's record/replay has two separable layers:

- **The paradigm (portable).** Record a demonstration → distill at stop into an
  editable, semantic skill → replay deterministically with per-step verify →
  hand to an LLM only to repair a failed step. This loop, the skill-as-editable
  artifact, text/semantic anchoring, conservative distillation, and the
  self-heal-on-failure pattern are all substrate-independent. They live in the
  `Step` / `ReplayRecording` / `Healer` shapes in `internal/browser/skill.go` and do
  not assume a browser.

- **The substrate (not portable).** Capture and replay today are bound to
  **CDP and the DOM**: the recorder injects DOM event listeners; replay resolves
  targets with `querySelector` + recorded text; self-heal reads a DOM
  interactable digest. Desktop applications have no DOM. Generalizing means
  building a new capture/replay substrate — this is the bulk of the work and it
  is platform-specific.

The honest framing: **desktop is a new Computer-Use pillar, a sibling of the
browser backend, not an extension of it.** It is the same frontier OpenAI's
Codex "Record & Replay" occupies — which is why that feature shipped macOS-first:
the hard part is the OS accessibility + input + capture layer, per platform.

## What transfers vs what is new

| Concern | Browser (shipped) | Desktop (new) |
|---|---|---|
| Record→distill→edit→replay→heal loop | yes | **reuse** |
| Skill as editable artifact | YAML | reuse (target shape changes) |
| Semantic/anchored targeting | DOM selector + text | OS accessibility role + label/value |
| Capture | injected DOM listeners + `Page.frameNavigated` | **OS accessibility events / polling** |
| Target resolution | `querySelector` + text anchor | **AX element by role+label path** |
| Input | CDP `Input.dispatchMouseEvent` | **OS input synthesis** (CGEvent / SendInput) |
| Vision fallback | not used (DOM suffices) | **screenshot + VLM** for apps with poor AX |
| Self-heal digest | DOM interactable list | AX subtree digest |
| Permissions | chrome://inspect toggle | **OS screen-recording + accessibility grants** |

The DOM analog on desktop is the **OS accessibility tree**:

- **macOS** — Accessibility API (`AXUIElement`): a tree of elements with roles,
  titles, values, and actions. The richest analog to the DOM and the natural
  first target.
- **Windows** — UI Automation (UIA): comparable element tree with control types
  and patterns.
- **Linux** — AT-SPI: the GNOME/Qt accessibility bus; coverage is more uneven.

Where AX is rich, replay stays semantic and precise (like the browser path).
Where an app exposes little AX (custom-drawn / Canvas-like native UIs), the only
recourse is **vision** — screenshot + a coordinate-grounding model — which is the
weak, slow corner, the same one browser replay deliberately avoids by using the
DOM.

## Proposed shape

A `internal/desktop` backend, sibling to `internal/browser`, implementing the
same capture→`Step`→replay contract. To share `ReplayRecording`, the engine's notion
of a target must abstract above CSS:

- Generalize `Step.Selector` (a CSS string) into a **target descriptor** that
  each backend resolves its own way — browser: CSS + text; desktop: AX
  role + label/value + structural path (+ optional bounding-box for vision
  fallback). The recorded `label` (visible text) stays the cross-backend anchor.
- Keep the action vocabulary small and shared: `click`, `type`, `key`, `scroll`,
  `wait`, `navigate`(app-launch / window-focus on desktop), `verify`. The replay
  loop, verify, and heal handoff are reused unchanged.
- A desktop self-heal digest = an AX subtree of the focused window (role + label
  list), mirroring the browser's interactable digest, so the same
  text-digest-based healer works with no vision.

The native AX/input/capture layer is **platform-specific and needs CGO or a
helper process** — it cannot be pure-Go like the CDP client. This is a real
departure from the browser backend's no-native-deps property and is the main
cost.

## Platform priority

1. **macOS first** (AXUIElement + CGEvent + ScreenCaptureKit). Richest AX, the
   common dev/work machine, and where the comparable products start.
2. **Windows** (UIA + SendInput) second — large surface, more enterprise apps.
3. **Linux** (AT-SPI) last — uneven AX coverage; likely vision-leaning.

## Phasing

1. **Abstract the target.** Generalize `Step`'s target so `ReplayRecording`,
   `verify`, and the heal handoff are backend-neutral; keep the browser backend
   green against it. No desktop code yet — this de-risks the shared engine.
2. **macOS AX capture + replay (happy path).** Record clicks/typing/navigation
   via the Accessibility API; resolve AX elements by role+label; synthesize input
   with CGEvent. Acceptance: record and replay a multi-step task in a native
   macOS app whose AX is rich.
3. **Self-heal + drift.** AX-subtree digest healer; survive minor UI change
   (relabeled/moved control) the way text-anchored browser replay does.
4. **Vision fallback.** Screenshot + coordinate grounding for AX-poor apps, gated
   on a vision/computer-use-capable model — explicitly the weak corner, degrade
   loudly rather than silently mis-click.
5. **Windows / Linux** behind the same contract.

## Honest risks & open questions

- **Distillation of fumbles is harder, not easier, on desktop.** The browser
  path cleans noise with DOM + text precision; AX labels are messier and vision
  is imprecise, so mis-clicks captured during recording are harder to drop
  cleanly. This is the exact pain reviewers call out in Codex's version, and it
  is where the browser pillar's precision advantage does *not* carry over.
- **AX coverage is the make-or-break.** Many native apps (and Electron apps —
  which are actually web, already covered by the browser backend) expose thin or
  wrong AX. Survey real target apps before committing; an app with no usable AX
  is a vision-only task with all the attendant quality cliffs.
- **Native dependency.** CGO / a per-platform helper breaks the
  no-runtime-deps property the browser backend enjoys; weigh against the value.
- **Permissions friction.** macOS screen-recording + accessibility grants are
  per-app, persistent prompts — an onboarding cost.
- **ROI vs. web.** Much "desktop" work is already web (internal tools, Electron),
  which the browser backend covers. The highest-leverage path remains making
  browser record/replay excellent; desktop is justified only by concrete native
  workflows that are neither web nor CLI-addressable.

## Recommendation

Treat desktop as a deliberately deferred, separate pillar. Land the
target-descriptor abstraction (phase 1) only when there is a committed desktop
use case, since it is the one piece that touches the shared engine; build the
macOS AX backend behind it. Until then, the browser pillar is the differentiated,
precise path, and CLI/API remain the faster route for anything that does not
genuinely require driving a GUI.

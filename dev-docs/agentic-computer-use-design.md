# Agentic Computer-Use (live desktop operation for the model)

Status: validated spike (branch `wt/computer-use`, commits `8b94e58e`,
`43e21392`, `d1d7b867`), design settled, not yet merged.

A design for giving the model live control of the macOS desktop: see the
screen, decide, act — click, type, scroll — inside native apps that have no
CLI and no API (CAD, game engines, legacy GUIs). This is the
screenshot→decide→act loop, **not** record→replay→self-heal.

Relationship to the other two computer-use docs:

- `browser-computer-use-design.md` — the shipped browser pillar. Unchanged;
  anything web stays with it.
- `desktop-computer-use-design.md` (issue #1871) — desktop **record/replay**.
  That proposal's own recommendation was "build only with a committed use
  case". The use case that materialised is the agentic loop in this doc,
  which is a sibling, not an extension. The AX substrate built here is
  exactly what #1871's later phases needed (semantic targeting, AX-digest
  self-heal), so if desktop record/replay is ever built, it starts from this
  package.

## Thesis: two channels, AX preferred, pixels as fallback

Desktop operation has two viable channels on macOS, and every claim below is
**measured on macOS 15 (2026-09-11), not assumed**:

| Channel | Mechanism | Background-safe? | Precision | Coverage |
|---|---|---|---|---|
| **AX (accessibility)** | Read the app's AX tree (`AXUIElement`), act with `AXPress` / `AXSetValue` | **Yes** — served by the target app's own AX server: no cursor, no focus change | Semantic (role + label); immune to window moves, display changes, perspective | AX-rich apps only; custom-drawn / some SwiftUI UIs expose little or nothing |
| **Pixels** | `screencapture` screenshot → vision model → global `CGEvent` injection (`kCGHIDEventTap`) | **No** — shares the user's cursor and focus | Coordinate-estimate quality; degrades with perspective/scale | Universal — anything on screen |

The architecture is AX-first with automatic pixel fallback: the model calls
`ax_tree` on the target app; if the tree exposes the needed elements, all
operations run in the background; otherwise it drops to screenshot + pixel
coordinates in the foreground. This mirrors what OpenAI shipped in Codex's
computer use (AX-tree-centric, built by the ex-Workflow/Shortcuts team they
acquired with Sky Applications), which independently validates the choice.

## Measured evidence (macOS 15, 2026-09-11)

Every row was reproduced with the spike's probe binary:

| Experiment | Result |
|---|---|
| `screencapture -l <windowID>` on an **occluded** window | ✅ Captures full window contents (Chess behind Finder, Calculator behind Chess) |
| `CGEventPostToPid` **mouse** events, with/without `kCGMouseEventWindowUnderMousePointer{,ThatCanHandleThisEvent}` (fields 91/92) | ❌ Dropped by both Calculator (SwiftUI) and Chess (custom Metal/SceneKit board), foreground or background |
| `CGEventPostToPid` **keyboard** events | ✅ Reach a backgrounded TextEdit, but bursts get coalesced by the throttled background runloop — **~30 ms per-character pacing required** |
| AppleScript/System Events AX query on new Calculator | ❌ Only window chrome (3 unnamed buttons); the keypad is not exposed. `AXEnhancedUserInterface` settable but no effect; `AXManualAccessibility` not settable |
| **Chess AX tree** | ✅ **Every board square is an `AXButton` with a semantic label** (`"白兵, e2"`, `"e4"`) — full semantic grounding on a 3D custom-rendered board |
| `AXPress` on Chess squares with Chess **backgrounded** | ✅ 16 consecutive moves played while other apps were frontmost; unaffected by the window being moved across displays |
| `AXPress` on menu-bar items | ⚠️ Only takes effect with the app **frontmost** (verified with Chess → 设置…) |
| `AXSetValue` on the Chess difficulty slider | ✅ Numeric set works (difficulty = thinking seconds; 1 = fastest/weakest; setting 0 rejected — lower bound is 1) |
| Label matching `contains="设置"` | ⚠️ Wrongly hits the Apple menu's `系统设置…` before the app menu's `设置…` — matching must be **exact-label first, substring second** |

Two design-level lessons from play-testing:

1. **Every action must be re-verified.** Chess silently ignores illegal moves
   while `AXPress` still reports success — an agent that trusts its own move
   list instead of re-reading the board corrupts its state. The tool
   description mandates act → `ax_tree`/screenshot → verify, never blind
   chaining.
2. **Semantic beats pixels even when both exist.** The Chess board's 3D
   perspective makes square-centre math fragile; the AX labels are exact.
   Pixels remain the fallback for apps with no tree.

## Tool surface (`computer`)

One tool, registered in `internal/tools/registry.go` alongside `BrowserTool`.
Actions:

| Action | Channel | Notes |
|---|---|---|
| `ax_tree` | AX | Indented digest of windows + menu bar: role, label, frame. Depth-capped (default 12), fan-out ≤ 200/node, total ≤ 2000 elements — a browser/Electron tree must not turn one call into a minute of mach IPC |
| `ax_press` | AX | Press first element matching `role` + `label` (exact label beats substring; `role` optional but recommended) |
| `ax_set` | AX | Set value: number for `AXSlider`/`AXStepper`, string for text fields |
| `screenshot` | Pixel | Full main display via `screencapture -x`; returned as a vision image block (gated on `ImagesAllowed`, `internal/tools/vision.go:79`) |
| `left_click` / `right_click` / `double_click` / `mouse_move` | Pixel | Global `CGEvent` at logical-point coordinates |
| `type` / `key` | Pixel | Unicode typing; combos like `cmd+shift+s` (`parseCombo`) |
| `scroll` | Pixel | Line-unit scroll wheel |

Coordinate contract: the model works in the pixel space of the screenshot **as
actually sent to the provider**. `agent.NewImageBlock` may downscale to
`imageCompressMaxEdge = 1568` (`internal/agent/image_compress.go:28`), so the
tool decodes the final block bytes, reports their dimensions in the result
text, and maps model coordinates back to logical points via
`ScreenSize()/sentWidth`. Retina and edge-capped screenshots stay accurate.

Model requirements: the AX channel works with **any** model (text in, text
out). The pixel channel needs a vision-capable model or a configured
`vision_helper`; `screenshot` degrades to a saved-path result otherwise.

## Package layout (as built in the spike)

- `internal/computer/` — substrate, platform-split:
  - `computer.go` — shared API (`Screenshot`, `Click`, `TypeText`, `Press`,
    `FindWindow`, `ClickPid`, `TypeTextPid`, `AXTree`, `AXPress`,
    `AXSetValue`, permission preflights) + `ErrUnsupported` +
    platform-neutral logic (`parseCombo`, `MatchAX`/`MatchAXExact`).
  - `helpers.h` — all C code as `static inline`, shared by both CGO
    translation units (cgo preambles don't share declarations).
  - `darwin_cgo.go` (`//go:build darwin && cgo`) — CGEvent input, CGWindowList
    window lookup, `screencapture` CLI capture (the `CGWindowListCreateImage`
    API is `unavailable` in the macOS 15 SDK; ScreenCaptureKit is ObjC/async
    and buys nothing the CLI doesn't for a single full-screen frame).
  - `ax_darwin.go` (`//go:build darwin && cgo`) — AX traversal, matching,
    actions.
  - `stub.go` (`//go:build !darwin || !cgo`) — every entry point returns
    `ErrUnsupported`, so **all existing builds compile unchanged**, including
    `CGO_ENABLED=0`.
- `internal/tools/computer.go` — the tool: action dispatch, permission
  preflight with actionable errors, coordinate mapping, AX digest rendering.

## Permissions and onboarding

Two macOS grants, both preflighted before use with the system prompt dialog
triggered on miss, and errors that say exactly what to grant:

- **Accessibility** (`AXIsProcessTrusted` / `AXIsProcessTrustedWithOptions`)
  — required for all input synthesis and all AX actions.
- **Screen Recording** (`CGPreflightScreenCaptureAccess` /
  `CGRequestScreenCaptureAccess`) — required for screenshots.

On macOS the grant is attributed to the responsible application (the desktop
app, or the terminal hosting the CLI), which the error messages name.

## Security model (settled)

Reuse octo's existing tool-permission system — the `computer` tool is gated
by the same permission mode as every other tool call; there is **no** second
per-app allowlist layer. Rationale: the permission prompt already surfaces
the action; duplicating it per-app adds friction without a new control point.
(Codex's per-app "Always allow" list and hard restrictions — no terminal
automation, no self-control, no approving system prompts — remain a reference
if real usage shows the generic gate is too coarse.)

The tool description carries the operational safety rules: screenshot/`ax_tree`
before acting, re-verify after every action, never blind-chain.

## Exposure: experimental, opt-in (settled)

The tool ships dark behind a config gate following the existing
`tools.tool_search.enabled` pattern (`internal/config/config.go:193-195`,
validated at `config.go:596`):

```yaml
tools:
  computer:
    enabled: "on"   # "off" (default) | "on"
```

`ComputerTool` is added to `allTools` in `internal/tools/registry.go` only
when enabled; when off it is absent from both the registry and the tool list
sent to the model. Graduate the default once the security model has real
mileage.

## Release & build (settled: ships in release binaries)

Constraint: goreleaser cross-compiles every target from `ubuntu-latest` with
`CGO_ENABLED=0` (`.goreleaser.yaml:29`), which would silently ship the stub
on macOS. Since the feature must ride releases:

- The **darwin CLI legs move out of goreleaser** into a `macos-latest`
  GitHub Actions job building `darwin/arm64` and `darwin/amd64` with
  `CGO_ENABLED=1`. clang on the runner targets both architectures (`-arch`),
  so a single arm64 runner produces both; the existing `darwin_all` lipo
  artifact (`.goreleaser.yaml:64-67`) is recreated from those two binaries so
  `install.sh` and the pkg installer keep working unchanged.
- `linux/*` and `windows/*` stay in goreleaser with `CGO_ENABLED=0` (stub).
- The macOS desktop app is unaffected: it already builds with `CGO_ENABLED=1`
  on `macos-latest` (`Makefile:104`).
- Local dev is unaffected: `make build` on macOS has CGO on by default.

## Out of scope

- **Windows / Linux** — no substrate. AutoCAD-class targets are mostly
  Windows; that is a future port behind the same `internal/computer` API
  (UIA + SendInput), not this design.
- **Private SkyLight/CGS event injection** (the suspected mechanism behind
  Codex's background mouse) — rejected: private API surface, maintenance and
  review burden, and the AX channel already covers the background need for
  AX-rich apps.
- **Locked-screen operation** — Codex's authorization-plug-in "locked use" is
  noted as prior art only; screen locked = no operation, like every other
  implementation except theirs.
- **Desktop record/replay** — remains #1871's scope; this package is its
  prerequisite substrate, not the feature.
- **Multi-display targeting** — screenshot covers the main display; AX frames
  are global and already work across displays. Pixel actions on non-main
  displays need display selection, deferred until a real case shows up.

## Known limitations (from measurement, not speculation)

- Menu-bar `AXPress` only fires with the target app frontmost.
- `FindWindow` requires one on-screen, non-minimized window (menu-bar-only
  apps are unreachable in the spike).
- Pixel-channel state (`shotScale`) is process-global — one display, one
  conversation driving the screen at a time.
- SwiftUI apps may expose only window chrome over AX (new Calculator); the
  pixel channel is the only route there.

## Test plan

- Unit (already in spike): `parseCombo` table test, `MatchAX`/`MatchAXExact`
  table test, `Click` validation, stub compile under `CGO_ENABLED=0` (CI's
  existing `go test ./...` covers this on all three platforms).
- Manual UAT before merging the gate flip:
  1. Pixel: drive Calculator in the foreground to compute 6×7=42 (done in
     spike, first-try).
  2. AX background: play ≥4 Chess moves with Chess occluded (done in spike,
     16 moves).
  3. AX foreground: open Chess 设置… via menu bar, set difficulty slider to
     minimum via `AXSetValue` (done in spike).
  4. Permission-less run: revoke grants, confirm both preflight errors are
     actionable and the system dialogs appear.

## Rollback

The config gate defaults off; flipping it off dark-launches the feature with
no code revert. Non-CGO and non-darwin builds contain only the stub — no
release artifact regresses by construction.

# Agentic Computer-Use (live desktop operation for the model)

A design for giving the model live control of the desktop — macOS and
Windows: see the screen, decide, act — click, type, scroll — inside native
apps that have no CLI and no API (CAD, game engines, legacy GUIs). This is the
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

Desktop operation has two viable channels. The macOS rows below are
**measured on macOS 15 (2026-09-11), not assumed**; Windows maps onto the
same two channels (UI Automation and SendInput, see the Windows section):

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

Every row was reproduced with a standalone probe binary against the real apps:

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
| `screenshot` | Pixel | Main display only: `screencapture -x -D 1` on macOS (display 1 is the main display), GDI `BitBlt` + `GetDIBits` on Windows; returned as a vision image block (gated on `tools.ImagesAllowed`) |
| `left_click` / `right_click` / `double_click` / `mouse_move` | Pixel | Global `CGEvent` at logical-point coordinates |
| `type` / `key` | Pixel | Unicode typing; combos like `cmd+shift+s`. `parseCombo` yields a platform-neutral key name and requires exactly one non-modifier key (keycode 0 is `a` on macOS, so a modifier-only combo would otherwise post ⌘A); each platform maps the name to its keycode. `cmd` is Command on macOS and the Windows key on Windows; `delete` is forward delete on both, `backspace` erases backwards |
| `scroll` | Pixel | Line-unit scroll wheel |

Coordinate contract: the model works in the pixel space of the screenshot **as
actually sent to the provider**. `agent.NewImageBlock` may downscale to
`imageCompressMaxEdge` (1568 px on the long edge), so the
tool decodes the final block bytes, reports their dimensions in the result
text, and maps model coordinates back to logical points via
`ScreenSize()/sentWidth`. Retina and edge-capped screenshots stay accurate.

Model requirements: the AX channel works with **any** model (text in, text
out). The pixel channel needs a vision-capable model or a configured
`vision_helper`; `screenshot` degrades to a saved-path result otherwise.

## Package layout

- `internal/computer/` — substrate, platform-split:
  - `computer.go` — shared API (`Screenshot`, `Click`, `TypeText`, `Press`,
    `FindWindow`, `ClickPid`, `TypeTextPid`, `AXTree`, `AXPress`,
    `AXSetValue`, permission preflights) + `ErrUnsupported` +
    platform-neutral logic (`parseCombo`, `MatchAX`/`MatchAXExact`, which
    treat `AXButton` and UIA's `Button` as the same role).
  - `helpers.h` — all C code as `static inline`, shared by both CGO
    translation units (cgo preambles don't share declarations).
  - `darwin_cgo.go` (`//go:build darwin && cgo`) — CGEvent input, CGWindowList
    window lookup, `screencapture` CLI capture (the `CGWindowListCreateImage`
    API is `unavailable` in the macOS 15 SDK; ScreenCaptureKit is ObjC/async
    and buys nothing the CLI doesn't for a single full-screen frame).
  - `ax_darwin.go` (`//go:build darwin && cgo`) — AX traversal, matching,
    actions. Matching runs two passes (exact label, then substring), each
    with its own traversal budget so the fallback still runs on large trees.
  - `windows.go` (`//go:build windows`) — pixel channel over Win32 through
    `golang.org/x/sys/windows` lazy procs: `SendInput` mouse/keyboard/wheel,
    `SetCursorPos`, GDI capture, `EnumWindows` app lookup, and a one-time
    `SetProcessDpiAwarenessContext(PER_MONITOR_AWARE_V2)` so every API speaks
    physical pixels. No cgo.
  - `uia_windows.go` (`//go:build windows`) — UI Automation over raw COM
    vtables (`CoCreateInstance(CUIAutomation)`, `ElementFromHandle`,
    `ControlViewWalker`, Invoke / Toggle / Value / LegacyIAccessible
    patterns). GUIDs, ids and slot numbers are taken from
    `uiautomationclient.h`; the calls are pinned to one OS thread with COM
    initialised per call.
  - `stub.go` (`//go:build (!darwin && !windows) || (darwin && !cgo)`) —
    every entry point returns `ErrUnsupported`, so **all other builds compile
    unchanged**, including a macOS build with `CGO_ENABLED=0`.
- `internal/tools/computer.go` — the tool: action dispatch, permission
  preflight with actionable errors, coordinate mapping, AX digest rendering.

## Permissions and onboarding

macOS needs two grants, both preflighted before use with the system prompt
dialog triggered on miss, and errors that say exactly what to grant:

- **Accessibility** (`AXIsProcessTrusted` / `AXIsProcessTrustedWithOptions`)
  — required for all input synthesis and all AX actions.
- **Screen Recording** (`CGPreflightScreenCaptureAccess` /
  `CGRequestScreenCaptureAccess`) — required for screenshots.

On macOS the grant is attributed to the responsible application (the desktop
app, or the terminal hosting the CLI), which the error messages name.

Windows has no equivalent grant: `Trusted` and `ScreenCaptureAllowed` report
true and the request calls are no-ops. What replaces the grant is User
Interface Privilege Isolation — a process cannot inject input into a window
of a higher integrity level, so an app running as administrator ignores a
non-elevated octo. `SendInput` reports the shortfall and the tool error says
so; the Settings hint under the toggle carries the same warning.

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
`tools.tool_search.enabled` pattern (`config.ToolSearchConfig`, validated in
`Config.Validate`):

```yaml
tools:
  computer:
    enabled: "on"   # "off" (default) | "on"
```

Surfaced to the user as a toggle in **Settings → Experimental**, visible only
on the **macOS or Windows desktop app** (the substrate's platforms; on macOS
the desktop shell is also the only one that can hold the Screen Recording /
Accessibility grants). The tab is gated on `/api/version`'s `native` flag
plus its `os` field (`runtime.GOOS`); the write goes through
`PUT /api/config/computer`, which additionally refuses servers on any other
OS so a remote Linux peer can never persist a no-op switch. `GET /api/config`
reports the raw value as `computer_enabled`.

`ComputerTool` stays in `allTools` (so dispatch works) but is filtered out of
the model's tool list in `defaultToolsFor` when the gate is off — the same
advertising-gate pattern as `BrowserTool`. The gate (`computerEnabled`) is
also false on every OS other than macOS and Windows regardless of the yaml
value: the API already refuses the write there, and a hand-edited config must
not advertise a tool whose every call fails. A darwin build without CGO still
advertises it, because its `ErrUnsupported` names the missing CGO — the
actionable message in that case. Graduate the default once the security
model has real mileage.

## Release & build (settled: ships in release binaries)

Constraint: goreleaser cross-compiles every target from `ubuntu-latest` with
`CGO_ENABLED=0` (its build `env`), which would silently ship the stub on
macOS. Since the feature must ride releases:

- The **darwin CLI legs move out of goreleaser** into a `macos-latest`
  GitHub Actions job building `darwin/arm64` and `darwin/amd64` with
  `CGO_ENABLED=1`. clang on the runner targets both architectures (`-arch`),
  so a single arm64 runner produces both; the existing `darwin_all` lipo
  artifact (the universal-binary entry in `.goreleaser.yaml`) is recreated
  from those two binaries so
  `install.sh` and the pkg installer keep working unchanged.
- `linux/*` and `windows/*` stay in goreleaser with `CGO_ENABLED=0`: Linux
  gets the stub, Windows gets the real substrate because it is pure Go.
- The macOS desktop app is unaffected: it already builds with `CGO_ENABLED=1`
  on `macos-latest` (the desktop target in the `Makefile`).
- Local dev is unaffected: `make build` on macOS has CGO on by default.

## Windows

The same two channels behind the same `internal/computer` API, implemented
without cgo so the existing `CGO_ENABLED=0` release cross-build ships it:

| macOS | Windows | Notes |
|---|---|---|
| `AXUIElement` tree, roles `AXButton`… | UI Automation `IUIAutomationElement` control view, roles `Button`, `MenuItem`, `Edit`… | `MatchAX` strips the `AX` prefix so the model may use either spelling; `ax_tree` prints the platform's own names |
| `AXPress` | `InvokePattern.Invoke`, else `TogglePattern.Toggle`, else `LegacyIAccessiblePattern.DoDefaultAction` | |
| `AXSetValue` (string or number) | `ValuePattern.SetValue(BSTR)`, else `LegacyIAccessiblePattern.SetValue(LPCWSTR)` | `RangeValuePattern.SetValue(double)` is unreachable without cgo — the x64/arm64 ABIs pass the double in a floating-point register `syscall.SyscallN` cannot load. Range-only controls report a clear error; the fallback is to focus them and use arrow keys |
| Label = title / description / value | Label = `Name`; description = `HelpText` | |
| `FindWindow(owner)` by menu-bar process name | `EnumWindows`, first visible non-minimized window whose executable name (without `.exe`) equals `owner`, else whose title contains it | UWP apps are hosted by `ApplicationFrameHost.exe`; the title match is what reaches them |
| CGEvent at logical points | `SetCursorPos` + `SendInput` at physical pixels | The process opts into per-monitor-v2 DPI awareness once, so `GetSystemMetrics`, `GetWindowRect`, the capture and input all agree and `shotScale` stays correct |
| `screencapture -D 1` | GDI `BitBlt(SRCCOPY|CAPTUREBLT)` → top-down 32-bit DIB → PNG | Primary display only, like macOS |
| `CGEventKeyboardSetUnicodeString` | `KEYEVENTF_UNICODE` per UTF-16 unit | Layout-independent typing; single-character `key` presses go through `VkKeyScanW` so the live layout decides |
| `CGEventPostToPid` | none | `ClickPid` / `TypeTextPid` return `ErrUnsupported`; the tool never calls them |

COM discipline: each AX call locks its goroutine to an OS thread,
`CoInitializeEx(MULTITHREADED)`, does the work, `CoUninitialize`. `S_FALSE`
(already initialised) is balanced normally; `RPC_E_CHANGED_MODE` (the thread
already lives in an STA, e.g. the desktop shell's main thread) is used as is.

Verification status: the Windows substrate is compile-checked from macOS
(`GOOS=windows go vet`, amd64 and arm64) and unit-tested on the
`windows-latest` CI runner — struct layouts (`sizeof(INPUT)` = 40),
virtual-key resolution, control-type names, `GetSystemMetrics`,
`EnumWindows` miss handling, and a COM smoke test that creates
`CUIAutomation`, takes `ElementFromHandle(GetDesktopWindow())` and walks its
first children through the vtable slots. **No interactive real-machine UAT
has been run yet**: driving an actual app (Notepad, Calculator) end to end,
UIPI behaviour against an elevated window, and multi-monitor coordinates
remain to be confirmed on hardware before the gate default changes.

## Out of scope

- **Linux** — no substrate; the stub reports `ErrUnsupported`.
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
  apps are unreachable).
- Pixel-channel state (`shotScale`) is process-global — one display, one
  conversation driving the screen at a time.
- SwiftUI apps may expose only window chrome over AX (new Calculator); the
  pixel channel is the only route there.
- Windows: numeric-only controls (`RangeValuePattern` without a string
  `ValuePattern`) cannot be set; elevated apps ignore input from a
  non-elevated octo; the primary display is the only capture/input target.

## Test plan

- Unit: `parseCombo` table test (including modifier-only and two-key
  rejections), `MatchAX`/`MatchAXExact` table test (both role spellings),
  `Click` validation, gate on/off/off-platform, macOS keycode table, Windows
  layout / virtual-key / control-type / COM smoke tests (`windows_test.go`),
  stub compile under `CGO_ENABLED=0` (CI's existing `go test ./...` covers
  this on all three platforms).
- Manual UAT on Windows (not yet run — see the Windows section): open
  Notepad, `ax_tree` it, `ax_set` the `Edit` document text, `key`
  `ctrl+s`; pixel: screenshot, click into the text area, `type`, verify with
  a second screenshot; elevated target: confirm the UIPI error message.
- Manual UAT (all passed on macOS 15; repeat before flipping the default):
  1. Pixel: drive Calculator in the foreground to compute 6×7=42.
  2. AX background: play ≥4 Chess moves with Chess occluded.
  3. AX foreground: open Chess 设置… via menu bar, set difficulty slider to
     minimum via `AXSetValue`.
  4. Permission-less run: revoke grants, confirm both preflight errors are
     actionable and the system dialogs appear.

## Rollback

The config gate defaults off; flipping it off dark-launches the feature with
no code revert. Non-CGO and non-darwin builds contain only the stub — no
release artifact regresses by construction.

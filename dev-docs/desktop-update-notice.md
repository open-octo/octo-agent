# Desktop update flow

The Wails desktop shell (`cmd/octo-desktop`) is distributed as a whole-app
installer (macOS pkg, Windows `octo-setup.exe`, Linux AppImage), not as the
`octo` CLI tarball. It cannot reuse the CLI's in-place swap (`upgrade.Install`
would rename the CLI `octo` binary over the GUI executable), so it updates
through two paths of its own:

- **In-place update** (macOS bundled + Windows release builds): the Wails v3
  updater (`app.Updater`) downloads the desktop release artifact, verifies its
  SHA-256 against the `desktop-checksums.txt` sidecar, swaps the installed app
  via a helper process, and relaunches. See "In-place update" below.
- **Notify-and-open** (Linux AppImage, dev/unbundled builds, or when the
  updater can't be configured): detect a newer release, tell the user, and
  open the download page in the system browser. The user installs the new
  package themselves.

`startUpdateFlow` (cmd/octo-desktop/update.go) routes every update request —
the tray's "Update to vX" item and the update toast — to whichever path the
running build supports (`bridge.inplaceUpdate`).

## Goals

- The desktop shell surfaces "a newer version is available" from three entry
  points: the web version badge, a tray menu item, and the Settings page.
- One click installs the update where the platform allows it; everywhere else
  the click lands on the download page.
- The CLI (`octo serve`) self-upgrade flow is untouched.
- A remote browser connected to the desktop-hosted server is handled correctly
  (it also cannot swap the desktop binary, and its OS may differ).

## Non-goals

- Silent auto-update: installing is always user-initiated (the updater window
  asks before the restart that performs the swap).
- Delta/patch downloads: each update downloads the full artifact (the Wails
  updater has no delta support).

## In-place update

### Release artifacts

The release workflow attaches swappable desktop artifacts alongside the
installers, plus a checksum sidecar (goreleaser's `checksums.txt` covers only
the CLI archives):

| Asset | Produced by | Swap target |
|---|---|---|
| `Octo-darwin-universal.zip` | macos-installer job (`ditto` of the universal, ad-hoc signed `Octo.app`) | the installed `.app` bundle |
| `octo-desktop-windows-{amd64,arm64}.exe` | windows-installer job (the same self-contained exe the installer wraps) | the installed exe |
| `desktop-checksums.txt` | desktop-checksums job (aggregates SHA-256 over all desktop assets after they upload) | — |

Linux has no swappable artifact: the AppImage runs from a read-only squashfs
mount the updater would try to write through, so it stays on notify-and-open.

### Wiring (`cmd/octo-desktop/update.go`)

- `canInplaceUpdate()` gates the whole path: release build only
  (`upgrade.Eligible` — a dev build must not be replaced by an older release),
  and macOS additionally requires running from a bundle.
- `initInplaceUpdater` configures `app.Updater` with a GitHub Releases
  provider. The asset matcher matches **exact names** (`desktopAssetName`),
  never substrings — the release also carries CLI archives whose names contain
  the same platform/arch markers, and a heuristic match would swap the GUI
  with the CLI binary. Any init failure logs and falls back to
  notify-and-open.
- Detection stays on `internal/upgrade`'s releases/latest redirect (no API
  rate limit, mirror fallback); `app.Updater.CheckAndInstall` runs only when
  the user asks to install. Its window walks download → verify → stage and
  prompts for the restart that performs the swap.
- Restart quits the app so the updater's helper (this same binary re-spawned
  with sentinel env vars; intercepted by `updater.HandleHelperMode()` at the
  top of `main`) can wait for the process to exit, swap, and relaunch. A
  listener on `updater.EventUserRestart` flags the bridge so `ShouldQuit`
  allows that specific quit — the keep-running-in-background veto would
  otherwise deadlock the swap. Keyed on the user's restart action, not on
  updater state: a staged-but-deferred update must not quietly disable
  keep-running-in-background.
- `startUpdateFlow` is single-flighted across its entry points (tray, toast,
  web badge), and the download uses a client without a whole-request timeout
  (`updateHTTPClient`) — the provider default caps the entire body read at
  30 s, which truncates a ~100 MB artifact on slow links.

### Trust anchor

Digest-only verification: the sidecar's SHA-256 over GitHub's TLS — the same
anchor as the CLI's `octo upgrade`. Fail-closed: the stock GitHub provider
treats a missing sidecar as "nothing to verify", so `verifiedOnly`
(update.go) wraps it and errors on any release without a digest — an
unverifiable release falls back to notify-and-open rather than installing on
TLS alone. No signing key is configured; when one exists (Developer ID /
Authenticode effort), `updater.Config.PublicKey` adds Ed25519 verification
without changing the flow. Because the update is downloaded by the app itself
(no browser), no quarantine / Mark-of-the-Web is attached, so Gatekeeper and
SmartScreen do not re-prompt on the swapped app.

### Known gap: Windows CLI version drift

The in-place swap replaces `octo-desktop.exe` only. On Windows the Inno
installer owns the bundled CLI (`ensureBundledOcto` deliberately skips
Windows), so `octo.exe` stays at the installed version until the user next
runs an installer — and Programs & Features keeps showing the installer's
version. macOS has no such drift: the swapped bundle carries the new CLI and
the app re-seeds `~/.local/bin/octo` on launch.

## Two upgrade modes

The server already reports `native` (a `NativeBridge` is wired) on
`GET /api/version`. The single fact the frontend is missing is *which* upgrade
mechanism this server offers. We add an explicit field:

- `upgrade_mode: "cli"` — `octo serve`: the existing in-place swap
  (`POST /api/version/upgrade`) is valid. Badge shows the current "Upgrade"
  flow.
- `upgrade_mode: "installer"` — desktop shell (`Native != nil`): binary swap is
  refused; the UI offers "Download update" instead.

Because the desktop shell is the single shared server, a *remote* browser
connected to it also receives `upgrade_mode: "installer"` — correct, since that
peer likewise must not swap the desktop binary.

`upgrade_mode` is derived, not configured:

```
if s.cfg.Native != nil        -> "installer"
else if s.cfg.UpdateCheck     -> "cli"
else                          -> "cli"   // value irrelevant; needs_update is always false
```

## Backend

### `GET /api/version` (handleVersion)

Add two fields to the existing response:

- `upgrade_mode` — as above.
- `release_url` — `upgrade.BaseURL + "/releases/latest"`, so the frontend does
  not hardcode the repo. Constant; the endpoint stays unauthenticated and leaks
  no local path.

The desktop shell flips `UpdateCheck: false` → `true` so `latestVersion()`
actually resolves `latest`/`needs_update`. `UpdateCheck` now means only "perform
the outbound latest-release check"; whether an in-place swap is allowed is
governed by `upgrade_mode`, not by `UpdateCheck`.

### `POST /api/version/upgrade` (handleVersionUpgrade)

Add a guard at the top: when `s.cfg.Native != nil`, refuse the in-place swap.

```go
if s.cfg.Native != nil {
    writeError(w, http.StatusConflict, "this build updates through its installer; use the download link")
    return
}
```

This is defense in depth: the installer-mode frontend never calls this endpoint,
but the route is registered unconditionally, so a remote peer must not be able
to drive a desktop binary swap.

### New: `POST /api/native/open-external`

Opens a URL in the system browser via the bridge. Same shape and loopback guard
as the other `/api/native/*` handlers (`native_handlers.go`): registered only
when `Native != nil`, `403` for non-loopback peers, validates the URL is
`http(s)`.

```go
type nativeOpenExternalRequest struct {
    URL string `json:"url"`
}
```

Rejects any scheme other than `http`/`https` so the endpoint can't be coerced
into launching arbitrary local handlers.

### `NativeBridge` interface

Add one method:

```go
// OpenExternal opens url in the user's default browser (the release download
// page). http/https only; the server validates the scheme before calling.
OpenExternal(url string) error
```

## Desktop shell (`cmd/octo-desktop`)

### Config

`main.go`: `UpdateCheck: false` → `true`.

### `OpenExternal` (bridge.go)

Implemented on the Wails app. Prefer the Wails runtime browser API if present;
otherwise shell out per-platform (no new third-party dependency):

- macOS: `open <url>`
- Windows: `rundll32 url.dll,FileProtocolHandler <url>`
- Linux: `xdg-open <url>`

(Confirm the Wails v3 browser-open API before adding the exec fallback.)

### Tray menu item "Check for updates…"

`buildTrayMenu` has one item between "Settings" and the separator. While no
update is known it is "Check for updates…" (manual `upgrade.Check`, reporting
via toast); once a newer release is known it becomes "↑ Update to vX", whose
click runs `startUpdateFlow` — the in-place install where supported, the
download page otherwise. A daily `autoUpdateLoop` keeps the item current
without the user asking.

Pure native path; does not depend on a window being open. The update toast's
action button follows the same routing (its label is "Update Now" when the
build installs in place, "Open Download Page" otherwise). Strings live in
`lang.go` (zh/en).

## Frontend

### `VersionBadge.svelte`

`checkVersion()` reads `upgrade_mode` and `release_url` into state.

- `upgrade_mode === 'cli'`: unchanged — the existing upgrade→restart state
  machine.
- `upgrade_mode === 'installer'`: when `needsUpdate`, the popover shows the new
  release version and one primary action:
  - `self_update` true (the desktop build swaps itself — the bridge's
    `CanSelfUpdate`) **and** `localAccess` true → an **"Update Now"** button
    that `POST /api/native/self-update`s; the native updater window takes over
    (so the badge has no phase machinery for it). Failure to start falls back
    to the download page.
  - otherwise a **"Download update"** button that opens the download page:
    `localAccess` true (loopback — the desktop window itself, or a localhost
    browser) → `POST /api/native/open-external { url: release_url }`;
    otherwise (remote browser) → `window.open(release_url, '_blank')`.

The `upgrading` / `needs_restart` / `reconnecting` phases are never entered in
installer mode.

### `SettingsView.svelte`

Inside the existing `{#if $nativeShell}` block, add an "About / Updates" card:
current version, a "Check for updates" button, and — when an update is
available — the same "Download update" action as the badge. Reuses
`api.getVersion()`.

### `lib/api.ts`

Add `openExternal(url)` → `POST /api/native/open-external`. i18n keys for the
new badge/settings strings.

## Edge cases

- **Remote browser on the desktop server**: `native=true` but the peer is not on
  the local machine, so `/api/native/open-external` refuses it (loopback guard).
  The frontend already branches on `localAccess` and falls back to
  `window.open`, so the remote user still reaches the download page.
- **Dev / unbundled desktop build**: `needsUpdate()` returns false whenever
  `upgrade.Eligible() != nil` (dev version string), so a dev build never grows
  the badge — matching CLI behavior. The tray "Check for updates…" still works
  and reports status via dialog.
- **Notifications service absent** (unbundled): the tray flow uses modal
  dialogs, not `Notify`, so it is unaffected.

## Test points

- `handleVersion` returns `upgrade_mode: "cli"` for a plain server,
  `"installer"` when a stub `Native` is set; `release_url` present.
- `handleVersionUpgrade` returns 409 when `Native != nil`.
- `handleNativeOpenExternal`: 403 for non-loopback; 400 for a non-http(s)
  scheme; calls the bridge for a valid loopback request (stub bridge records the
  URL).
- Frontend: installer-mode badge renders "Download update" and routes to the
  native endpoint vs `window.open` by `localAccess`.

## Change list

Backend:
- `internal/server/version_upgrade_handlers.go` — `upgrade_mode` + `release_url`
  in `handleVersion`; native guard in `handleVersionUpgrade`.
- `internal/server/native_handlers.go` — `OpenExternal` on `NativeBridge`;
  `handleNativeOpenExternal`.
- `internal/server/server.go` — register `POST /api/native/open-external`.

Desktop:
- `cmd/octo-desktop/main.go` — `UpdateCheck: true`; tray "Check for updates…"
  item + handler.
- `cmd/octo-desktop/bridge.go` — implement `OpenExternal`.
- `cmd/octo-desktop/lang.go`, `lang_darwin.go`, `lang_windows.go`,
  `lang_other.go` — new tray/dialog strings.

Frontend (`make web-build` rebuilds `internal/server/webdist` locally — it is gitignored; CI builds it for releases):
- `web/src/components/layout/VersionBadge.svelte`
- `web/src/views/SettingsView.svelte`
- `web/src/lib/api.ts` + i18n message files.

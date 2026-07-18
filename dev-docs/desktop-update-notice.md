# Desktop update notification

The Wails desktop shell (`cmd/octo-desktop`) is distributed as a whole-app
installer (macOS pkg, Windows `octo-setup.exe`, Linux AppImage), not as the
`octo` CLI tarball. It cannot use the in-place binary swap that
`octo serve` offers, because the running executable is the app bundle's
`octo-desktop`, and `upgrade.Install` would rename the CLI `octo` binary over
it — breaking the app (and, on macOS, its bundle code signature). That is why
the desktop shell constructs the server with `UpdateCheck: false` today, which
suppresses the whole version badge.

This design gives the desktop shell a **notify-and-open** update flow instead:
detect a newer release, tell the user, and open the release download page in
the system browser. The user installs the new package themselves. No in-app
download, no in-place swap.

## Goals

- The desktop shell surfaces "a newer version is available" and links to the
  download page, from three entry points: the web version badge, a tray menu
  item, and the Settings page.
- The CLI (`octo serve`) self-upgrade flow is untouched.
- A remote browser connected to the desktop-hosted server is handled correctly
  (it also cannot swap the desktop binary, and its OS may differ).

## Non-goals

- In-app download of the installer, launching the installer, or silent
  auto-update. Explicitly out of scope for this iteration.

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

`buildTrayMenu` gains one item between "Settings" and the separator. On click,
a background goroutine (never block the UI thread):

1. `upgrade.Check(ctx)` for the latest tag.
2. Compare with `version.Version`:
   - Newer → native question dialog "vX is available. Open the download page?"
     → on confirm, `OpenExternal(BaseURL + "/releases/latest")`.
   - Already latest → native info dialog "You're on the latest version."
   - Check failed → native error dialog.

Pure native path; does not depend on a window being open. Reuses the existing
`confirm` / `showError` dialog helpers. New strings go in `lang.go` +
`lang_*.go` (zh/en), matching the tray/dialog i18n already there.

## Frontend

### `VersionBadge.svelte`

`checkVersion()` reads `upgrade_mode` and `release_url` into state.

- `upgrade_mode === 'cli'`: unchanged — the existing upgrade→restart state
  machine.
- `upgrade_mode === 'installer'`: when `needsUpdate`, the popover shows the new
  release version and a **"Download update"** button instead of "Upgrade".
  The button opens the download page:
  - `localAccess` true (loopback — the desktop window itself, or a localhost
    browser) → `POST /api/native/open-external { url: release_url }`.
  - otherwise (remote browser) → `window.open(release_url, '_blank')`.

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

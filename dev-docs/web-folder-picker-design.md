# Web Folder Picker

A native-feeling directory picker in the web UI that sets a session's working directory to a real path on the server's filesystem. The agent then operates on files under that path directly — `read_file`, `edit_file`, `terminal` — exactly as it does under the CLI. This makes "point the agent at a folder" the primary way to hand it local files, and demotes upload to the fallback it should be.

## Motivation

The web backend already runs the agent on the local filesystem with the same file tools as the CLI. A session already carries a working directory that can be retargeted to any local path (`PATCH /api/sessions/{id}/working_dir`), and the agent's tools run in that cwd. What is missing is a way for the user to *choose* that path from the browser: a browser file input only yields bytes, which is why the current mental model collapses into "upload the file." Upload copies bytes into `/api/uploads/` and hands the agent a path — a faithful workaround when the frontend and backend live on different machines, but pure friction when they are the same machine, which is the common case for `octo serve`.

The backend capability is done. This feature is the frontend affordance plus one read-only browsing endpoint to feed it.

## Goals

- Let the user browse the server's filesystem and pick a directory, from the browser, without typing a path.
- Set the picked directory as the session working dir via the existing `working_dir` endpoint — no new persistence, no new agent surface.
- Make the working directory a visible, primary control in the composer; keep upload as an explicit fallback rather than the default gesture.
- Cost nothing when the browser and server are not co-located: the picker is off, upload still works.

## Non-goals

- Not a file manager: no create / rename / move / delete, no file *content* reading (the artifacts endpoint already covers reading files the agent wrote).
- No remote filesystem browsing. The picker is localhost-only by design (see below); a remotely-reached server exposes no directory listing and the user falls back to upload or typing a path.
- No multi-root or bookmark model in this phase — one directory, picked fresh, like a native open-folder dialog.
- Not a replacement for `PATCH .../working_dir` typed input: typing a path stays available; the picker is an additional way to produce the same value.

## Security model: localhost-only, per request

Directory browsing discloses the server's filesystem layout to whoever holds the web session. On a loopback-bound single-user `octo serve` — the default (`127.0.0.1:8088`) — that discloses nothing the user could not already see, and nothing the agent could not already reach through `terminal`. There is no privilege boundary to protect locally: the user owns both ends.

The moment the server is reachable off-box (`--addr` on a wildcard or LAN address), that stops being true, so the endpoint is gated per request on the peer being loopback:

```go
if !isLoopbackRemote(r.RemoteAddr) {
    writeError(w, http.StatusForbidden, "directory browsing is available only from the local machine")
    return
}
```

`isLoopbackRemote` (`internal/server/auth.go:109`) already backs the loopback auth exemption: `net.ParseIP` + `IsLoopback`, covering `127.0.0.0/8`, `::1`, and the IPv4-mapped `::ffff:127.0.0.1`. Gating per request, not per process, is deliberate: the same server can serve a local browser (picker on) while also being reachable over a tunnel (picker off for that peer). It composes with `requireAuth`, not instead of it — the route registers through `Server.api` like every other, so a valid access key is still required; the loopback check is an *additional* gate specific to this endpoint's disclosure, layered on top of the DNS-rebinding and Origin protections that already harden the loopback exemption.

Because traversal offers no escalation beyond what the local user already has, there is no path-sandbox or root-whitelist: the picker can navigate anywhere the server process can `os.ReadDir`, like a native open dialog. Permission-denied and non-directory paths surface as errors rather than being hidden.

## Browsing endpoint

```
GET /api/fs/list?path=<dir>
```

Read-only. `path` is optional; when absent, listing starts at the user's home directory (`os.UserHomeDir`, falling back to the server launch dir). The path is resolved through the existing `expandDir` (`handlers.go:1374`) so `~`, relative, and absolute inputs behave the same as they do for `working_dir`, and validated with the same `os.Stat` branch set (not-exist → 400 with "create it first", permission → 400, not-a-directory → 400) so error copy stays consistent across the two endpoints.

Response:

```json
{
  "path": "/Users/alice/projects",
  "parent": "/Users/alice",
  "entries": [
    { "name": "octo-agent", "is_dir": true,  "is_symlink": false },
    { "name": "notes",       "is_dir": true,  "is_symlink": false },
    { "name": "README.md",   "is_dir": false, "is_symlink": false }
  ],
  "truncated": false
}
```

- `path` is the resolved absolute directory being listed; `parent` is its parent (`filepath.Dir`), empty at the filesystem root, so the frontend can offer "up" without guessing.
- Entries come from `os.ReadDir`. `is_dir` follows symlinks via an on-demand `os.Stat` (a symlink to a directory is navigable and picks as a directory); `is_symlink` is set from the raw `DirEntry` type so the UI can mark links. Entries sort directories first, then by name, case-insensitive.
- Files are included for orientation, not because they are pickable — the picker returns directories only. Showing files is what makes a folder recognizable ("yes, this is the repo, there's `go.mod`").
- `truncated` guards pathological directories (`node_modules`): entries are capped (1000) and `truncated: true` tells the UI to show a "list truncated" hint rather than silently implying the folder is small. No pagination in this phase.

Dotfiles are returned; the frontend hides them behind a "show hidden" toggle, default off.

The route registers in `server.go` alongside the others and runs the loopback gate as its first line:

```go
s.api("GET /api/fs/list", s.handleFsList)
```

## Frontend: the picker and the composer control

The composer already shows a working-dir chip driven by `chatWorkingDir` and refreshed by the `session_update` WS event. That chip becomes the entry point:

- Clicking the chip opens a **folder picker** modal: a single-pane directory browser (breadcrumb + "up", list of subdirectories, files shown greyed for context, hidden-file toggle) fed by `GET /api/fs/list`. Navigating into a folder re-fetches; "Select this folder" confirms the current `path`.
- Confirm calls the existing `PATCH /api/sessions/{id}/working_dir` with the chosen path. The server validates, saves, and broadcasts `session_update`; the chip refreshes through the existing store path with no new wiring.
- The Browse button is always present; it does not probe availability up front. When the server can't serve listings to this peer, the first `GET /api/fs/list` returns 403 and the modal shows the server's "available only from the local machine" message in place of a listing — so a remotely-served UI degrades cleanly to typed paths + upload, and tells the remote user *why* rather than silently hiding the affordance. The typed-path input stays available regardless.

Upload stays exactly as it is, repositioned as the fallback: when there is no shared filesystem (remote server, or a file genuinely coming from the user's own machine into a remote box), upload is still the right primitive and remains one gesture away. The change is emphasis — the working-dir control is the prominent one — not removal.

## Reuse by the Wails shell (phase 2)

The picker component is deliberately thin: it renders whatever `entries`/`path` shape it is handed and emits a chosen directory string. The Wails desktop shell can either keep feeding it from `/api/fs/list` unchanged, or swap the data source for a native OS folder dialog and emit the same directory string into the same `PATCH working_dir` call. Either way the working-dir plumbing built here is the plumbing the desktop app reuses; nothing here is throwaway.

## Testing

- `handleFsList`: loopback gate returns 403 for a non-loopback `RemoteAddr`; happy path lists a temp dir with a nested dir, a file, and a dotfile, asserting sort order and `is_dir`/`is_symlink`; a symlink-to-dir reports `is_dir: true`; not-exist / not-a-directory / permission paths return 400 with the expected copy; `truncated` trips past the cap. `httptest.NewServer`, no live network, per the repo's test rules.
- `expandDir` reuse is covered by its existing tests; the new endpoint asserts it resolves `~` and relative paths identically to `working_dir`.

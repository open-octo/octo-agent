package server

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
)

// NativeBridge is the desktop shell's hook into OS-native capabilities that a
// browser can't provide. It is nil under `octo serve` and set only by the
// Wails desktop build (cmd/octo-desktop), which supplies an implementation
// backed by the Wails runtime. When nil, the /api/native/* routes are never
// registered, so `octo serve` exposes no extra surface — the whole native
// layer is opt-in at construction, not a runtime branch on every request.
type NativeBridge interface {
	// PickFolder opens an OS directory-choose dialog seeded at startDir (which
	// may be empty) and returns the chosen absolute path. cancelled is true
	// when the user dismissed the dialog without choosing, in which case path
	// is empty.
	PickFolder(ctx context.Context, startDir string) (path string, cancelled bool, err error)

	// PickFile opens an OS file-choose dialog and returns the chosen absolute
	// path. Same cancelled semantics as PickFolder.
	PickFile(ctx context.Context, startDir string) (path string, cancelled bool, err error)

	// Notify raises an OS-native notification. Best-effort: the host logs its
	// own failures; callers don't handle an error.
	Notify(title, body string)

	// AutostartEnabled reports whether the app is registered to launch at login.
	AutostartEnabled() (bool, error)
	// SetAutostart registers (enable) or unregisters the app from launch-at-login.
	SetAutostart(enable bool) error

	// ToggleMaximise maximises the window if it isn't, or restores it if it is —
	// the double-click-the-titlebar zoom the frontend's draggable header can't do
	// itself (the page is octo-served, so it has no Wails runtime to call).
	ToggleMaximise()

	// Minimise minimises the window to the taskbar/dock.
	Minimise()

	// Close closes the window (the app's ShouldQuit hook decides whether the hub
	// actually terminates or keeps running in the tray).
	Close()

	// OpenExternal opens url in the user's default browser — used by the update
	// badge's "Download update" action to reach the release page, since the
	// desktop build updates through its installer, not an in-place swap. The
	// server validates url is http/https before calling.
	OpenExternal(url string) error

	// SaveFile shows an OS save dialog seeded with defaultName, writes content
	// to the chosen path, and returns it. cancelled is true when the user
	// dismissed the dialog (path empty). Backs the artifact "Download" action:
	// the octo-served webview can't trigger an in-page blob download, so the
	// desktop shell writes the file through a native dialog instead.
	SaveFile(ctx context.Context, defaultName, content string) (path string, cancelled bool, err error)
}

type nativePickFolderRequest struct {
	StartDir string `json:"start_dir"`
}

// POST /api/native/pick-folder — open the OS folder dialog (desktop only) and
// return the chosen directory. The frontend then sets it as the session
// working dir through the existing PATCH /api/sessions/{id}/working_dir,
// reusing that endpoint's validation and the cwd-chip refresh rather than
// duplicating them here. Registered only when a NativeBridge is present.
func (s *Server) handleNativePickFolder(w http.ResponseWriter, r *http.Request) {
	// Same-machine only, matching /api/fs/list. In the desktop build every
	// request is loopback anyway; the guard just refuses to drive a native
	// dialog on behalf of some other peer if the port were ever reachable.
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "native dialogs are available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		// Unreachable via routing (the route isn't registered without a bridge),
		// kept as defense so a future unconditional registration can't panic.
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}

	// Body is optional; a missing or unparseable one just leaves StartDir empty.
	var req nativePickFolderRequest
	_ = readBodyJSON(r, &req)

	path, cancelled, err := s.cfg.Native.PickFolder(r.Context(), req.StartDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      path,
		"cancelled": cancelled,
	})
}

// POST /api/native/pick-file — open the OS file dialog (desktop only) and
// return the chosen absolute path. The frontend attaches it by real path (no
// upload) so the agent reads it in place. Registered only with a bridge.
func (s *Server) handleNativePickFile(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "native dialogs are available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	var req nativePickFolderRequest
	_ = readBodyJSON(r, &req)
	path, cancelled, err := s.cfg.Native.PickFile(r.Context(), req.StartDir)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      path,
		"cancelled": cancelled,
	})
}

type nativeNotifyRequest struct {
	Title string `json:"title"`
	Body  string `json:"body"`
}

// POST /api/native/notify — raise an OS-native notification (desktop only).
// The frontend calls this in desktop mode instead of the browser Notification
// API, which native webviews don't implement. Best-effort: it always returns
// ok; the bridge swallows delivery failures. Registered only with a bridge.
func (s *Server) handleNativeNotify(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "native notifications are available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	var req nativeNotifyRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	s.cfg.Native.Notify(req.Title, req.Body)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type nativeAutostartRequest struct {
	Enabled bool `json:"enabled"`
}

// GET /api/native/autostart — report launch-at-login state (desktop only).
func (s *Server) handleNativeAutostartGet(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	enabled, err := s.cfg.Native.AutostartEnabled()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
}

// PUT /api/native/autostart — set launch-at-login (desktop only).
func (s *Server) handleNativeAutostartSet(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	var req nativeAutostartRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.cfg.Native.SetAutostart(req.Enabled); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": req.Enabled})
}

// POST /api/native/window/toggle-maximise — maximise/restore the desktop window
// (the double-click-titlebar zoom). Desktop only, loopback-gated.
func (s *Server) handleNativeToggleMaximise(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	s.cfg.Native.ToggleMaximise()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/native/window/minimise — minimise the desktop window to the
// taskbar/dock. Desktop only, loopback-gated.
func (s *Server) handleNativeMinimise(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	s.cfg.Native.Minimise()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// POST /api/native/window/close — close the desktop window. The app's ShouldQuit
// decides whether the hub actually terminates or keeps running in the tray.
// Desktop only, loopback-gated.
func (s *Server) handleNativeClose(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	s.cfg.Native.Close()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type nativeOpenExternalRequest struct {
	URL string `json:"url"`
}

// POST /api/native/open-external — open a URL with the system's default
// handler (desktop only). The update badge calls this in installer mode to
// reach the release download page; chat links route through it too. Loopback-
// gated like the other native routes, and restricted to an allowlist of
// user-facing schemes (http/https for the browser, mailto/tel for the mail and
// dialer apps) so the endpoint can't be coerced into launching an arbitrary
// local handler (file://, custom app schemes).
// openExternalSchemeAllowed reports whether a URL scheme is safe to hand to the
// system's default handler: the browser (http/https) and the mail/dialer apps
// (mailto/tel). Everything else — file://, custom app schemes — is rejected so
// the loopback endpoint can't be turned into a local-handler launcher.
func openExternalSchemeAllowed(scheme string) bool {
	switch scheme {
	case "http", "https", "mailto", "tel":
		return true
	default:
		return false
	}
}

func (s *Server) handleNativeOpenExternal(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	var req nativeOpenExternalRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u, err := url.Parse(req.URL)
	if err != nil || !openExternalSchemeAllowed(u.Scheme) {
		writeError(w, http.StatusBadRequest, "only http(s), mailto, and tel URLs may be opened")
		return
	}
	if err := s.cfg.Native.OpenExternal(req.URL); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type nativeSaveFileRequest struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	// Encoding defaults to "utf8" (content written as-is). When "base64",
	// content is decoded to bytes before writing — used for binary blobs
	// (e.g. skill zip exports) that would otherwise not survive a UTF-8
	// JSON round-trip.
	Encoding string `json:"encoding"`
}

// POST /api/native/save-file — show the OS save dialog (desktop only), write
// the posted content to the chosen path, and return it. The artifact panel
// calls this instead of an in-page blob download: the page is octo-served, so
// the webview has no download delegate and a blob <a download> click does
// nothing. Loopback-gated like the other native routes; registered only with a
// bridge.
func (s *Server) handleNativeSaveFile(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRemote(r.RemoteAddr) {
		writeError(w, http.StatusForbidden, "native dialogs are available only from the local machine")
		return
	}
	if s.cfg.Native == nil {
		writeError(w, http.StatusNotFound, "native bridge not available")
		return
	}
	var req nativeSaveFileRequest
	if err := readBodyJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	content := req.Content
	if req.Encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(req.Content)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid base64 content")
			return
		}
		content = string(decoded)
	}
	path, cancelled, err := s.cfg.Native.SaveFile(r.Context(), req.Name, content)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":      path,
		"cancelled": cancelled,
	})
}

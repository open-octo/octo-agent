package server

import (
	"context"
	"net/http"
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

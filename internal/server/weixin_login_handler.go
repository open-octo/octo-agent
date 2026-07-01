package server

import (
	"context"
	"net/http"
	"sync"
	"time"

	"github.com/open-octo/octo-agent/internal/channel/adapters/weixin/ilink"
)

// ─── Weixin QR login over the web API ───────────────────────────────────────
//
// The former `octo channel login` CLI flow, exposed as REST so the Channels
// panel (and the channel-manager skill) can drive it:
//
//	POST   /api/channels/weixin/login   — start a flow (no-op if logged in,
//	                                      unless {"force": true})
//	GET    /api/channels/weixin/login   — poll the flow state
//	DELETE /api/channels/weixin/login   — cancel an in-flight flow
//
// ilink.Login blocks until the QR is confirmed/expired, so it runs in a
// goroutine and reports through this state, one flow at a time.

type weixinLoginFlow struct {
	mu     sync.Mutex
	active bool
	status string // "idle" | "pending" | "scanned" | "done" | "failed"
	qrURL  string
	userID string
	errMsg string
	cancel context.CancelFunc

	// qrBaseURL is a test seam (stub iLink host); empty = production.
	qrBaseURL string
}

func (f *weixinLoginFlow) snapshot() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]string{"status": f.status}
	if f.status == "" {
		out["status"] = "idle"
	}
	if f.qrURL != "" {
		out["qr_url"] = f.qrURL
	}
	if f.userID != "" {
		out["user_id"] = f.userID
	}
	if f.errMsg != "" {
		out["error"] = f.errMsg
	}
	return out
}

func (f *weixinLoginFlow) set(fn func(*weixinLoginFlow)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (s *Server) handleWeixinLoginStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	_ = readBodyJSON(r, &req) // empty body = no force

	f := &s.weixinLogin

	// Already logged in and not forcing: report it without starting a flow.
	if !req.Force {
		if creds, err := ilink.LoadCredentials(""); err == nil && creds != nil {
			writeJSON(w, http.StatusOK, map[string]string{
				"status":  "already_logged_in",
				"user_id": creds.UserID,
			})
			return
		}
	}

	f.mu.Lock()
	if f.active {
		f.mu.Unlock()
		writeJSON(w, http.StatusOK, f.snapshot())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	f.active = true
	f.status = "pending"
	f.qrURL = ""
	f.userID = ""
	f.errMsg = ""
	f.cancel = cancel
	qrBase := f.qrBaseURL
	f.mu.Unlock()

	qrReady := make(chan struct{}, 1)
	go func() {
		defer cancel()
		creds, err := ilink.Login(ctx, ilink.NewClient(), ilink.LoginOptions{
			Force:     req.Force,
			QRBaseURL: qrBase,
			OnQRURL: func(url string) {
				f.set(func(f *weixinLoginFlow) { f.qrURL = url; f.status = "pending" })
				select {
				case qrReady <- struct{}{}:
				default:
				}
			},
			OnScanned: func() {
				f.set(func(f *weixinLoginFlow) { f.status = "scanned" })
			},
			OnExpired: func() {
				// The library refreshes the QR itself; OnQRURL fires again
				// with the new one, so stay "pending".
				f.set(func(f *weixinLoginFlow) { f.status = "pending" })
			},
		})
		f.set(func(f *weixinLoginFlow) {
			f.active = false
			if err != nil {
				f.status = "failed"
				f.errMsg = err.Error()
				return
			}
			f.status = "done"
			f.userID = creds.UserID
		})
	}()

	// Give the flow a moment to fetch the first QR so the response already
	// carries qr_url; fall back to polling otherwise.
	select {
	case <-qrReady:
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
	}
	writeJSON(w, http.StatusOK, f.snapshot())
}

func (s *Server) handleWeixinLoginStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.weixinLogin.snapshot())
}

func (s *Server) handleWeixinLoginCancel(w http.ResponseWriter, r *http.Request) {
	f := &s.weixinLogin
	f.mu.Lock()
	if f.cancel != nil && f.active {
		f.cancel()
	}
	f.active = false
	f.status = "idle"
	f.qrURL = ""
	f.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

package server

import "net/http"

// TunnelPairing is the material a phone needs to pair with this host over the
// managed tunnel: the deep-link URL a QR encodes, plus display fields. It is
// published by `octo serve --tunnel` (see cmd/octo) and read by the web UI to
// render a pairing QR. The server holds it as opaque display data — it learns
// nothing about the tunnel's Noise or relay mechanics from it.
type TunnelPairing struct {
	PairURL  string `json:"pair_url"`
	Relay    string `json:"relay"`
	TunnelID string `json:"tunnel_id"`
}

// SetTunnelPairing publishes the current pairing material, or clears it with
// nil. Safe for concurrent use.
func (s *Server) SetTunnelPairing(p *TunnelPairing) {
	s.tunnelPairing.Store(p)
}

// handleTunnelPairing returns the pairing material for the web UI. When the
// managed tunnel is off, enabled is false and there is nothing to render.
func (s *Server) handleTunnelPairing(w http.ResponseWriter, r *http.Request) {
	p := s.tunnelPairing.Load()
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":   true,
		"pair_url":  p.PairURL,
		"relay":     p.Relay,
		"tunnel_id": p.TunnelID,
	})
}

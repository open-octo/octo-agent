package server

import (
	"net"
	"net/http"
	"net/url"
)

// The mobile app connects straight to this server: it bundles the web
// frontend, and its shim rewrites the frontend's /api and /ws calls to the
// server's address with the access key attached. Pairing is just handing the
// app that address and key — the same bootstrap link `octo serve` prints on
// startup, rendered as a QR in Settings › Mobile.

// capacitorOrigins are the fixed Origins the bundled app's WebView sends: iOS
// serves the bundle from capacitor://localhost, Android from https://localhost.
// Both are allowed CORS by default so the app works without a --cors flag.
var capacitorOrigins = map[string]bool{
	"capacitor://localhost": true,
	"https://localhost":     true,
}

func isCapacitorOrigin(origin string) bool {
	return capacitorOrigins[origin]
}

type mobileConnectInfo struct {
	AccessKey string `json:"access_key"`
	// URLs are the addresses a phone on the same network may reach this
	// server at, derived from the bind address. Empty when the server only
	// listens on loopback, which no phone can reach without a tunnel or proxy.
	URLs         []string `json:"urls"`
	LoopbackOnly bool     `json:"loopback_only"`
}

// handleMobileConnect returns what the Settings › Mobile QR encodes. The key
// goes only to a caller that already got past requireAuth: a holder of the key,
// or a genuinely local browser.
func (s *Server) handleMobileConnect(w http.ResponseWriter, r *http.Request) {
	urls, loopbackOnly := reachableURLs(s.cfg.Addr)
	// Never null: the settings page spreads the list, and a loopback bind
	// (the desktop app's default) has no addresses to offer.
	if urls == nil {
		urls = []string{}
	}
	writeJSON(w, http.StatusOK, mobileConnectInfo{
		AccessKey:    s.accessKey,
		URLs:         urls,
		LoopbackOnly: loopbackOnly,
	})
}

// reachableURLs lists http://host:port URLs for the bind address: the bind
// host itself when it is specific, or every non-loopback IPv4 interface
// address for a wildcard bind.
func reachableURLs(addr string) (urls []string, loopbackOnly bool) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, false
	}
	toURL := func(h string) string {
		return (&url.URL{Scheme: "http", Host: net.JoinHostPort(h, port)}).String()
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		if host == "localhost" {
			return nil, true
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil, true
		}
		return []string{toURL(host)}, false
	}
	ifaddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, false
	}
	for _, a := range ifaddrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP
		if ip.IsLoopback() || ip.To4() == nil || !ip.IsGlobalUnicast() {
			continue
		}
		urls = append(urls, toURL(ip.String()))
	}
	return urls, false
}

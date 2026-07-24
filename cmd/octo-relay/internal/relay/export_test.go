package relay

import "net/http"

// TunnelIDFromRequest exposes the double-read routing rule to the external
// test package.
func TunnelIDFromRequest(req *http.Request) string { return tunnelIDFromRequest(req) }

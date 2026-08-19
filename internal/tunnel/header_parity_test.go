package tunnel

import (
	"testing"

	"github.com/open-octo/octo-agent/internal/server"
)

// The two packages declare the marker's name independently — a one-string
// constant is not worth a production dependency between them — so nothing but
// this assertion stops them drifting apart. Drift is silent and expensive: the
// bridge would keep stamping a header the server no longer recognises, and
// every phone across the relay would quietly regain local-peer standing (real
// paths on the host's disk, the OS dialogs, the unauthenticated exemption).
//
// The import lives in a test file, so internal/tunnel still ships with no
// dependency on internal/server.
func TestForwardedHeaderNameMatchesServer(t *testing.T) {
	if headerForwarded != server.HeaderForwarded {
		t.Fatalf("marker name drifted: tunnel has %q, server expects %q",
			headerForwarded, server.HeaderForwarded)
	}
}

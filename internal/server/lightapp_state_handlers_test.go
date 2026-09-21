package server

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The light-app state relay is on its way out (the mirror it feeds is retired
// in favour of the session-artifact one — artifact_state_handlers.go); what
// remains covered here is the input validation, and the rest of the file went
// with the lightapp_state / view_lightapp tools whose reads were the only
// way to observe the mirror from a test.
func TestPutLightAppState_RejectsBadSlug(t *testing.T) {
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	for _, slug := range []string{"..", "a/b"} {
		req := httptest.NewRequest("PUT", "/api/light-apps/"+slug+"/state", strings.NewReader(""))
		w := httptest.NewRecorder()
		serveLoopback(srv.mux, w, req)
		if w.Code == 200 {
			t.Errorf("slug %q was accepted", slug)
		}
	}
}

package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
)

// TestHandleTurn_RejectsWhenTurnRunning: a REST turn must refuse (409) when a
// turn is already running for the session — otherwise it would run concurrently
// with the in-flight WS turn (which holds only the turnRunning flag, not the
// mutex) and both would Save() the same session file, clobbering history.
func TestHandleTurn_RejectsWhenTurnRunning(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)

	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatal(err)
	}

	// Simulate a WS turn already in flight for this session.
	if srv.turnRunning == nil {
		srv.turnRunning = make(map[string]bool)
	}
	srv.turnRunning[sess.ID] = true

	body := bytes.NewBufferString(`{"message":"hello"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/chat/"+sess.ID+"/turn", body)
	w := httptest.NewRecorder()
	serveLoopback(srv.mux, w, req)

	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 when a turn is already running (body: %s)", w.Code, w.Body.String())
	}
}

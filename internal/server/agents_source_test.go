package server

import (
	"net/http"
	"strings"
	"testing"
)

// agentResponse.Source documents itself as always present, and the web card
// branches on it (a "default" agent is read-only and offers no edit action).
// Create and update answered from the struct the handler had just assembled,
// which never carries a Source — only the read path stamps it — so both
// returned source:"".
func TestHandleAgents_CreateAndUpdateReportSource(t *testing.T) {
	srv := agentsTestServer(t)

	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "Sourced",
		"description": "d"
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"source":"user"`) {
		t.Errorf("create response did not report a source: %s", body)
	}

	created := doAgent(t, srv, "sourced")
	if created.Source != "user" {
		t.Fatalf("get reported source %q, want user", created.Source)
	}

	w = doJSON(t, srv, http.MethodPut, "/api/agents/sourced", `{
		"name": "Sourced v2",
		"description": "d2"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"source":"user"`) {
		t.Errorf("update response did not report a source: %s", body)
	}
}

package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// An explicit `tools: []` is a restriction, and every hop it survives is one
// where an agent can't silently get its toolbelt back. This walks the whole
// API round trip in raw JSON, because the bug it guards against lives in the
// difference between an absent key and an empty array — which is exactly what
// decoding into a struct and checking len() cannot see.
func TestHandleAgents_EmptyToolsSurvivesRoundTrip(t *testing.T) {
	srv := agentsTestServer(t)

	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "Locked Down",
		"description": "answers, nothing else",
		"tools": []
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("create response dropped the explicit empty list: %s", body)
	}

	var created agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Tools == nil {
		t.Fatal("create response reported the restriction as inheritance")
	}

	// GET must still show it.
	w = doJSON(t, srv, http.MethodGet, "/api/agents/"+created.ID, "")
	if body := w.Body.String(); !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("get dropped the explicit empty list: %s", body)
	}

	// The edit flow is told to send the smallest possible change, so a PUT
	// that only touches the description is the common case. It must not be
	// read as "give this agent every tool".
	w = doJSON(t, srv, http.MethodPut, "/api/agents/"+created.ID, `{
		"name": "Locked Down",
		"description": "answers, still nothing else"
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("a PUT omitting tools widened the agent to every tool: %s", body)
	}

	// And it must have persisted, not just echoed.
	w = doJSON(t, srv, http.MethodGet, "/api/agents/"+created.ID, "")
	if body := w.Body.String(); !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("the restriction did not survive on disk: %s", body)
	}
}

// The mirror case: an agent that never declared a list inherits everything,
// and must not acquire a phantom restriction on the way through the API.
func TestHandleAgents_AbsentToolsStaysAbsent(t *testing.T) {
	srv := agentsTestServer(t)

	w := doJSON(t, srv, http.MethodPost, "/api/agents", `{
		"name": "Open Agent",
		"description": "d"
	}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); strings.Contains(body, `"tools"`) {
		t.Fatalf("create invented a tools key: %s", body)
	}

	var created agentResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Tools != nil {
		t.Errorf("expected an absent list, got %#v", *created.Tools)
	}

	// An explicit [] on a previously-open agent is a real edit and must land.
	w = doJSON(t, srv, http.MethodPut, "/api/agents/"+created.ID, `{
		"name": "Open Agent",
		"description": "d",
		"tools": []
	}`)
	if w.Code != http.StatusOK {
		t.Fatalf("update = %d: %s", w.Code, w.Body.String())
	}
	if body := w.Body.String(); !strings.Contains(body, `"tools":[]`) {
		t.Fatalf("an explicit restriction was ignored: %s", body)
	}
}

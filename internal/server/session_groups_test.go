package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// groupTestServer builds a loopback server with an isolated HOME so the
// registry file lives under a per-test temp dir.
func groupTestServer(t *testing.T) *Server {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	t.Setenv("USERPROFILE", tmp)
	return mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})
}

func doGroupReq(t *testing.T, srv *Server, method, target string, body any) (*httptest.ResponseRecorder, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, rdr)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	serveLoopback(srv.mux, rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
	}
	return rec, out
}

func TestSessionGroups_CreateListRename(t *testing.T) {
	srv := groupTestServer(t)

	// Empty to start.
	rec, out := doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: status %d", rec.Code)
	}
	if groups, _ := out["groups"].([]any); len(groups) != 0 {
		t.Fatalf("expected 0 groups, got %d", len(groups))
	}

	// Create.
	rec, out = doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "  Work  "})
	if rec.Code != http.StatusOK {
		t.Fatalf("create: status %d body %s", rec.Code, rec.Body.String())
	}
	g, _ := out["group"].(map[string]any)
	if g["name"] != "Work" {
		t.Fatalf("expected trimmed name %q, got %q", "Work", g["name"])
	}
	id, _ := g["id"].(string)
	if id == "" {
		t.Fatal("create: empty group id")
	}

	// Blank name rejected.
	rec, _ = doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "   "})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("blank name: expected 400, got %d", rec.Code)
	}

	// Rename.
	rec, out = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+id, map[string]any{"name": "学习"})
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: status %d", rec.Code)
	}
	if g, _ = out["group"].(map[string]any); g["name"] != "学习" {
		t.Fatalf("rename: got %q", g["name"])
	}

	// List reflects the rename and persists (fresh load from disk).
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(groups) != 1 || groups[0].Name != "学习" {
		t.Fatalf("persisted groups = %+v", groups)
	}
}

func TestSessionGroups_ToggleCollapsed(t *testing.T) {
	srv := groupTestServer(t)
	_, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "G"})
	id := out["group"].(map[string]any)["id"].(string)

	rec, out := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+id, map[string]any{"collapsed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("collapse: status %d", rec.Code)
	}
	if c, _ := out["group"].(map[string]any)["collapsed"].(bool); !c {
		t.Fatal("collapsed not set to true")
	}
	groups, _ := loadSessionGroups()
	if !groups[0].Collapsed {
		t.Fatal("collapsed not persisted")
	}

	// Empty PATCH body (neither field) is a 400.
	rec, _ = doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+id, map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty patch: expected 400, got %d", rec.Code)
	}
}

func TestSessionGroups_SingleMembership(t *testing.T) {
	srv := groupTestServer(t)
	_, o1 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "A"})
	g1 := o1["group"].(map[string]any)["id"].(string)
	_, o2 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "B"})
	g2 := o2["group"].(map[string]any)["id"].(string)

	const sid = "20260101-000000-deadbeef"

	// Move into A.
	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": g1})
	if rec.Code != http.StatusOK {
		t.Fatalf("move to A: status %d", rec.Code)
	}
	// Move into B — must leave A.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": g2})
	if rec.Code != http.StatusOK {
		t.Fatalf("move to B: status %d", rec.Code)
	}
	groups, _ := loadSessionGroups()
	byID := map[string]sessionGroup{}
	for _, g := range groups {
		byID[g.ID] = g
	}
	if len(byID[g1].SessionIDs) != 0 {
		t.Fatalf("A should be empty, got %v", byID[g1].SessionIDs)
	}
	if len(byID[g2].SessionIDs) != 1 || byID[g2].SessionIDs[0] != sid {
		t.Fatalf("B should hold %s, got %v", sid, byID[g2].SessionIDs)
	}

	// Ungroup (empty target).
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": ""})
	if rec.Code != http.StatusOK {
		t.Fatalf("ungroup: status %d", rec.Code)
	}
	groups, _ = loadSessionGroups()
	for _, g := range groups {
		if len(g.SessionIDs) != 0 {
			t.Fatalf("group %s still holds %v after ungroup", g.ID, g.SessionIDs)
		}
	}

	// Move to a nonexistent group → 404.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": "g-nope"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("move to missing group: expected 404, got %d", rec.Code)
	}
}

func TestSessionGroups_Delete(t *testing.T) {
	srv := groupTestServer(t)
	_, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Temp"})
	id := out["group"].(map[string]any)["id"].(string)

	rec, _ := doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+id, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: status %d", rec.Code)
	}
	groups, _ := loadSessionGroups()
	if len(groups) != 0 {
		t.Fatalf("expected group removed, got %+v", groups)
	}

	// Deleting again → 404.
	rec, _ = doGroupReq(t, srv, http.MethodDelete, "/api/session-groups/"+id, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete missing: expected 404, got %d", rec.Code)
	}
}

func TestSessionGroups_RenameUnknown(t *testing.T) {
	srv := groupTestServer(t)
	rec, _ := doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/g-missing", map[string]any{"name": "X"})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rename missing: expected 404, got %d", rec.Code)
	}
}

func TestSessionGroups_Reorder(t *testing.T) {
	srv := groupTestServer(t)
	ids := make([]string, 3)
	for i, name := range []string{"A", "B", "C"} {
		_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": name})
		ids[i] = o["group"].(map[string]any)["id"].(string)
	}

	// Reverse the order: C, B, A.
	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/session-groups/order",
		map[string]any{"ids": []string{ids[2], ids[1], ids[0]}})
	if rec.Code != http.StatusOK {
		t.Fatalf("reorder: status %d", rec.Code)
	}
	groups, _ := loadSessionGroups()
	got := []string{groups[0].Name, groups[1].Name, groups[2].Name}
	if got[0] != "C" || got[1] != "B" || got[2] != "A" {
		t.Fatalf("expected [C B A], got %v", got)
	}

	// A partial/stale request (only one known id, one bogus) keeps the omitted
	// groups, appended in their current order after the named one.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/session-groups/order",
		map[string]any{"ids": []string{ids[0], "g-bogus"}})
	if rec.Code != http.StatusOK {
		t.Fatalf("partial reorder: status %d", rec.Code)
	}
	groups, _ = loadSessionGroups()
	if len(groups) != 3 || groups[0].Name != "A" {
		t.Fatalf("expected A first and all 3 kept, got %+v", groups)
	}

	// Duplicate IDs in the request are deduped (each group appears once).
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/session-groups/order",
		map[string]any{"ids": []string{ids[1], ids[1], ids[0], ids[2]}})
	if rec.Code != http.StatusOK {
		t.Fatalf("dup reorder: status %d", rec.Code)
	}
	groups, _ = loadSessionGroups()
	if len(groups) != 3 {
		t.Fatalf("dedupe failed, got %d groups: %+v", len(groups), groups)
	}
	if groups[0].Name != "B" || groups[1].Name != "A" || groups[2].Name != "C" {
		t.Fatalf("expected [B A C], got %v", []string{groups[0].Name, groups[1].Name, groups[2].Name})
	}

	// A pin set before reordering survives the reorder (same shared registry).
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/sess-1/pin", map[string]any{"pinned": true})
	doGroupReq(t, srv, http.MethodPut, "/api/session-groups/order",
		map[string]any{"ids": []string{ids[0], ids[1], ids[2]}})
	if pins, _ := loadPinnedSessions(); len(pins) != 1 || pins[0] != "sess-1" {
		t.Fatalf("pin lost across reorder, got %v", pins)
	}
}

func TestSessionPin_PinListUnpin(t *testing.T) {
	srv := groupTestServer(t)
	const sid = "20260101-000000-deadbeef"

	// Nothing pinned yet.
	_, out := doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if pins, ok := out["pinned_session_ids"].([]any); !ok || len(pins) != 0 {
		t.Fatalf("expected empty pinned list, got %v", out["pinned_session_ids"])
	}

	// Pin it.
	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("pin: status %d", rec.Code)
	}
	pins, err := loadPinnedSessions()
	if err != nil || len(pins) != 1 || pins[0] != sid {
		t.Fatalf("expected [%s], got %v (err %v)", sid, pins, err)
	}

	// GET reflects the pin.
	_, out = doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if got := out["pinned_session_ids"].([]any); len(got) != 1 || got[0].(string) != sid {
		t.Fatalf("GET pinned list = %v", got)
	}

	// Pinning again is idempotent (no duplicate).
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": true})
	if pins, _ = loadPinnedSessions(); len(pins) != 1 {
		t.Fatalf("re-pin duplicated: %v", pins)
	}

	// Unpin.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("unpin: status %d", rec.Code)
	}
	if pins, _ = loadPinnedSessions(); len(pins) != 0 {
		t.Fatalf("expected empty after unpin, got %v", pins)
	}
}

// Pins and groups share one registry file; editing one must not clobber the
// other.
func TestSessionPin_CoexistsWithGroups(t *testing.T) {
	srv := groupTestServer(t)
	const sid = "20260101-000000-deadbeef"

	// Pin a session, then create/rename a group.
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": true})
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", map[string]any{"name": "Work"})
	gid := o["group"].(map[string]any)["id"].(string)
	doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"name": "Work2"})

	// The pin survived the group writes.
	if pins, _ := loadPinnedSessions(); len(pins) != 1 || pins[0] != sid {
		t.Fatalf("pin lost after group edits, got %v", pins)
	}

	// Moving a session into the group must not wipe the pin either.
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/other/group", map[string]any{"group_id": gid})
	if pins, _ := loadPinnedSessions(); len(pins) != 1 || pins[0] != sid {
		t.Fatalf("pin lost after group membership change, got %v", pins)
	}

	// Conversely, an unrelated unpin must not drop the group.
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": false})
	groups, _ := loadSessionGroups()
	if len(groups) != 1 || groups[0].Name != "Work2" {
		t.Fatalf("group lost after unpin, got %+v", groups)
	}
}

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/open-octo/octo-agent/internal/agent"
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

// newGroupBody is the body for creating a group over HTTP. Every group a client
// creates is a project, so a working directory is part of the minimum request.
func newGroupBody(t *testing.T, name string) map[string]any {
	t.Helper()
	return map[string]any{"name": name, "working_dir": t.TempDir()}
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
	rec, out = doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "  Work  "))
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
	_, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "G"))
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

// Single membership is enforced by addSessionToGroup, the only path that files
// a session anywhere: the session is removed from every group before being
// appended to the target.
func TestSessionGroups_SingleMembership(t *testing.T) {
	srv := groupTestServer(t)
	_, o1 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "A"))
	g1 := o1["group"].(map[string]any)["id"].(string)
	_, o2 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "B"))
	g2 := o2["group"].(map[string]any)["id"].(string)

	const sid = "20260101-000000-deadbeef"

	if err := addSessionToGroup(g1, sid); err != nil {
		t.Fatalf("file into A: %v", err)
	}
	if err := addSessionToGroup(g2, sid); err != nil {
		t.Fatalf("file into B: %v", err)
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

	// A nonexistent target is an error, not a silently dropped write.
	if err := addSessionToGroup("g-nope", sid); err == nil {
		t.Error("filing into a missing group should fail")
	}
}

// newDiskSession creates a real session file under the test HOME and returns
// its ID — handleSetSessionGroup refuses to file a session that doesn't exist.
func newDiskSession(t *testing.T) string {
	t.Helper()
	sess := agent.NewSession("stub-model", "")
	if err := sess.Save(); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return sess.ID
}

// A loose session can be filed into a project after creation; that is the only
// move there is.
func TestSessionGroups_MoveLooseSessionIntoProject(t *testing.T) {
	srv := groupTestServer(t)
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "A"))
	gid := o["group"].(map[string]any)["id"].(string)
	sid := newDiskSession(t)

	rec, out := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": gid})
	if rec.Code != http.StatusOK {
		t.Fatalf("move: status %d body %s", rec.Code, rec.Body.String())
	}
	if g, _ := out["group"].(map[string]any); g["id"] != gid {
		t.Fatalf("response group = %v, want %s", out["group"], gid)
	}
	assertGroupOrder(t, gid, []string{sid})
	if p := projectForSession(sid); p == nil || p.ID != gid {
		t.Fatalf("projectForSession = %v, want group %s", p, gid)
	}
}

// Filing clears a stale collapsed entry, the same as the creation path —
// group membership and collapsing are mutually exclusive.
func TestSessionGroups_MoveDropsCollapsed(t *testing.T) {
	srv := groupTestServer(t)
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "A"))
	gid := o["group"].(map[string]any)["id"].(string)
	sid := newDiskSession(t)

	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("collapse: status %d", rec.Code)
	}
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": gid})
	if rec.Code != http.StatusOK {
		t.Fatalf("move: status %d body %s", rec.Code, rec.Body.String())
	}
	gf, err := loadRegistryFile()
	if err != nil {
		t.Fatalf("loadRegistryFile: %v", err)
	}
	for _, id := range gf.CollapsedSessionIDs {
		if id == sid {
			t.Fatal("session stayed collapsed after being filed into a project")
		}
	}
}

// A session already in a project stays there: moving between projects (or
// re-filing into the same one) is refused, so a session's directory, memory
// tier, and mounts keep one consistent history.
func TestSessionGroups_MoveOutOfProjectIsRefused(t *testing.T) {
	srv := groupTestServer(t)
	_, o1 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "A"))
	g1 := o1["group"].(map[string]any)["id"].(string)
	_, o2 := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "B"))
	g2 := o2["group"].(map[string]any)["id"].(string)
	sid := newDiskSession(t)
	if err := addSessionToGroup(g1, sid); err != nil {
		t.Fatalf("file in: %v", err)
	}

	for _, target := range []string{g1, g2} {
		rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": target})
		if rec.Code != http.StatusConflict {
			t.Errorf("move to %q: status %d, want 409", target, rec.Code)
		}
	}
	// Membership is untouched by the refused requests.
	assertGroupOrder(t, g1, []string{sid})
}

// Bad targets: a missing group is 404, a missing session is 404, an empty
// group_id is 400 — and none of them touch the registry.
func TestSessionGroups_MoveBadTargets(t *testing.T) {
	srv := groupTestServer(t)
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "A"))
	gid := o["group"].(map[string]any)["id"].(string)
	sid := newDiskSession(t)

	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": "g-nope"})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing group: status %d, want 404", rec.Code)
	}
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/no-such-session/group", map[string]any{"group_id": gid})
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing session: status %d, want 404", rec.Code)
	}
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/group", map[string]any{"group_id": " "})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("blank group_id: status %d, want 400", rec.Code)
	}
	assertGroupOrder(t, gid, []string{})
}

func TestAddSessionToGroup_PrependsNewest(t *testing.T) {
	srv := groupTestServer(t)
	_, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "G"))
	gid := out["group"].(map[string]any)["id"].(string)

	// Newly created sessions land at the top of their group, newest first —
	// matching the sidebar's newest-first session list.
	for _, sid := range []string{"s-old", "s-mid", "s-new"} {
		if err := addSessionToGroup(gid, sid); err != nil {
			t.Fatalf("addSessionToGroup(%s): %v", sid, err)
		}
	}
	assertGroupOrder(t, gid, []string{"s-new", "s-mid", "s-old"})

	// Re-adding an existing member moves it to the top (filter + prepend).
	if err := addSessionToGroup(gid, "s-old"); err != nil {
		t.Fatalf("addSessionToGroup(s-old again): %v", err)
	}
	assertGroupOrder(t, gid, []string{"s-old", "s-new", "s-mid"})
}

// assertGroupOrder reloads the registry from disk and asserts the group's
// SessionIDs exactly match want, in order.
func assertGroupOrder(t *testing.T, gid string, want []string) {
	t.Helper()
	groups, err := loadSessionGroups()
	if err != nil {
		t.Fatalf("loadSessionGroups: %v", err)
	}
	for _, g := range groups {
		if g.ID != gid {
			continue
		}
		if len(g.SessionIDs) != len(want) {
			t.Fatalf("SessionIDs = %v, want %v", g.SessionIDs, want)
		}
		for i := range want {
			if g.SessionIDs[i] != want[i] {
				t.Fatalf("SessionIDs = %v, want %v", g.SessionIDs, want)
			}
		}
		return
	}
	t.Fatalf("group %s not found", gid)
}

func TestSessionGroups_Delete(t *testing.T) {
	srv := groupTestServer(t)
	_, out := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "Temp"))
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
		_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, name))
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
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "Work"))
	gid := o["group"].(map[string]any)["id"].(string)
	doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"name": "Work2"})

	// The pin survived the group writes.
	if pins, _ := loadPinnedSessions(); len(pins) != 1 || pins[0] != sid {
		t.Fatalf("pin lost after group edits, got %v", pins)
	}

	// Moving a session into the group must not wipe the pin either.
	fileInProject(t, gid, "other")
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

func TestSessionCollapse_CollapseListRestore(t *testing.T) {
	srv := groupTestServer(t)
	const sid = "20260101-000000-deadbeef"

	// Nothing collapsed yet.
	_, out := doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if col, ok := out["collapsed_session_ids"].([]any); !ok || len(col) != 0 {
		t.Fatalf("expected empty collapsed list, got %v", out["collapsed_session_ids"])
	}

	// Collapse it.
	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("collapse: status %d", rec.Code)
	}
	col, err := loadCollapsedSessions()
	if err != nil || len(col) != 1 || col[0] != sid {
		t.Fatalf("expected [%s], got %v (err %v)", sid, col, err)
	}

	// GET reflects it.
	_, out = doGroupReq(t, srv, http.MethodGet, "/api/session-groups", nil)
	if got := out["collapsed_session_ids"].([]any); len(got) != 1 || got[0].(string) != sid {
		t.Fatalf("GET collapsed list = %v", got)
	}

	// Collapsing again is idempotent (no duplicate).
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})
	if col, _ = loadCollapsedSessions(); len(col) != 1 {
		t.Fatalf("re-collapse duplicated: %v", col)
	}

	// Restore.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d", rec.Code)
	}
	if col, _ = loadCollapsedSessions(); len(col) != 0 {
		t.Fatalf("expected empty after restore, got %v", col)
	}
}

// A pinned session still can't be collapsed — the two states contradict (pin
// means always at the top, archive means out of sight). A session that
// belongs to a project can, and keeps its membership: archiving is meant to
// work inside a project too, and restoring one has to put it back exactly
// where it was.
func TestSessionCollapse_RejectsPinnedAllowsGrouped(t *testing.T) {
	srv := groupTestServer(t)
	const pinned = "sess-pinned"
	const grouped = "sess-grouped"

	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+pinned+"/pin", map[string]any{"pinned": true})
	rec, _ := doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+pinned+"/collapse", map[string]any{"collapsed": true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("collapse of pinned session: status %d, want 409", rec.Code)
	}

	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "Work"))
	gid := o["group"].(map[string]any)["id"].(string)
	fileInProject(t, gid, grouped)
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+grouped+"/collapse", map[string]any{"collapsed": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("collapse of grouped session: status %d, want 200", rec.Code)
	}
	col, _ := loadCollapsedSessions()
	if len(col) != 1 || col[0] != grouped {
		t.Fatalf("collapse did not land: %v", col)
	}
	if p := projectForSession(grouped); p == nil || p.ID != gid {
		t.Fatalf("session lost its project on archive: %+v", p)
	}

	// Restoring puts it back with no further write — membership was never
	// touched.
	rec, _ = doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+grouped+"/collapse", map[string]any{"collapsed": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: status %d", rec.Code)
	}
	if p := projectForSession(grouped); p == nil || p.ID != gid {
		t.Fatalf("session did not restore into its project: %+v", p)
	}
}

// Pinning or filing a collapsed session (possible from a racing stale tab)
// wins over the collapse: the session leaves the collapsed list.
func TestSessionCollapse_PinAndGroupDropCollapsed(t *testing.T) {
	srv := groupTestServer(t)
	const sid = "sess-1"

	// Collapse, then pin — the pin drops the collapsed entry.
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": true})
	if col, _ := loadCollapsedSessions(); len(col) != 0 {
		t.Fatalf("pin left session collapsed: %v", col)
	}
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/pin", map[string]any{"pinned": false})

	// Collapse, then move into a group — the move drops the collapsed entry.
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "Work"))
	gid := o["group"].(map[string]any)["id"].(string)
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})
	fileInProject(t, gid, sid)
	if col, _ := loadCollapsedSessions(); len(col) != 0 {
		t.Fatalf("group move left session collapsed: %v", col)
	}
	groups, _ := loadSessionGroups()
	if len(groups) != 1 || len(groups[0].SessionIDs) != 1 || groups[0].SessionIDs[0] != sid {
		t.Fatalf("group membership wrong: %+v", groups)
	}
}

// The collapsed list shares the registry with groups and pins; editing those
// must not clobber it.
func TestSessionCollapse_CoexistsWithGroupsAndPins(t *testing.T) {
	srv := groupTestServer(t)
	const sid = "sess-collapsed"

	doGroupReq(t, srv, http.MethodPut, "/api/sessions/"+sid+"/collapse", map[string]any{"collapsed": true})

	// Group create/rename/reorder and an unrelated pin cycle all leave it intact.
	_, o := doGroupReq(t, srv, http.MethodPost, "/api/session-groups", newGroupBody(t, "Work"))
	gid := o["group"].(map[string]any)["id"].(string)
	doGroupReq(t, srv, http.MethodPatch, "/api/session-groups/"+gid, map[string]any{"name": "Work2"})
	doGroupReq(t, srv, http.MethodPut, "/api/session-groups/order", map[string]any{"ids": []string{gid}})
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/other/pin", map[string]any{"pinned": true})
	doGroupReq(t, srv, http.MethodPut, "/api/sessions/other/pin", map[string]any{"pinned": false})

	if col, _ := loadCollapsedSessions(); len(col) != 1 || col[0] != sid {
		t.Fatalf("collapsed list lost across registry edits, got %v", col)
	}
}

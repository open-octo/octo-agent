package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

// seedDB creates a named database the way the sqlite tool does.
func seedDB(t *testing.T, name string) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE quote(id INTEGER PRIMARY KEY, symbol TEXT, price REAL)",
		"INSERT INTO quote(symbol, price) VALUES ('AAPL', 231.4), ('MSFT', 402)",
	} {
		if _, err := sqlitedb.Exec(context.Background(), name, sqlitedb.Create, q, nil, dbPageLimits); err != nil {
			t.Fatal(err)
		}
	}
}

type dbResponse struct {
	Columns      []string `json:"columns"`
	Rows         [][]any  `json:"rows"`
	Truncated    bool     `json:"truncated"`
	Changes      int64    `json:"changes"`
	LastInsertID int64    `json:"last_insert_id"`
	Error        string   `json:"error"`
}

func decodeDB(t *testing.T, w *httptest.ResponseRecorder) dbResponse {
	t.Helper()
	var out dbResponse
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode %q: %v", w.Body.String(), err)
	}
	return out
}

func dbBody(sql string, params ...any) string {
	b, _ := json.Marshal(map[string]any{"sql": sql, "params": params})
	return string(b)
}

func localPost(srv *Server, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	return w
}

func remotePost(srv *Server, target, body string, cookie bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "abc.ngrok-free.app"
	req.Header.Set("X-Forwarded-For", "203.0.113.5")
	if cookie {
		req.AddCookie(&http.Cookie{Name: accessKeyCookie, Value: srv.accessKey})
	}
	w := httptest.NewRecorder()
	srv.http.Handler.ServeHTTP(w, req)
	return w
}

func TestArtifactDB_ReadsAndWrites(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	seedDB(t, "prices")
	base := "/_artifacts/" + f.token(t) + "/__octo/db/"

	w := localPost(f.srv, base+"prices", dbBody("SELECT symbol, price FROM quote WHERE id = ?", 1))
	if w.Code != http.StatusOK {
		t.Fatalf("select: %d %s", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q", got)
	}
	res := decodeDB(t, w)
	if strings.Join(res.Columns, ",") != "symbol,price" || len(res.Rows) != 1 || res.Rows[0][0] != "AAPL" || res.Truncated {
		t.Errorf("select = %+v", res)
	}

	w = localPost(f.srv, base+"prices", dbBody("UPDATE quote SET price = ? WHERE symbol = ?", 240, "AAPL"))
	if res := decodeDB(t, w); w.Code != http.StatusOK || res.Changes != 1 {
		t.Errorf("update: %d %+v", w.Code, res)
	}

	for name, tc := range map[string]struct {
		target, body string
		want         int
	}{
		"missing db":     {base + "nope", dbBody("SELECT 1"), http.StatusNotFound},
		"bad name":       {base + "Bad.Name", dbBody("SELECT 1"), http.StatusBadRequest},
		"two statements": {base + "prices", dbBody("SELECT 1; DELETE FROM quote"), http.StatusBadRequest},
		"sql error":      {base + "prices", dbBody("SELECT FROM"), http.StatusBadRequest},
		"attach":         {base + "prices", dbBody("ATTACH DATABASE 'x.db' AS x"), http.StatusForbidden},
		"ddl":            {base + "prices", dbBody("DROP TABLE quote"), http.StatusForbidden},
		"bad json":       {base + "prices", "{", http.StatusBadRequest},
		"unknown token":  {"/_artifacts/" + strings.Repeat("0", 32) + "/__octo/db/prices", dbBody("SELECT 1"), http.StatusNotFound},
	} {
		if w := localPost(f.srv, tc.target, tc.body); w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, w.Code, tc.want, w.Body.String())
		}
	}
	if path, _ := sqlitedb.Path("nope"); fileExists(path) {
		t.Error("a page request created a database")
	}
	if res := decodeDB(t, localPost(f.srv, base+"prices", dbBody("SELECT count(*) FROM quote"))); res.Rows[0][0] != float64(2) {
		t.Errorf("rows after refused multi-statement = %v, want 2", res.Rows[0][0])
	}
}

func TestArtifactDB_RequiresAuthRemotely(t *testing.T) {
	f := newPageFixture(t, "<h1>hi</h1>")
	seedDB(t, "prices")
	target := "/_artifacts/" + f.token(t) + "/__octo/db/prices"
	if w := remotePost(f.srv, target, dbBody("SELECT 1"), false); w.Code != http.StatusUnauthorized {
		t.Errorf("keyless remote: status = %d, want 401", w.Code)
	}
	if w := remotePost(f.srv, target, dbBody("SELECT 1"), true); w.Code != http.StatusOK {
		t.Errorf("remote with cookie: status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
}

func TestLightAppDB_Private(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	seedDB(t, "prices")

	if w := remotePost(srv, "/_apps/demo/__octo/db/prices", dbBody("SELECT 1"), false); w.Code != http.StatusUnauthorized {
		t.Errorf("keyless remote: status = %d, want 401", w.Code)
	}
	w := remotePost(srv, "/_apps/demo/__octo/db/prices", dbBody("INSERT INTO quote(symbol, price) VALUES (?, ?)", "NVDA", 120.5), true)
	if res := decodeDB(t, w); w.Code != http.StatusOK || res.Changes != 1 || res.LastInsertID != 3 {
		t.Errorf("remote with cookie insert: %d %+v", w.Code, res)
	}
	// A database the manifest does not list is fine while the app is private.
	seedDB(t, "other")
	if w := localPost(srv, "/_apps/demo/__octo/db/other", dbBody("SELECT count(*) FROM quote")); w.Code != http.StatusOK {
		t.Errorf("undeclared db on a private app: status = %d", w.Code)
	}
	if w := localPost(srv, "/_apps/missing/__octo/db/prices", dbBody("SELECT 1")); w.Code != http.StatusNotFound {
		t.Errorf("app not installed: status = %d, want 404", w.Code)
	}
	// GET on the reserved path is just a file lookup.
	if w := localGet(srv, "/_apps/demo/__octo/db/prices"); w.Code != http.StatusNotFound {
		t.Errorf("GET: status = %d, want 404", w.Code)
	}
}

func TestLightAppDB_PublicReadsDeclaredOnly(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	seedDB(t, "prices")
	seedDB(t, "secret")
	manifest := filepath.Join(lightAppsDir(), "demo", "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"slug":"demo","name":"Demo","databases":["prices"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := setPublic(t, srv, true); w.Code != http.StatusOK {
		t.Fatalf("PUT public: %d", w.Code)
	}

	w := remotePost(srv, "/_apps/demo/__octo/db/prices", dbBody("SELECT symbol FROM quote ORDER BY id"), false)
	if res := decodeDB(t, w); w.Code != http.StatusOK || len(res.Rows) != 2 {
		t.Errorf("keyless read of a declared db: %d %+v", w.Code, res)
	}
	for name, tc := range map[string]struct {
		target, body string
		cookie       bool
		want         int
	}{
		"undeclared db":     {"/_apps/demo/__octo/db/secret", dbBody("SELECT * FROM quote"), false, http.StatusForbidden},
		"write":             {"/_apps/demo/__octo/db/prices", dbBody("DELETE FROM quote"), false, http.StatusForbidden},
		"write as owner":    {"/_apps/demo/__octo/db/prices", dbBody("DROP TABLE quote"), true, http.StatusForbidden},
		"attach":            {"/_apps/demo/__octo/db/prices", dbBody("ATTACH DATABASE 'x.db' AS x"), false, http.StatusForbidden},
		"write via the cte": {"/_apps/demo/__octo/db/prices", dbBody("WITH x AS (SELECT 1) DELETE FROM quote"), false, http.StatusForbidden},
	} {
		if w := remotePost(srv, tc.target, tc.body, tc.cookie); w.Code != tc.want {
			t.Errorf("%s: status = %d, want %d (%s)", name, w.Code, tc.want, w.Body.String())
		}
	}
	res := decodeDB(t, remotePost(srv, "/_apps/demo/__octo/db/prices", dbBody("SELECT count(*) FROM quote"), false))
	if len(res.Rows) != 1 || res.Rows[0][0] != float64(2) {
		t.Errorf("rows after refused writes = %+v, want 2", res)
	}
}

// A cross-site page cannot drive a private app's database through the
// user's browser: a foreign Origin loses the loopback exemption.
func TestLightAppDB_CrossSiteRefused(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	seedDB(t, "prices")
	req := httptest.NewRequest(http.MethodPost, "/_apps/demo/__octo/db/prices", strings.NewReader(dbBody("DELETE FROM quote")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	serveLoopback(srv.http.Handler, w, req)
	if w.Code == http.StatusOK {
		t.Fatalf("cross-site POST: status 200 (%s)", w.Body.String())
	}
	if res := decodeDB(t, localPost(srv, "/_apps/demo/__octo/db/prices", dbBody("SELECT count(*) FROM quote"))); res.Rows[0][0] != float64(2) {
		t.Errorf("rows after cross-site POST = %v, want 2", res.Rows[0][0])
	}
}

// What an anonymous caller can make a public app's endpoint do is bounded:
// an endless query stops at the row cap, a huge value is refused, and a
// PRAGMA — some set process-wide state — is not a query.
func TestLightAppDB_PublicIsBounded(t *testing.T) {
	srv := newLightAppFixture(t, Config{Addr: "127.0.0.1:0", Tools: false}, "<h1>demo</h1>")
	seedDB(t, "prices")
	manifest := filepath.Join(lightAppsDir(), "demo", "manifest.json")
	if err := os.WriteFile(manifest, []byte(`{"slug":"demo","name":"Demo","databases":["prices"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if w := setPublic(t, srv, true); w.Code != http.StatusOK {
		t.Fatalf("PUT public: %d", w.Code)
	}
	post := func(sql string) *httptest.ResponseRecorder {
		return remotePost(srv, "/_apps/demo/__octo/db/prices", dbBody(sql), false)
	}
	w := post("WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c) SELECT x FROM c")
	if res := decodeDB(t, w); w.Code != http.StatusOK || len(res.Rows) != dbPageLimits.MaxRows || !res.Truncated {
		t.Errorf("endless query: %d rows=%d truncated=%v", w.Code, len(res.Rows), res.Truncated)
	}
	if w := post("SELECT zeroblob(100000000)"); w.Code != http.StatusBadRequest {
		t.Errorf("100 MB value: status = %d, want 400", w.Code)
	}
	if w := post("PRAGMA soft_heap_limit = 1"); w.Code != http.StatusForbidden {
		t.Errorf("PRAGMA: status = %d, want 403 (%s)", w.Code, w.Body.String())
	}
}

// databases is agent-written; a wrong shape must not hide the app.
func TestLightAppManifest_LooseDatabases(t *testing.T) {
	for raw, want := range map[string]string{
		`{"databases":["a","b"]}`: "a,b",
		`{"databases":"a"}`:       "a",
		`{"databases":{"x":1}}`:   "",
		`{"databases":[1,2]}`:     "",
	} {
		var m lightAppManifest
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			t.Errorf("%s: %v", raw, err)
			continue
		}
		if got := strings.Join(m.Databases, ","); got != want {
			t.Errorf("%s: databases = %q, want %q", raw, got, want)
		}
	}
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

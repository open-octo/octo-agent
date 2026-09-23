package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/open-octo/octo-agent/internal/sqlitedb"
)

// ─── POST <page>/__octo/db/{name} ───────────────────────────────────────────
//
// A page — a session artifact or a Light App — queries a named database under
// ~/.octo/databases/ by a path relative to itself, `./__octo/db/<name>`, so
// the same page code works in the artifacts panel and after it is saved as an
// app. A non-public page reads and writes; a public app reads only, and only
// the databases its manifest lists. A page never creates a database: that is
// the sqlite tool's job. See dev-docs/named-databases-design.md.

const dbRequestMaxBytes = 1 << 20

// dbPageLimits bounds one page query. A public app's queries come from anyone
// with the link, and the HTTP server has no write timeout (WebSockets), so
// these are what stop one request from spinning a core or filling memory.
var dbPageLimits = sqlitedb.Limits{
	MaxRows:     10000,
	MaxBytes:    16 << 20,
	MaxValueLen: 4 << 20,
	Timeout:     10 * time.Second,
}

// publicDBSlots caps public queries in flight across every app, so a burst of
// anonymous requests cannot take every core.
var publicDBSlots = make(chan struct{}, 4)

func (s *Server) handleArtifactDB(w http.ResponseWriter, r *http.Request) {
	if s.lookupGrant(r.PathValue("token")) == nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	serveDBQuery(w, r, sqlitedb.ReadWrite)
}

func (s *Server) handleLightAppDB(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !safeLightAppSlug(slug) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if m, ok := readLightAppManifest(slug); ok && m.Public {
		// Read-only even for a signed-in owner, so the page's behaviour does
		// not depend on who is looking at it.
		if !slices.Contains(m.Databases, r.PathValue("name")) {
			writeError(w, http.StatusForbidden, "database_not_declared")
			return
		}
		select {
		case publicDBSlots <- struct{}{}:
			defer func() { <-publicDBSlots }()
		default:
			writeError(w, http.StatusServiceUnavailable, "database_busy")
			return
		}
		serveDBQuery(w, r, sqlitedb.ReadOnly)
		return
	}
	s.requireAuth(func(w http.ResponseWriter, r *http.Request) {
		if fi, err := os.Stat(filepath.Join(lightAppsDir(), slug, "index.html")); err != nil || fi.IsDir() {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		serveDBQuery(w, r, sqlitedb.ReadWrite)
	})(w, r)
}

func readLightAppManifest(slug string) (lightAppManifest, bool) {
	var m lightAppManifest
	data, err := os.ReadFile(filepath.Join(lightAppsDir(), slug, "manifest.json"))
	if err != nil || json.Unmarshal(data, &m) != nil {
		return m, false
	}
	return m, true
}

func serveDBQuery(w http.ResponseWriter, r *http.Request, mode sqlitedb.Mode) {
	name := r.PathValue("name")
	if !sqlitedb.ValidName(name) {
		writeError(w, http.StatusBadRequest, "invalid_database_name")
		return
	}
	var req struct {
		SQL    string `json:"sql"`
		Params []any  `json:"params"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, dbRequestMaxBytes))
	// Numbers stay json.Number so a large integer id binds exactly.
	dec.UseNumber()
	if err := dec.Decode(&req); err != nil {
		writeInvalidJSONBody(w, err)
		return
	}
	res, err := sqlitedb.Exec(r.Context(), name, mode, req.SQL, req.Params, dbPageLimits)
	setPageHeaders(w.Header())
	switch {
	case err == nil:
	case errors.Is(err, sqlitedb.ErrNotFound):
		writeError(w, http.StatusNotFound, "database_not_found")
		return
	case errors.Is(err, sqlitedb.ErrReadOnly), errors.Is(err, sqlitedb.ErrNotAllowed):
		writeError(w, http.StatusForbidden, err.Error())
		return
	case errors.Is(err, sqlitedb.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "database_busy")
		return
	case errors.Is(err, sqlitedb.ErrTimeout):
		writeError(w, http.StatusBadRequest, "query_timeout")
		return
	default:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if res.HasRows {
		writeJSON(w, http.StatusOK, map[string]any{"columns": res.Columns, "rows": res.Rows, "truncated": res.Truncated})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changes": res.Changes, "last_insert_id": res.LastInsertID})
}

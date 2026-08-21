package server

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func seedLightApp(t *testing.T, home, slug, name, desc string) {
	t.Helper()
	dir := filepath.Join(home, ".octo", "light-apps", slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := lightAppManifest{Slug: slug, Name: name, Description: desc, Icon: "📦", CreatedAt: "2026-07-26T10:00:00Z"}
	b, _ := json.Marshal(m)
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>ok</html>"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestListLightApps_EmptyDir: listing with no light apps returns an empty array.
func TestListLightApps_EmptyDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/light-apps", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out struct {
		Apps []lightAppManifest
		Dir  string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Apps) != 0 {
		t.Errorf("expected 0 apps, got %d", len(out.Apps))
	}
	if want := filepath.Join(home, ".octo", "light-apps"); out.Dir != want {
		t.Errorf("expected dir %q, got %q", want, out.Dir)
	}
}

// TestListLightApps_WithApps: validates listing and field round-trip.
func TestListLightApps_WithApps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	seedLightApp(t, home, "csv-tool", "CSV Tool", "Reconcile CSVs")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/light-apps", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out struct {
		Apps []lightAppManifest
		Dir  string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Apps) != 1 {
		t.Fatalf("expected 1 app, got %d", len(out.Apps))
	}
	if out.Apps[0].Name != "CSV Tool" || out.Apps[0].Slug != "csv-tool" {
		t.Errorf("unexpected app: %+v", out.Apps[0])
	}
	if want := filepath.Join(home, ".octo", "light-apps"); out.Dir != want {
		t.Errorf("expected dir %q, got %q", want, out.Dir)
	}
}

// TestGetLightApp_Success: validates manifest + HTML round-trip.
func TestGetLightApp_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	seedLightApp(t, home, "csv-tool", "CSV Tool", "Reconcile CSVs")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "GET", "/api/light-apps/csv-tool", "")
	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	var out struct {
		Manifest lightAppManifest `json:"manifest"`
		HTML     string           `json:"html"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Manifest.Name != "CSV Tool" {
		t.Errorf("wrong name: %q", out.Manifest.Name)
	}
	if out.HTML != "<html>ok</html>" {
		t.Errorf("wrong html: %q", out.HTML)
	}
}

// TestGetLightApp_PathTraversal: rejects slash and backslash in slugs.
// Note: ".." is stripped by net/http path normalization before routing,
// and "/" in a slug causes a 404 from the router (Go 1.22+ ServeMux wildcard
// {slug} does not capture segments containing "/"). The handler-level check
// is defense-in-depth for slugs arriving through non-URL channels.
func TestGetLightApp_PathTraversal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	// Backslash is not a URL path separator on any platform — it reaches the
	// handler and must be rejected.
	w := doJSON(t, srv, "GET", "/api/light-apps/a\\b", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("backslash slug: expected 400, got %d (body: %s)", w.Code, w.Body.String())
	}
}

// TestCreateLightApp_SourcePathRoundTrip: source_path persists into the
// manifest and comes back through the create response and the listing — the
// web UI relies on it to hide "Save to Light App" for already-saved artifacts.
func TestCreateLightApp_SourcePathRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	body := `{"name":"Quicksort Viz","html":"<html>ok</html>","source_path":"/work/quicksort-visualizer.html"}`
	w := doJSON(t, srv, "POST", "/api/light-apps", body)
	if w.Code != 201 {
		t.Fatalf("create: expected 201, got %d (body: %s)", w.Code, w.Body.String())
	}
	var created lightAppManifest
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.SourcePath != "/work/quicksort-visualizer.html" {
		t.Errorf("create response source_path: got %q", created.SourcePath)
	}

	w = doJSON(t, srv, "GET", "/api/light-apps", "")
	if w.Code != 200 {
		t.Fatalf("list: expected 200, got %d", w.Code)
	}
	var out struct{ Apps []lightAppManifest }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Apps) != 1 || out.Apps[0].SourcePath != "/work/quicksort-visualizer.html" {
		t.Errorf("listing source_path round-trip failed: %+v", out.Apps)
	}
}

// TestDeleteLightApp_Success: deletes an app and confirms it's gone from listing.
func TestDeleteLightApp_Success(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	seedLightApp(t, home, "csv-tool", "CSV Tool", "Reconcile CSVs")
	srv := mustServer(t, Config{Addr: "127.0.0.1:0", Tools: false})

	w := doJSON(t, srv, "DELETE", "/api/light-apps/csv-tool", "")
	if w.Code != 200 {
		t.Fatalf("DELETE: expected 200, got %d", w.Code)
	}
	// Confirm it's gone from listing.
	w = doJSON(t, srv, "GET", "/api/light-apps", "")
	if w.Code != 200 {
		t.Fatalf("list after delete: expected 200, got %d", w.Code)
	}
	var out struct{ Apps []lightAppManifest }
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Apps) != 0 {
		t.Errorf("expected 0 apps after delete, got %d", len(out.Apps))
	}
}

package server

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/open-octo/octo-agent/internal/datahome"
	"github.com/open-octo/octo-agent/internal/profiles"
)

// The server under test runs under profile "work"; the fixture adds an idle
// "old" root and a "busy" root whose serve.pid is our own (alive) pid.
func seedProfiles(t *testing.T) string {
	t.Helper()
	home := setTestHome(t)
	t.Setenv(datahome.ProfileEnv, "work")
	// Keep the default root's liveness probe off the developer's real 8088.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	prev := profiles.DefaultAddr
	profiles.DefaultAddr = ln.Addr().String()
	ln.Close()
	t.Cleanup(func() { profiles.DefaultAddr = prev })
	for _, d := range []string{".octo", ".octo-work", ".octo-old", ".octo-busy"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	pid := strconv.Itoa(os.Getpid()) + "\n"
	if err := os.WriteFile(filepath.Join(home, ".octo-busy", "serve.pid"), []byte(pid), 0o644); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestProfiles_ListMarksCurrentAndRunning(t *testing.T) {
	seedProfiles(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodGet, "/api/profiles", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/profiles = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Current  string `json:"current"`
		Profiles []struct {
			Name    string `json:"name"`
			Current bool   `json:"current"`
			Running bool   `json:"running"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Current != "work" {
		t.Errorf("current = %q, want work", resp.Current)
	}
	got := map[string][2]bool{}
	for _, p := range resp.Profiles {
		got[p.Name] = [2]bool{p.Current, p.Running}
	}
	if len(got) != 4 || got["work"] != [2]bool{true, false} || got["busy"] != [2]bool{false, true} || got["old"] != [2]bool{false, false} {
		t.Errorf("profiles = %+v", got)
	}
}

func TestProfiles_CreateAndConflicts(t *testing.T) {
	home := seedProfiles(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	w := doJSON(t, srv, http.MethodPost, "/api/profiles", `{"name": " lab "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /api/profiles = %d: %s", w.Code, w.Body.String())
	}
	if st, err := os.Stat(filepath.Join(home, ".octo-lab")); err != nil || !st.IsDir() {
		t.Fatalf("root not created: %v", err)
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/profiles", `{"name": "lab"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate = %d, want 409", w.Code)
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/profiles", `{"name": "bad name"}`); w.Code != http.StatusBadRequest {
		t.Errorf("invalid = %d, want 400", w.Code)
	}
	if w := doJSON(t, srv, http.MethodPost, "/api/profiles", `{"name": ""}`); w.Code != http.StatusBadRequest {
		t.Errorf("empty = %d, want 400", w.Code)
	}
}

func TestProfiles_DeleteGuardsAndSuccess(t *testing.T) {
	home := seedProfiles(t)
	srv := mustServer(t, Config{Addr: "127.0.0.1:0"})

	cases := map[string]int{
		"work":     http.StatusConflict, // the profile this server runs under
		"busy":     http.StatusConflict, // live backend
		"missing":  http.StatusNotFound,
		"bad%20nm": http.StatusBadRequest,
	}
	for name, want := range cases {
		if w := doJSON(t, srv, http.MethodDelete, "/api/profiles/"+name, ""); w.Code != want {
			t.Errorf("DELETE %s = %d, want %d: %s", name, w.Code, want, w.Body.String())
		}
	}
	for _, d := range []string{".octo", ".octo-work", ".octo-busy"} {
		if _, err := os.Stat(filepath.Join(home, d)); err != nil {
			t.Errorf("%s removed by a refused DELETE: %v", d, err)
		}
	}
	if w := doJSON(t, srv, http.MethodDelete, "/api/profiles/old", ""); w.Code != http.StatusOK {
		t.Fatalf("DELETE old = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".octo-old")); !os.IsNotExist(err) {
		t.Errorf("old root still present: %v", err)
	}
}

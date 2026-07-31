package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestMirrorURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"https://api.github.com/repos/" + releaseRepo + "/releases/latest",
			"https://dl.octo-agent.dev/api/releases/latest"},
		{"https://github.com/" + releaseRepo + "/releases/download/v1.2.3/Octo-darwin-universal.zip",
			"https://dl.octo-agent.dev/releases/download/v1.2.3/Octo-darwin-universal.zip"},
		// Other repositories must not be routed through the mirror.
		{"https://api.github.com/repos/someone/else/releases/latest", ""},
		{"https://github.com/someone/else/releases/download/v1/a.zip", ""},
		// Presigned asset hosts have no derivable mirror path.
		{"https://objects.githubusercontent.com/github-production-release-asset/abc", ""},
		// Non-release GitHub paths stay direct.
		{"https://github.com/" + releaseRepo + "/issues", ""},
	}
	for _, c := range cases {
		u, err := url.Parse(c.in)
		if err != nil {
			t.Fatal(err)
		}
		if got := mirrorURL(u); got != c.want {
			t.Errorf("mirrorURL(%s) = %q, want %q", c.in, got, c.want)
		}
	}
}

// failGitHub fails transport-level for GitHub hosts and passes everything
// else to the default transport (the httptest mirror on 127.0.0.1).
type failGitHub struct{ failed *[]string }

func (f failGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	switch req.Host {
	case "api.github.com", "github.com":
		*f.failed = append(*f.failed, req.URL.String())
		return nil, errors.New("connection reset by peer")
	}
	return http.DefaultTransport.RoundTrip(req)
}

func TestMirrorTransportRetriesOnTransportError(t *testing.T) {
	var gotAuth string
	mirror := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/releases/latest" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, `{"tag_name":"v9.9.9"}`)
	}))
	defer mirror.Close()

	origBase := mirrorBase
	mirrorBase = mirror.URL
	defer func() { mirrorBase = origBase }()

	var failed []string
	client := &http.Client{Transport: mirrorTransport{base: failGitHub{failed: &failed}}}

	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+releaseRepo+"/releases/latest", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected mirror to answer, got %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != `{"tag_name":"v9.9.9"}` {
		t.Errorf("unexpected body %q", body)
	}
	if len(failed) != 1 {
		t.Errorf("expected exactly one direct attempt, got %v", failed)
	}
	if gotAuth != "" {
		t.Errorf("Authorization header must not reach the mirror, got %q", gotAuth)
	}
}

func TestMirrorTransportDoesNotRetryUnmappedHosts(t *testing.T) {
	var failed []string
	client := &http.Client{Transport: mirrorTransport{base: failGitHub{failed: &failed}}}
	_, err := client.Get("https://github.com/someone/else/releases/download/v1/a.zip")
	if err == nil {
		t.Fatal("expected the direct failure to propagate")
	}
	if len(failed) != 1 {
		t.Errorf("expected one attempt, got %v", failed)
	}
}

func TestMirrorTransportPassesThroughHTTPErrors(t *testing.T) {
	// An HTTP status from GitHub is a real answer — no mirror retry.
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer direct.Close()

	client := &http.Client{Transport: mirrorTransport{base: http.DefaultTransport}}
	resp, err := client.Get(direct.URL + "/repos/" + releaseRepo + "/releases/latest")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 passthrough, got %d", resp.StatusCode)
	}
}

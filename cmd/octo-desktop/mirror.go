// Mirror fallback for the in-place updater's HTTP traffic: when a GitHub
// request fails at the transport level (github.com is blocked on some
// networks, notably mainland China), retry the equivalent URL on the
// project's download mirror (infra/dl-worker). Only connection-level
// failures are retried — an HTTP error status is a real answer from GitHub
// and passes through untouched, so the mirror never masks one.
package main

import (
	"net/http"
	"net/url"
	"strings"
)

// mirrorBase is the download mirror origin. A var so tests can point it at
// httptest. Keep in sync with internal/upgrade's MirrorBaseURLs and
// infra/dl-worker.
var mirrorBase = "https://dl.octo-agent.dev"

// mirrorURL maps a GitHub URL the updater uses onto its mirror equivalent,
// or "" when there is no mapping — any other repository, and presigned
// objects.githubusercontent.com URLs, whose paths cannot be derived.
func mirrorURL(u *url.URL) string {
	repoPath := "/" + releaseRepo
	switch u.Host {
	case "api.github.com":
		if rest, ok := strings.CutPrefix(u.Path, "/repos"+repoPath+"/"); ok {
			return mirrorBase + "/api/" + rest
		}
	case "github.com":
		if rest, ok := strings.CutPrefix(u.Path, repoPath+"/releases/download/"); ok {
			return mirrorBase + "/releases/download/" + rest
		}
	}
	return ""
}

// mirrorTransport wraps a RoundTripper with the mirror retry.
type mirrorTransport struct {
	base http.RoundTripper
}

func (t mirrorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err == nil || req.Context().Err() != nil {
		return resp, err
	}
	var m string
	if req.Method == http.MethodGet || req.Method == http.MethodHead {
		m = mirrorURL(req.URL)
	}
	if m == "" {
		return resp, err
	}
	mu, perr := url.Parse(m)
	if perr != nil {
		return resp, err
	}
	mreq := req.Clone(req.Context())
	mreq.URL = mu
	mreq.Host = ""
	// Never forward GitHub credentials to the mirror.
	mreq.Header.Del("Authorization")
	mresp, merr := t.base.RoundTrip(mreq)
	if merr != nil {
		return resp, err // the direct failure is the more truthful error
	}
	return mresp, nil
}

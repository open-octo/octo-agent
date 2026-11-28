package tools

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// stubPublicTarget makes every URL look like a public target for the duration
// of a test. httptest servers bind 127.0.0.1, which publicFetchTarget rejects
// by design — the real guard is exercised separately.
func stubPublicTarget(t *testing.T) {
	t.Helper()
	old := thirdPartyFetchAllowed
	thirdPartyFetchAllowed = func(context.Context, *url.URL) bool { return true }
	t.Cleanup(func() { thirdPartyFetchAllowed = old })
}

// fakeDirectFetchServer returns 403 to the direct GET.
func forbiddenTarget(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no bots", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestWebFetch_DeepSeekFallbackOnForbidden: the direct GET hits an anti-bot
// 403, the DeepSeek Fetch API answers with Markdown, and the result carries the
// provenance note plus the content.
func TestWebFetch_DeepSeekFallbackOnForbidden(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dk")
	stubPublicTarget(t)
	target := forbiddenTarget(t)

	var gotAuth, gotURL string
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var req struct {
			URL string `json:"url"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		gotURL = req.URL
		_, _ = w.Write([]byte(`{"title":"Example article","content":"# Example article\n\nArticle content."}`))
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	out, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{"url": target.URL})
	if err != nil {
		t.Fatalf("Execute should succeed via fallback: %v", err)
	}
	if gotAuth != "Bearer dk" {
		t.Errorf("auth header = %q, want 'Bearer dk'", gotAuth)
	}
	if gotURL != target.URL {
		t.Errorf("forwarded url = %q, want %q", gotURL, target.URL)
	}
	if !strings.Contains(out.Text, "DeepSeek Fetch API") || !strings.Contains(out.Text, "403") {
		t.Errorf("result should record the provenance and the failed status; got:\n%s", out.Text)
	}
	if !strings.Contains(out.Text, "Article content.") {
		t.Errorf("result should carry the fetched content; got:\n%s", out.Text)
	}
	if ui, ok := out.UI.(map[string]any); !ok || ui["url"] != target.URL {
		t.Errorf("UI payload = %+v, want the fetched url", out.UI)
	}
}

// TestWebFetch_NoFallbackWithoutKey: without DEEPSEEK_API_KEY the original
// failure surfaces untouched and no third-party request is made.
func TestWebFetch_NoFallbackWithoutKey(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "")
	stubPublicTarget(t)
	target := forbiddenTarget(t)

	hits := 0
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	_, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{"url": target.URL})
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want the direct 403 error, got %v", err)
	}
	if hits != 0 {
		t.Errorf("DeepSeek must not be called without a key; got %d hits", hits)
	}
}

// TestWebFetch_NoFallbackForPrivateTargets: a loopback or private-LAN URL must
// never reach the third-party service, even though the direct path allows it.
func TestWebFetch_NoFallbackForPrivateTargets(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dk")
	target := forbiddenTarget(t) // 127.0.0.1

	hits := 0
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	if _, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{"url": target.URL}); err == nil {
		t.Fatal("want the direct error, got success")
	}
	if hits != 0 {
		t.Errorf("a loopback URL must not be sent to DeepSeek; got %d hits", hits)
	}
}

// TestWebFetch_NoFallbackWhenRawRequested: clean=false asks for the raw body;
// a Markdown extraction is the wrong answer, so the failure stands.
func TestWebFetch_NoFallbackWhenRawRequested(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dk")
	stubPublicTarget(t)
	target := forbiddenTarget(t)

	hits := 0
	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	_, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{
		"url": target.URL, "clean": false,
	})
	if err == nil {
		t.Fatal("want the direct error, got success")
	}
	if hits != 0 {
		t.Errorf("clean=false must not trigger the fallback; got %d hits", hits)
	}
}

// TestWebFetch_FallbackFailureNamesBothErrors: when DeepSeek also fails, the
// caller learns about both causes instead of only the original one.
func TestWebFetch_FallbackFailureNamesBothErrors(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dk")
	stubPublicTarget(t)
	target := forbiddenTarget(t)

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"insufficient balance"}}`, http.StatusPaymentRequired)
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	_, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{"url": target.URL})
	if err == nil {
		t.Fatal("want an error")
	}
	if !strings.Contains(err.Error(), "403") || !strings.Contains(err.Error(), "402") {
		t.Errorf("error should name both failures; got %v", err)
	}
}

// TestWebFetch_FallbackEmptyContentIsAnError: a page the service could not
// extract must not be reported as a successful empty fetch.
func TestWebFetch_FallbackEmptyContentIsAnError(t *testing.T) {
	t.Setenv("DEEPSEEK_API_KEY", "dk")
	stubPublicTarget(t)
	target := forbiddenTarget(t)

	ds := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"title":"","content":"   "}`))
	}))
	defer ds.Close()
	swapEndpoint(t, &deepseekFetchEndpoint, ds.URL)

	_, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{"url": target.URL})
	if err == nil || !strings.Contains(err.Error(), "no extractable content") {
		t.Fatalf("want a no-content error, got %v", err)
	}
}

// TestPublicFetchTarget covers the guard's decisions without any network use
// for the literal cases.
func TestPublicFetchTarget(t *testing.T) {
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1", false},
		{"0.0.0.0", false},
		{"10.1.2.3", false},
		{"192.168.1.10", false},
		{"169.254.169.254", false}, // cloud metadata
		{"8.8.8.8", true},
		{"", false},
	}
	for _, c := range cases {
		u := &url.URL{Host: c.host}
		if c.host != "" {
			u.Host = net.JoinHostPort(c.host, "80")
		}
		if got := publicFetchTarget(context.Background(), u); got != c.want {
			t.Errorf("publicFetchTarget(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestPublicFetchTarget_LocalhostName pins the name-based path: "localhost"
// resolves to a loopback address, so it must be refused.
func TestPublicFetchTarget_LocalhostName(t *testing.T) {
	if publicFetchTarget(context.Background(), &url.URL{Host: "localhost:8080"}) {
		t.Error("localhost must not be a public fetch target")
	}
}

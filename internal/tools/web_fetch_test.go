package tools

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestWebFetch_CustomHeadersPassedThrough verifies that supplied referer /
// user_agent headers reach the destination server.
func TestWebFetch_CustomHeadersPassedThrough(t *testing.T) {
	var gotReferer, gotUA string
	hits := 0
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		gotReferer = r.Header.Get("Referer")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html><body>ok</body></html>"))
	}))
	defer target.Close()

	if _, err := (WebFetchTool{}).Execute(context.Background(), "web_fetch", map[string]any{
		"url":        target.URL,
		"referer":    "https://ref.example/",
		"user_agent": "MyUA/1.0",
	}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if hits != 1 {
		t.Fatalf("target hit %d times, want 1", hits)
	}
	if gotReferer != "https://ref.example/" {
		t.Errorf("Referer = %q, want https://ref.example/", gotReferer)
	}
	if gotUA != "MyUA/1.0" {
		t.Errorf("User-Agent = %q, want MyUA/1.0", gotUA)
	}
}

// TestFetchDirect_DefaultSameOriginReferer pins the default direct-fetch
// headers: a same-origin Referer and the browser UA when the caller passes none.
func TestFetchDirect_DefaultSameOriginReferer(t *testing.T) {
	var gotReferer, gotUA string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("Referer")
		gotUA = r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>x</html>"))
	}))
	defer target.Close()

	if _, err := fetchDirect(context.Background(), target.URL, "", ""); err != nil {
		t.Fatalf("fetchDirect: %v", err)
	}
	if want := target.URL + "/"; gotReferer != want {
		t.Errorf("default Referer = %q, want same-origin %q", gotReferer, want)
	}
	if gotUA != defaultDirectUserAgent {
		t.Errorf("default User-Agent = %q, want the browser default", gotUA)
	}
}

// blockedFetchIP must reject the cloud metadata / link-local range (the
// high-value SSRF target) while still allowing loopback and private-LAN
// addresses, since fetching a local dev server is a legitimate use.
func TestBlockedFetchIP(t *testing.T) {
	blocked := []string{"169.254.169.254", "169.254.0.1", "fe80::1"}
	for _, s := range blocked {
		if !blockedFetchIP(net.ParseIP(s)) {
			t.Errorf("blockedFetchIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.20", "8.8.8.8"}
	for _, s := range allowed {
		if blockedFetchIP(net.ParseIP(s)) {
			t.Errorf("blockedFetchIP(%s) = true, want false", s)
		}
	}
}

// A non-text response (e.g. an image) must not be stringified into garbage:
// readBody returns a clean notice pointing at the right tool, with no raw bytes.
func TestReadBody_BinaryContentTypeGuarded(t *testing.T) {
	png := "\x89PNG\r\n\x1a\nBINARY-PIXEL-JUNK"
	res, err := readBody(strings.NewReader(png), "https://x.test/logo.png", "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(res.Text, "PNG") || strings.Contains(res.Text, "BINARY-PIXEL-JUNK") {
		t.Errorf("binary bytes must not leak into the result; got:\n%s", res.Text)
	}
	for _, want := range []string{"image/png", "read_file", ".png", "curl"} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("image notice should mention %q; got:\n%s", want, res.Text)
		}
	}
	if len(res.Blocks) != 0 {
		t.Errorf("guard must not emit content blocks (text-only notice); got %d", len(res.Blocks))
	}
}

func TestReadBody_TextContentTypePassesThrough(t *testing.T) {
	res, err := readBody(strings.NewReader("# hello world"), "https://x.test", "text/markdown; charset=utf-8")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "hello world") {
		t.Errorf("text content should pass through unchanged; got:\n%s", res.Text)
	}
}

func TestIsTextualContentType(t *testing.T) {
	textual := []string{"", "text/html", "text/plain; charset=utf-8", "application/json",
		"application/xhtml+xml", "image/svg+xml", "application/atom+xml", "application/x-ndjson"}
	for _, ct := range textual {
		if !isTextualContentType(ct) {
			t.Errorf("%q should be treated as textual", ct)
		}
	}
	binary := []string{"image/png", "image/jpeg", "application/pdf", "application/octet-stream",
		"audio/mpeg", "video/mp4", "font/woff2", "application/zip"}
	for _, ct := range binary {
		if isTextualContentType(ct) {
			t.Errorf("%q should NOT be treated as textual", ct)
		}
	}
}

func TestWebFetch_RequiresURL(t *testing.T) {
	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{})
	if err == nil {
		t.Fatal("missing url should error")
	}
}

func TestWebFetch_RejectsNonHTTPScheme(t *testing.T) {
	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": "ftp://example.com/file",
	})
	if err == nil || !strings.Contains(err.Error(), "only http/https") {
		t.Errorf("expected scheme rejection, got %v", err)
	}
}

func TestWebFetch_FetchesURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Hello\n\nthis is markdown"))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/article",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "Hello") || !strings.Contains(out.Text, "markdown") {
		t.Errorf("unexpected body: %q", out.Text)
	}
	ui, ok := out.UI.(map[string]any)
	if !ok || ui["type"] != "web_fetch" || ui["url"] != srv.URL+"/article" {
		t.Errorf("UI payload = %#v", out.UI)
	}
}

// TestWebFetch_FollowsRedirect verifies the direct client follows both
// same-host and cross-host redirects (URL shorteners, www-canonical hops,
// http→https moves are all normal for an arbitrary URL).
func TestWebFetch_FollowsRedirect(t *testing.T) {
	// Same-host redirect (path change only) is followed.
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, srv.URL+"/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("# arrived"))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/start",
	})
	if err != nil {
		t.Fatalf("same-host redirect should be followed: %v", err)
	}
	if !strings.Contains(out.Text, "arrived") {
		t.Errorf("expected to reach the same-host redirect target, got %q", out.Text)
	}

	// Cross-host redirect is followed too (URL shorteners are normal).
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cross-host arrived"))
	}))
	defer dest.Close()

	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL, http.StatusFound)
	}))
	defer entry.Close()

	out, err = WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": entry.URL,
	})
	if err != nil {
		t.Fatalf("cross-host redirect should be followed: %v", err)
	}
	if !strings.Contains(out.Text, "cross-host arrived") {
		t.Errorf("expected the cross-host redirect target, got %q", out.Text)
	}
}

func TestWebFetch_HTTPErrorWrapped(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream is sad", http.StatusBadGateway)
	}))
	defer srv.Close()

	_, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("expected HTTP 502 error, got %v", err)
	}
}

func TestWebFetch_Truncates(t *testing.T) {
	// Generate a body larger than WebFetchMaxBytes.
	big := strings.Repeat("a", WebFetchMaxBytes+1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/big",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Large responses are now spilled to a temp file; the preview should
	// mention truncation and provide a file path.
	if !strings.Contains(out.Text, "truncated") {
		t.Errorf("expected truncation marker, got tail: %q", out.Text[max(0, len(out.Text)-100):])
	}
	if !strings.Contains(out.Text, "Saved to:") {
		t.Errorf("expected spilled file path in preview, got: %q", out.Text)
	}
}

func TestWebFetch_SmallResponseNotSpilled(t *testing.T) {
	// A tiny body should be returned inline (old behaviour).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/small",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out.Text != "hello world" {
		t.Errorf("expected inline small response, got: %q", out.Text)
	}
}

// A medium page — bigger than the old 8 KB threshold but within the 64 KB
// inline budget — is returned inline, not spilled. The common "fetch a doc and
// read it" case shouldn't be forced into a second read_file round-trip.
func TestWebFetch_MediumResponseInline(t *testing.T) {
	body := strings.Repeat("line of content\n", 2000) // ~32 KB
	if len(body) > WebFetchInlineBytes {
		t.Fatalf("test body %d must stay under the inline cap %d", len(body), WebFetchInlineBytes)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/medium",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if strings.Contains(out.Text, "Saved to:") {
		t.Errorf("a %d-byte response (≤ %d) should return inline, not spill", len(body), WebFetchInlineBytes)
	}
	if out.Text != body {
		t.Errorf("inline response should be the body verbatim; got %d bytes, want %d", len(out.Text), len(body))
	}
}

func TestMarkdownOutline(t *testing.T) {
	src := []string{
		"# Title",
		"intro text",
		"## Section A",
		"body",
		"### Sub A.1",
		"```",
		"## NotAHeading (inside fence)",
		"```",
		"## Section B",
		"#nospace is not a heading",
		"####### too many hashes",
	}
	got := markdownOutline(src, 50)
	for _, want := range []string{"Title", "Section A", "Sub A.1", "Section B"} {
		if !strings.Contains(got, want) {
			t.Errorf("outline missing %q; got:\n%s", want, got)
		}
	}
	if strings.Contains(got, "NotAHeading") {
		t.Errorf("headings inside code fences must be ignored; got:\n%s", got)
	}
	if strings.Contains(got, "nospace") || strings.Contains(got, "too many hashes") {
		t.Errorf("non-ATX lines must not count as headings; got:\n%s", got)
	}
	// Indentation reflects level: "## Section A" sits one indent below "# Title".
	if !strings.Contains(got, "\n  Section A\n") {
		t.Errorf("level-2 heading should be indented two spaces; got:\n%s", got)
	}

	// No headings (e.g. raw HTML / plain text) → empty outline.
	if out := markdownOutline([]string{"<html>", "plain text", "more"}, 50); out != "" {
		t.Errorf("no-heading input should yield empty outline, got %q", out)
	}

	// Cap respected with a "+N more" tail.
	many := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		many = append(many, "# H")
	}
	if out := markdownOutline(many, 3); !strings.Contains(out, "+7 more") {
		t.Errorf("expected '+7 more' when capped at 3 of 10; got:\n%s", out)
	}
}

// A spilled markdown page surfaces a heading outline in its preview.
func TestWebFetch_SpillIncludesOutline(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("# Main Title\n")
	for i := 0; i < 3000; i++ { // push well past the 64 KB inline cap
		sb.WriteString("filler line of body content\n")
		if i == 1000 {
			sb.WriteString("## Halfway Section\n")
		}
	}
	body := sb.String()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/doc",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	for _, want := range []string{"outline (headings)", "Main Title", "Halfway Section", "Saved to:"} {
		if !strings.Contains(out.Text, want) {
			t.Errorf("spilled preview missing %q; got:\n%s", want, out.Text)
		}
	}
}

func TestWebFetch_LargeResponseSpilled(t *testing.T) {
	// A body larger than WebFetchInlineBytes should be spilled.
	// Each line is short so we have many lines (line-based preview is useful).
	line := strings.Repeat("x", 50) + "\n"
	repeatCount := (WebFetchInlineBytes / len(line)) + 20
	big := strings.Repeat(line, repeatCount)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(big))
	}))
	defer srv.Close()

	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", map[string]any{
		"url": srv.URL + "/large",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(out.Text, "Saved to:") {
		t.Errorf("expected spilled file path, got: %q", out.Text)
	}
	if !strings.Contains(out.Text, "Content-Type:") {
		t.Errorf("expected content-type in preview, got: %q", out.Text)
	}
	if !strings.Contains(out.Text, "first 40 lines") {
		t.Errorf("expected head preview, got: %q", out.Text)
	}
	// Make sure the file actually exists and contains the full body.
	pathLine := strings.SplitN(out.Text, "Saved to: ", 2)
	if len(pathLine) < 2 {
		t.Fatalf("could not extract file path from output")
	}
	path := strings.SplitN(pathLine[1], "\n", 2)[0]
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("spill file not readable: %v", err)
	}
	if string(data) != big {
		t.Errorf("spill file content mismatch")
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

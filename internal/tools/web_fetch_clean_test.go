package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// htmlServer serves one fixed HTML body as text/html.
func htmlServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fetchText(t *testing.T, u string, input map[string]any) string {
	t.Helper()
	args := map[string]any{"url": u}
	for k, v := range input {
		args[k] = v
	}
	out, err := WebFetchTool{}.Execute(context.Background(), "web_fetch", args)
	if err != nil {
		t.Fatalf("Execute(%s): %v", u, err)
	}
	return out.Text
}

const articlePage = `<!doctype html>
<html><head><title>The Go Memory Model</title></head>
<body>
  <nav><a href="/">Home</a> <a href="/about">About</a> <a href="/blog">Blog</a></nav>
  <header><span>SITEWIDE BANNER</span></header>
  <div class="sidebar"><ul><li><a href="/x">Related one</a></li><li><a href="/y">Related two</a></li></ul></div>
  <article class="post-content">
    <h1>The Go Memory Model</h1>
    <p>The Go memory model specifies the conditions under which reads of a variable in one goroutine can be guaranteed to observe values produced by writes to the same variable in a different goroutine.</p>
    <p>Programs that modify data being simultaneously accessed by multiple goroutines must serialize such access, and the race detector exists precisely to find the places where they forgot to.</p>
    <h2>Happens Before</h2>
    <p>Within a single goroutine, reads and writes must behave as if they executed in the order specified by the program, even when instructions are reordered by the compiler.</p>
  </article>
  <footer>COPYRIGHT NOISE 2026</footer>
</body></html>`

func TestClean_ArticleExtraction(t *testing.T) {
	srv := htmlServer(t, articlePage)
	got := fetchText(t, srv.URL, nil)

	for _, want := range []string{
		"# The Go Memory Model",
		"## Happens Before",
		"memory model specifies the conditions",
		"race detector exists precisely",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("cleaned output missing %q\n---\n%s", want, got)
		}
	}
	for _, noise := range []string{"SITEWIDE BANNER", "COPYRIGHT NOISE", "Related one", "About"} {
		if strings.Contains(got, noise) {
			t.Errorf("cleaned output should have dropped %q\n---\n%s", noise, got)
		}
	}
	// The <title> is prepended, but the article's own <h1> says the same
	// thing — it must not appear twice.
	if n := strings.Count(got, "# The Go Memory Model"); n != 1 {
		t.Errorf("title appears %d times, want 1\n---\n%s", n, got)
	}
}

func TestClean_LinkHeavyPageFallsBackToWholePage(t *testing.T) {
	// A directory page: many links, no prose. Extraction should come up short
	// and the whole-page fallback should keep the links, since here they ARE
	// the content.
	var b strings.Builder
	b.WriteString(`<html><head><title>Package Index</title></head><body><nav><a href="/">Home</a></nav><ul>`)
	for i := 0; i < 40; i++ {
		b.WriteString(`<li><a href="/pkg/item">entry</a></li>`)
	}
	b.WriteString(`</ul></body></html>`)

	srv := htmlServer(t, b.String())
	got := fetchText(t, srv.URL, nil)

	if !strings.Contains(got, "# Package Index") {
		t.Errorf("expected the title, got:\n%s", got)
	}
	if !strings.Contains(got, "/pkg/item") {
		t.Errorf("whole-page fallback must keep links, got:\n%s", got)
	}
	// nav survives the fallback on purpose.
	if !strings.Contains(got, "Home") {
		t.Errorf("whole-page fallback should keep nav, got:\n%s", got)
	}
}

func TestClean_ShortPageFallsBack(t *testing.T) {
	srv := htmlServer(t, `<html><head><title>Tiny</title></head><body><div><p>Just a sentence.</p></div></body></html>`)
	got := fetchText(t, srv.URL, nil)
	if !strings.Contains(got, "Just a sentence.") {
		t.Errorf("short page lost its text: %q", got)
	}
	if !strings.Contains(got, "# Tiny") {
		t.Errorf("short page lost its title: %q", got)
	}
}

func TestClean_NonHTMLUntouched(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"a":1,"b":"<p>not html</p>"}`))
	}))
	defer srv.Close()

	got := fetchText(t, srv.URL, nil)
	if got != `{"a":1,"b":"<p>not html</p>"}` {
		t.Errorf("JSON must pass through untouched, got %q", got)
	}
}

func TestClean_FalseReturnsRawHTML(t *testing.T) {
	srv := htmlServer(t, articlePage)
	got := fetchText(t, srv.URL, map[string]any{"clean": false})
	if !strings.Contains(got, "<article") || !strings.Contains(got, "COPYRIGHT NOISE") {
		t.Errorf("clean=false must return the raw body, got:\n%s", got)
	}
}

// A non-boolean clean value is treated as absent — cleaning stays on.
func TestClean_NonBooleanFlagDefaultsToOn(t *testing.T) {
	srv := htmlServer(t, articlePage)
	for _, bad := range []any{"false", 0, nil} {
		got := fetchText(t, srv.URL, map[string]any{"clean": bad})
		if strings.Contains(got, "<article") {
			t.Errorf("clean=%#v should not disable cleaning, got raw HTML", bad)
		}
	}
}

func TestClean_RegularTableToGFM(t *testing.T) {
	page := `<html><head><title>T</title></head><body><article>
	<p>` + strings.Repeat("Prose to clear the extraction floor. ", 10) + `</p>
	<table>
	  <thead><tr><th>Vendor</th><th>Protocol</th></tr></thead>
	  <tbody><tr><td>Anthropic</td><td>Messages</td></tr><tr><td>OpenAI</td><td>Chat</td></tr></tbody>
	</table></article></body></html>`

	got := fetchText(t, htmlServer(t, page).URL, nil)
	for _, want := range []string{
		"| Vendor | Protocol |",
		"| --- | --- |",
		"| Anthropic | Messages |",
		"| OpenAI | Chat |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing GFM row %q\n---\n%s", want, got)
		}
	}
}

func TestClean_IrregularTableDegradesToRows(t *testing.T) {
	page := `<html><head><title>T</title></head><body><article>
	<p>` + strings.Repeat("Prose to clear the extraction floor. ", 10) + `</p>
	<table>
	  <tr><td colspan="2">Spanning header</td></tr>
	  <tr><td>left</td><td>right</td></tr>
	</table></article></body></html>`

	got := fetchText(t, htmlServer(t, page).URL, nil)
	if strings.Contains(got, "| --- |") {
		t.Errorf("colspan table must not claim to be GFM\n---\n%s", got)
	}
	for _, want := range []string{"Spanning header", "left | right"} {
		if !strings.Contains(got, want) {
			t.Errorf("degraded table lost %q\n---\n%s", want, got)
		}
	}
}

func TestClean_CodeBlockFenced(t *testing.T) {
	page := `<html><head><title>T</title></head><body><article>
	<p>` + strings.Repeat("Prose to clear the extraction floor. ", 10) + `</p>
	<pre><code class="language-go">func main() {
	fmt.Println("hi")
}</code></pre></article></body></html>`

	got := fetchText(t, htmlServer(t, page).URL, nil)
	if !strings.Contains(got, "```go") {
		t.Errorf("expected a go-tagged fence\n---\n%s", got)
	}
	if !strings.Contains(got, `fmt.Println("hi")`) {
		t.Errorf("code body lost\n---\n%s", got)
	}
	// Indentation inside <pre> is content, not noise.
	if !strings.Contains(got, "\n\tfmt.Println") {
		t.Errorf("pre whitespace not preserved\n---\n%s", got)
	}
}

func TestClean_EmptyResultFallsBackToRaw(t *testing.T) {
	// Nothing but noise tags: the cleaner produces nothing and must hand back
	// the original bytes rather than an empty string.
	raw := `<html><body><script>var a = 1;</script><style>.x{}</style></body></html>`
	got := fetchText(t, htmlServer(t, raw).URL, nil)
	if got != raw {
		t.Errorf("empty clean result must fall back to raw, got %q", got)
	}
}

// ── charset ────────────────────────────────────────────────────────────────

func gbk(t *testing.T, s string) []byte {
	t.Helper()
	enc, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatalf("gbk encode: %v", err)
	}
	return enc
}

func TestClean_GBKViaContentTypeHeader(t *testing.T) {
	body := gbk(t, `<html><head><title>中文标题</title></head><body><article><p>这是一段足够长的中文正文内容，用来确保正文提取能够越过字符数下限，从而验证编码解码在清洗之前正确完成了工作。</p></article></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got := fetchText(t, srv.URL, nil)
	if !strings.Contains(got, "中文标题") || !strings.Contains(got, "这是一段足够长的中文正文") {
		t.Errorf("GBK page not decoded, got:\n%s", got)
	}
}

func TestClean_GBKViaMetaTagOnly(t *testing.T) {
	// No charset in the header — the decoder must find <meta charset>.
	body := gbk(t, `<html><head><meta charset="gbk"><title>元标签编码</title></head><body><article><p>服务端没有在响应头里声明编码，只在文档内部用 meta 标签声明，解码必须照样成功，否则页面会变成乱码。</p></article></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got := fetchText(t, srv.URL, nil)
	if !strings.Contains(got, "元标签编码") || !strings.Contains(got, "只在文档内部用 meta 标签声明") {
		t.Errorf("meta-declared GBK not decoded, got:\n%s", got)
	}
}

// Decoding is transport normalisation, so clean=false gets it too.
func TestClean_GBKDecodedEvenWhenCleanFalse(t *testing.T) {
	body := gbk(t, `<html><head><title>原始</title></head><body><p>原始文本</p></body></html>`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=gbk")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	got := fetchText(t, srv.URL, map[string]any{"clean": false})
	if !strings.Contains(got, "原始文本") {
		t.Errorf("clean=false must still decode the charset, got:\n%s", got)
	}
	if !strings.Contains(got, "<p>") {
		t.Errorf("clean=false must keep the markup, got:\n%s", got)
	}
}

// ── URL resolution ─────────────────────────────────────────────────────────

func TestClean_RelativeURLsResolvedAgainstPage(t *testing.T) {
	page := `<html><head><title>T</title></head><body><article>
	<p>` + strings.Repeat("Prose to clear the extraction floor. ", 10) + `</p>
	<p><a href="/root">root</a> <a href="../up">up</a> <a href="deep">deep</a></p>
	<p><img src="/img/pic.png" alt="pic"></p>
	<p><a href="javascript:void(0)">script link</a></p>
	</article></body></html>`

	srv := htmlServer(t, page)
	got := fetchText(t, srv.URL+"/a/b/c", nil)

	for _, want := range []string{
		"(" + srv.URL + "/root)",
		"(" + srv.URL + "/a/up)",
		"(" + srv.URL + "/a/b/deep)",
		"![pic](" + srv.URL + "/img/pic.png)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing absolute URL %q\n---\n%s", want, got)
		}
	}
	if strings.Contains(got, "javascript:") {
		t.Errorf("javascript: URL should have been dropped\n---\n%s", got)
	}
	if !strings.Contains(got, "script link") {
		t.Errorf("text of a dropped link must survive\n---\n%s", got)
	}
}

func TestClean_BaseHrefWinsOverResponseURL(t *testing.T) {
	page := `<html><head><title>T</title><base href="https://cdn.example.com/docs/"></head><body><article>
	<p>` + strings.Repeat("Prose to clear the extraction floor. ", 10) + `</p>
	<p><a href="page.html">rel</a></p></article></body></html>`

	got := fetchText(t, htmlServer(t, page).URL+"/somewhere", nil)
	if !strings.Contains(got, "(https://cdn.example.com/docs/page.html)") {
		t.Errorf("<base href> must win over the response URL\n---\n%s", got)
	}
}

// After a cross-host redirect, relative links resolve against where we landed
// — not against the URL the caller passed in.
func TestClean_RelativeURLsUseFinalRedirectTarget(t *testing.T) {
	dest := htmlServer(t, `<html><head><title>T</title></head><body><article>
	<p>`+strings.Repeat("Prose to clear the extraction floor. ", 10)+`</p>
	<p><a href="/next">next</a></p></article></body></html>`)

	entry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/landed", http.StatusFound)
	}))
	defer entry.Close()

	got := fetchText(t, entry.URL, nil)
	if !strings.Contains(got, "("+dest.URL+"/next)") {
		t.Errorf("links must resolve against the final hop %s\n---\n%s", dest.URL, got)
	}
	if strings.Contains(got, entry.URL+"/next") {
		t.Errorf("links must not resolve against the entry URL\n---\n%s", got)
	}
}

// ── spill interaction ──────────────────────────────────────────────────────

// A page whose cleaned Markdown still exceeds the inline budget spills to a
// temp file — and now that the body is Markdown, the spill preview's heading
// outline works again. That outline is exactly what dropping the Jina proxy
// had silently disabled.
func TestClean_LargeCleanedPageSpillsWithOutline(t *testing.T) {
	var b strings.Builder
	b.WriteString(`<html><head><title>Long Document</title></head><body><article>`)
	for i := 0; i < 60; i++ {
		b.WriteString(`<h2>Section heading</h2>`)
		b.WriteString(`<p>` + strings.Repeat("Substantial paragraph text that adds up. ", 40) + `</p>`)
	}
	b.WriteString(`</article></body></html>`)

	got := fetchText(t, htmlServer(t, b.String()).URL, nil)

	if !strings.Contains(got, "Saved to:") {
		t.Fatalf("expected a spill summary, got:\n%s", got[:min(len(got), 400)])
	}
	if !strings.Contains(got, "--- outline (headings) ---") {
		t.Errorf("cleaned spill must carry a heading outline\n---\n%s", got[:min(len(got), 800)])
	}
	if !strings.Contains(got, "Section heading") {
		t.Errorf("outline should list the h2s\n---\n%s", got[:min(len(got), 800)])
	}
}

// ── unit-level checks ──────────────────────────────────────────────────────

func TestCleanHTMLToMarkdown_UnparseableInputDeclines(t *testing.T) {
	// html.Parse is famously permissive, so the honest contract is "declines
	// when nothing useful comes out", not "declines on malformed input".
	if md, ok := cleanHTMLToMarkdown([]byte("   "), nil); ok {
		t.Errorf("blank input should decline, got %q", md)
	}
}

func TestCleanHTMLToMarkdown_NoBaseKeepsAbsoluteDropsRelative(t *testing.T) {
	page := []byte(`<html><body><article><p>` +
		strings.Repeat("Prose to clear the extraction floor. ", 10) +
		`<a href="https://example.com/abs">abs</a> <a href="/rel">rel</a></p></article></body></html>`)

	md, ok := cleanHTMLToMarkdown(page, nil)
	if !ok {
		t.Fatal("expected a clean result")
	}
	if !strings.Contains(md, "[abs](https://example.com/abs)") {
		t.Errorf("absolute URL should survive without a base\n---\n%s", md)
	}
	if strings.Contains(md, "](/rel)") {
		t.Errorf("relative URL must not be emitted unresolved\n---\n%s", md)
	}
	if !strings.Contains(md, "rel") {
		t.Errorf("dropped link should keep its text\n---\n%s", md)
	}
}

func TestIsHTMLContentType(t *testing.T) {
	for _, tc := range []struct {
		ct   string
		want bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"application/xhtml+xml", true},
		{"", true},
		{"application/json", false},
		{"text/plain", false},
		{"text/markdown", false},
	} {
		if got := isHTMLContentType(tc.ct); got != tc.want {
			t.Errorf("isHTMLContentType(%q) = %v, want %v", tc.ct, got, tc.want)
		}
	}
}

func TestDecodeToUTF8_PassThroughOnUnknownCharset(t *testing.T) {
	raw := []byte("plain ascii body")
	if got := decodeToUTF8(raw, "text/html; charset=definitely-not-a-charset"); string(got) != string(raw) {
		t.Errorf("unknown charset should pass through, got %q", got)
	}
}

func TestResolveRejectsNonNavigableSchemes(t *testing.T) {
	base, _ := url.Parse("https://example.com/a/b")
	c := &mdConv{base: base}
	for _, raw := range []string{"javascript:alert(1)", "data:text/html,x", "about:blank", "#anchor", ""} {
		if got := c.resolve(raw); got != "" {
			t.Errorf("resolve(%q) = %q, want empty", raw, got)
		}
	}
	if got := c.resolve("../c"); got != "https://example.com/c" {
		t.Errorf("resolve(../c) = %q", got)
	}
}

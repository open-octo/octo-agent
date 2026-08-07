package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/net/html/charset"

	"github.com/open-octo/octo-agent/internal/agent"
)

// WebFetchMaxBytes is the absolute ceiling on a single fetched body, whether
// returned inline or spilled to a temp file. It bounds memory and disk for a
// pathological response; past it the body is truncated with a clear marker.
// Set well above any real page so the spilled file holds the full content in
// practice.
const WebFetchMaxBytes = 5 * 1024 * 1024 // 5 MB

// WebFetchInlineBytes is the size up to which a fetched body is returned
// inline. Larger responses (up to WebFetchMaxBytes) are written to a temp file
// and summarised with an outline + head preview, so a big page never floods the
// model's context while its full content stays one read_file away.
const WebFetchInlineBytes = 64 * 1024

// Preview bounds when a spilled response is summarised: a markdown-heading
// outline (so the page structure is visible for targeted read_file/grep) plus
// the opening lines. The tail is omitted — for a web page it's usually footer
// noise, while the substance sits in the body the outline maps.
const (
	webFetchPreviewHeadLines   = 40
	webFetchOutlineMaxHeadings = 50
)

// WebFetchTool fetches a URL and returns its body as text via a direct HTTP
// GET with a browser-like header set. HTML is converted to readable Markdown
// by default. JS-rendered pages come back as their static HTML skeleton; for
// interactive or login-walled content, use browser.
type WebFetchTool struct{}

func (WebFetchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch a URL and return its content via a direct HTTP GET with a " +
			"browser-like header set (JS-rendered pages return their static HTML skeleton; " +
			"for interactive or login-walled content use the browser tool). " +
			"HTML pages are converted to clean Markdown — the main article when it can be " +
			"identified, the whole page otherwise; pass clean=false for the raw response body. " +
			"Responses larger than ~64 KB are saved to a temp file; the tool returns a " +
			"preview summary (size, content-type, first/last lines) plus the file path. " +
			"Use read_file or grep on that path to inspect the full content. " +
			"Returns text only — for a binary/image URL it returns a short notice (download " +
			"it with the terminal tool, then read_file an image for multimodal viewing). " +
			"Public web only — no authentication.\n\n" +
			"If a URL returns 403/404 even though it works in a browser (hotlink/anti-bot " +
			"checks), set `referer` — e.g. the page's own origin (https://example.com) or the " +
			"search engine you found it from — or override `user_agent`.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Full URL to fetch (http or https).",
				},
				"referer": map[string]any{
					"type":        "string",
					"description": "Optional Referer header. Use when a page 403/404s without one (hotlink protection, anti-bot). Often the page's own origin or the search-result source.",
				},
				"user_agent": map[string]any{
					"type":        "string",
					"description": "Optional User-Agent override. Rarely needed (a realistic browser UA is sent by default).",
				},
				"clean": map[string]any{
					"type":        "boolean",
					"description": "Convert HTML responses to clean Markdown (extracting the main content when possible). Default true. Set false to get the raw response body — useful when you need the markup itself (meta tags, JSON-LD, inline data).",
				},
			},
			"required": []string{"url"},
		},
	}
}

func (WebFetchTool) Execute(ctx context.Context, _ string, input map[string]any) (agent.ToolResult, error) {
	if !NetworkAllowed() {
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: network access is disabled by sandbox")
	}

	raw, _ := input["url"].(string)
	if strings.TrimSpace(raw) == "" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: parse url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: only http/https URLs are allowed (got %q)", u.Scheme)
	}

	referer := strings.TrimSpace(stringArg(input, "referer"))
	userAgent := strings.TrimSpace(stringArg(input, "user_agent"))

	// Clean by default. A non-boolean value is treated as absent rather than
	// rejected — the useful reading of a malformed flag is "caller didn't mean
	// to turn cleaning off".
	clean := true
	if v, ok := input["clean"].(bool); ok {
		clean = v
	}

	// fetchDirect supplies a same-origin Referer and a browser UA by default;
	// explicit overrides are passed through when the caller needs to clear a
	// hotlink/anti-bot 403/404.
	directCtx, directCancel := context.WithTimeout(ctx, 30*time.Second)
	out, err := fetchDirect(directCtx, raw, referer, userAgent, clean)
	directCancel()
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: %w", err)
	}
	out.UI = webFetchUI(raw, out.Text)
	return out, nil
}

// webFetchUI builds the "web_fetch" UI payload. Title and status code are
// not surfaced by the fetch pipeline, so the card shows URL + a short
// preview of the rendered text.
func webFetchUI(url, text string) map[string]any {
	return map[string]any{
		"type":            "web_fetch",
		"url":             url,
		"content_preview": uiHead(text, 4, 300),
	}
}

// defaultDirectUserAgent is the browser-like UA sent on direct fetches when the
// caller doesn't override it, so simple anti-bot checks don't reject us.
const defaultDirectUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"

// fetchDirect performs a direct HTTP GET against the original URL. It uses
// a browser-like header set so simple anti-bot checks don't immediately
// reject us, but it does NOT run JavaScript — dynamic pages will return
// their static HTML skeleton. referer and userAgent override the defaults;
// when referer is empty a same-origin Referer (scheme://host/) is sent, which
// a browser would send navigating within a site and which clears many
// hotlink/anti-bot 403/404s.
func fetchDirect(ctx context.Context, rawURL, referer, userAgent string, clean bool) (agent.ToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("build request: %w", err)
	}
	if userAgent == "" {
		userAgent = defaultDirectUserAgent
	}
	if referer == "" {
		if u, perr := url.Parse(rawURL); perr == nil && u.Scheme != "" && u.Host != "" {
			referer = u.Scheme + "://" + u.Host + "/"
		}
	}
	req.Header.Set("User-Agent", userAgent)
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := directFetchHTTPClient().Do(req)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return agent.ToolResult{Text: ""}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	// resp.Request.URL is the LAST hop, not the URL we asked for. Relative
	// links in the body resolve against it, so a shortener or www-canonical
	// redirect doesn't turn every link in the page into a dead one.
	finalURL := resp.Request.URL
	if finalURL == nil {
		finalURL, _ = url.Parse(rawURL)
	}
	return readBody(resp.Body, rawURL, finalURL, resp.Header.Get("Content-Type"), clean)
}

// readBody reads the body (capped at WebFetchMaxBytes), decodes it to UTF-8,
// optionally converts HTML to Markdown, then either returns it inline
// (≤ WebFetchInlineBytes) or spills the full content to a temp file and
// returns a head+tail preview summary. finalURL is the last redirect hop and
// resolves relative links; sourceURL is what the caller asked for and labels
// the output.
func readBody(r io.Reader, sourceURL string, finalURL *url.URL, contentType string, clean bool) (agent.ToolResult, error) {
	// Content-type guard: web_fetch only returns text. A binary response
	// (image, PDF, audio/video, archive, …) would otherwise be stringified into
	// garbage that wastes the model's context. Return a clean pointer to the
	// right tool instead of reading the body at all.
	if !isTextualContentType(contentType) {
		return agent.ToolResult{Text: binaryContentNotice(sourceURL, contentType)}, nil
	}

	body, err := io.ReadAll(io.LimitReader(r, WebFetchMaxBytes+1))
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("read body: %w", err)
	}
	truncated := false
	if len(body) > WebFetchMaxBytes {
		body = body[:WebFetchMaxBytes]
		truncated = true
	}

	body = decodeToUTF8(body, contentType)

	if clean && isHTMLContentType(contentType) {
		// truncated stays as-is on purpose: cleaning shrinks the output, but
		// the bytes the server never got to send are still missing.
		if md, ok := cleanHTMLToMarkdown(body, finalURL); ok {
			body = []byte(md)
		}
	}

	// Within the inline budget — return it directly. An inline body is always
	// well under WebFetchMaxBytes, so it is never truncated here.
	if len(body) <= WebFetchInlineBytes {
		return agent.ToolResult{Text: string(body)}, nil
	}

	// Larger — spill the full body to a temp file and return a preview summary.
	return spillWebFetch(body, sourceURL, contentType, truncated)
}

// decodeToUTF8 converts a response body to UTF-8. html.Parse — and the model
// itself — assume UTF-8, so a GBK/Big5/Shift-JIS page would otherwise come
// back as mojibake, and silently: nothing errors, so no fallback fires. The
// charset is taken from the Content-Type parameter, then the document's own
// <meta charset>, then a BOM, then content sniffing.
//
// This runs regardless of the clean flag. Decoding is transport normalisation,
// not cleaning — "raw" means unextracted and unconverted, not undecoded. When
// the encoding can't be determined the body is returned untouched.
func decodeToUTF8(body []byte, contentType string) []byte {
	r, err := charset.NewReader(bytes.NewReader(body), contentType)
	if err != nil {
		return body
	}
	// The limit re-caps the DECODED size: a legacy-encoded body near
	// WebFetchMaxBytes can grow ~1.5× as UTF-8. The tail past the cap is
	// dropped without a truncation marker — accepted, since the input was
	// already cut at the same cap and real pages don't get near it.
	decoded, err := io.ReadAll(io.LimitReader(r, WebFetchMaxBytes+1))
	if err != nil {
		return body
	}
	return decoded
}

// isHTMLContentType reports whether a response is HTML worth running the
// cleaner over. An empty Content-Type is common on static hosts and the body
// is usually HTML, so it is included — cleanHTMLToMarkdown declines anything
// that doesn't parse into real content anyway.
func isHTMLContentType(contentType string) bool {
	switch mediaType(contentType) {
	case "", "text/html", "application/xhtml+xml":
		return true
	}
	return false
}

// mediaType returns the lowercased media type of a Content-Type header,
// stripping any ";charset=…" / boundary parameters and surrounding space.
func mediaType(contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(contentType))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	return ct
}

// isTextualContentType reports whether a Content-Type names text web_fetch can
// usefully return. An empty type is treated as text — many servers omit it and
// the body is usually HTML/markdown. Covers text/*, JSON/XML/JS, and the
// +json / +xml structured-syntax suffixes.
func isTextualContentType(contentType string) bool {
	ct := mediaType(contentType)
	if ct == "" {
		return true
	}
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	if strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml") {
		return true
	}
	switch ct {
	case "application/json", "application/xml", "application/javascript",
		"application/ecmascript", "application/markdown", "application/x-ndjson",
		"application/x-www-form-urlencoded", "application/yaml", "application/x-yaml":
		return true
	}
	return false
}

// imageTypeExtension maps an image media type to the file extension read_file
// recognises (so a downloaded image is rendered visually, not refused). Returns
// "" for non-image or unknown types.
func imageTypeExtension(ct string) string {
	switch mediaType(ct) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/bmp":
		return ".bmp"
	case "image/tiff":
		return ".tiff"
	case "image/heic":
		return ".heic"
	case "image/x-icon", "image/vnd.microsoft.icon":
		return ".ico"
	}
	return ""
}

// binaryContentNotice is the message web_fetch returns for a non-text response:
// it names the type and points at the tool that can actually handle it, instead
// of dumping garbled bytes into the model's context.
func binaryContentNotice(sourceURL, contentType string) string {
	shown := strings.TrimSpace(contentType)
	if shown == "" {
		shown = "non-text"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "web_fetch: %s returned %s content — web_fetch only handles text, so the bytes are not shown.\n", sourceURL, shown)
	if ext := imageTypeExtension(contentType); ext != "" {
		fmt.Fprintf(&b, "To view this image, download it and open it with read_file (which returns images for multimodal viewing):\n")
		fmt.Fprintf(&b, "  terminal: curl -sL %q -o /tmp/web_fetch_image%s\n  read_file: /tmp/web_fetch_image%s", sourceURL, ext, ext)
	} else {
		b.WriteString("If you need its contents, download it with the terminal tool (curl/wget) and use the appropriate tool on the saved file.")
	}
	return b.String()
}

// spillWebFetch writes body to a temp file and returns a preview summary with
// file path, size, content-type, a markdown-heading outline, and the opening
// lines.
func spillWebFetch(body []byte, sourceURL, contentType string, truncated bool) (agent.ToolResult, error) {
	text := string(body)
	lines := strings.Split(text, "\n")

	path, err := writeWebFetchSpillFile(sourceURL, body)
	if err != nil {
		// Degrade gracefully: return inline on write failure.
		out := text
		if truncated {
			out += "\n\n…[truncated at " + strconv.Itoa(WebFetchMaxBytes) + " bytes]"
		}
		return agent.ToolResult{Text: out}, nil
	}

	headCount := webFetchPreviewHeadLines
	if headCount > len(lines) {
		headCount = len(lines)
	}

	var preview strings.Builder
	fmt.Fprintf(&preview, "URL: %s\n", sourceURL)
	fmt.Fprintf(&preview, "Size: %s (%d lines)\n", formatBytes(int64(len(body))), len(lines))
	if contentType != "" {
		fmt.Fprintf(&preview, "Content-Type: %s\n", contentType)
	}
	fmt.Fprintf(&preview, "Saved to: %s\n", path)
	if truncated {
		fmt.Fprintf(&preview, "Note: response truncated at %s (server sent more)\n", formatBytes(int64(WebFetchMaxBytes)))
	}
	if outline := markdownOutline(lines, webFetchOutlineMaxHeadings); outline != "" {
		preview.WriteString("\n--- outline (headings) ---\n")
		preview.WriteString(outline)
	}
	fmt.Fprintf(&preview, "\n--- first %d lines ---\n", headCount)
	preview.WriteString(strings.Join(lines[:headCount], "\n"))
	fmt.Fprintf(&preview, "\n\n[Full content saved to %s — use read_file or grep to inspect.]", path)

	return agent.ToolResult{Text: preview.String()}, nil
}

// markdownOutline extracts ATX markdown headings (#, ##, … up to ######) and
// renders them as an indented outline, so a spilled page's structure is visible
// at a glance for targeted read_file/grep. Headings inside fenced code blocks
// are ignored. Returns "" when there are no headings (raw HTML, plain text,
// JSON); at most maxHeadings are listed, with a trailing "+N more" count.
func markdownOutline(lines []string, maxHeadings int) string {
	var b strings.Builder
	shown, total := 0, 0
	inFence := false
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level := 0
		for level < len(t) && t[level] == '#' {
			level++
		}
		// An ATX heading is 1–6 '#' followed by a space and some text.
		if level < 1 || level > 6 || level >= len(t) || t[level] != ' ' {
			continue
		}
		title := strings.TrimSpace(t[level+1:])
		if title == "" {
			continue
		}
		total++
		if shown < maxHeadings {
			fmt.Fprintf(&b, "%s%s\n", strings.Repeat("  ", level-1), title)
			shown++
		}
	}
	if total == 0 {
		return ""
	}
	if total > shown {
		fmt.Fprintf(&b, "… +%d more heading(s)\n", total-shown)
	}
	return b.String()
}

// writeWebFetchSpillFile persists body under ~/.octo/tmp and returns the
// absolute path. The filename is derived from the URL host + a timestamp (so
// concurrent fetches never collide) and ends with the pid so CleanSpillFiles
// reclaims it on a clean exit, the same way it does for `term-` files.
func writeWebFetchSpillFile(sourceURL string, body []byte) (string, error) {
	dir, err := spillDir()
	if err != nil {
		return "", err
	}
	sweepOldSpillFiles(dir)

	u, _ := url.Parse(sourceURL)
	host := "unknown"
	if u != nil && u.Host != "" {
		host = sanitizeSpillID(u.Host)
	}
	name := fmt.Sprintf("webfetch-%s-%d-%d.log", host, time.Now().UnixNano(), os.Getpid())
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// webHTTPClient is the shared http.Client used by the network backends of
// web_search. Default Go client has no timeout — we set 30s to keep agents
// responsive.
func webHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// blockedFetchIP reports whether ip sits in a link-local range — the block that
// includes the cloud instance-metadata endpoint (IPv4 169.254.169.254 and the
// IPv6 fe80::/10 equivalents). A prompt-injected URL aimed at metadata is the
// highest-value SSRF target — it can leak cloud credentials — so web_fetch
// refuses to dial it. Loopback and private-LAN ranges stay reachable on
// purpose: fetching your own dev server (http://localhost:3000, a LAN box) is a
// legitimate, common use of the tool on a developer machine.
func blockedFetchIP(ip net.IP) bool {
	return ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()
}

// secureFetchTransport builds an http.Transport whose dialer rejects link-local
// destinations. The check runs in net.Dialer.Control, which fires AFTER DNS
// resolution with the concrete remote IP — so a hostname that resolves to a
// blocked address (DNS rebinding) is refused too, and every redirect hop is
// re-dialed through the same hook. Used by the web_fetch client.
func secureFetchTransport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				host = address
			}
			if ip := net.ParseIP(host); ip != nil && blockedFetchIP(ip) {
				return fmt.Errorf("refusing to connect to link-local/metadata address %s", host)
			}
			return nil
		},
	}
	return &http.Transport{
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// directFetchHTTPClient is the client used by web_fetch. It MUST allow
// cross-host redirects (URL shorteners, www-canonical hops, http→https on
// another host are all normal for an arbitrary URL), but it shares the
// link-local block via secureFetchTransport and caps the redirect chain so a
// redirect loop can't hang the agent.
func directFetchHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   30 * time.Second,
		Transport: secureFetchTransport(),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}
}

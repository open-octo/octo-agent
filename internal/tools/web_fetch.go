package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Leihb/octo-agent/internal/agent"
	"github.com/Leihb/octo-agent/internal/version"
)

// JinaReaderHost is the public Jina AI Reader endpoint. Sending a URL via
// `https://r.jina.ai/<URL>` returns the page rendered to Markdown — handles
// JavaScript-rendered pages, paywalls (where Jina has access), and most
// of the noisy chrome a raw curl would dump on the LLM.
const JinaReaderHost = "https://r.jina.ai/"

// jinaReaderHostForTest is the actual host the tool uses. Tests swap it
// out to a local httptest server; production reads the const default.
var jinaReaderHostForTest = JinaReaderHost

// WebFetchMaxBytes is the absolute ceiling on a single fetched body, whether
// returned inline or spilled to a temp file. It bounds memory and disk for a
// pathological response; past it the body is truncated with a clear marker.
// Set well above any real page so the spilled file holds the full content in
// practice.
const WebFetchMaxBytes = 5 * 1024 * 1024 // 5 MB

// WebFetchInlineBytes is the size up to which a fetched body is returned
// inline. Larger responses (up to WebFetchMaxBytes) are written to a temp file
// and summarised with a head+tail preview, so a big page never floods the
// model's context while its full content stays one read_file away.
const WebFetchInlineBytes = 64 * 1024

// WebFetchPreviewLines bounds the head+tail preview when output is spilled.
const (
	webFetchPreviewHeadLines = 30
	webFetchPreviewTailLines = 10
)

// WebFetchTool fetches a URL and returns its body as Markdown. It prefers
// the Jina AI Reader proxy for JS-rendered pages and clean HTML-to-Markdown
// conversion, but falls back to a direct HTTP fetch when the proxy fails.
type WebFetchTool struct{}

func (WebFetchTool) Definition() agent.ToolDefinition {
	return agent.ToolDefinition{
		Name: "web_fetch",
		Description: "Fetch a URL and return its content. Prefers the Jina Reader proxy " +
			"for JS-rendered pages and clean HTML-to-Markdown conversion; falls back to " +
			"a direct HTTP fetch when the proxy is unavailable. " +
			"Responses larger than ~64 KB are saved to a temp file; the tool returns a " +
			"preview summary (size, content-type, first/last lines) plus the file path. " +
			"Use read_file or grep on that path to inspect the full content. " +
			"Returns text only — for a binary/image URL it returns a short notice (download " +
			"it with the terminal tool, then read_file an image for multimodal viewing). " +
			"Public web only — no authentication.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "Full URL to fetch (http or https).",
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

	// Strategy: try Jina Reader proxy first (better quality), then fall back
	// to a direct fetch on network-level or 5xx/429 proxy failures.
	// Jina gets a short 5s timeout so a slow proxy doesn't block the fallback.
	jinaCtx, jinaCancel := context.WithTimeout(ctx, 5*time.Second)
	out, jinaErr := fetchViaJina(jinaCtx, raw)
	jinaCancel()
	if jinaErr == nil {
		return out, nil
	}

	// Fallback conditions: network errors (TLS, DNS, timeout), 5xx proxy
	// errors, or 429 rate-limit. 4xx client errors (e.g. 404) from the proxy
	// are NOT retried — the proxy correctly reflected an upstream 404.
	if shouldFallback(jinaErr) {
		// Give the direct fetch the remaining time up to 30s total.
		directCtx, directCancel := context.WithTimeout(ctx, 30*time.Second)
		out, directErr := fetchDirect(directCtx, raw)
		directCancel()
		if directErr == nil {
			return out, nil
		}
		// Both failed — surface both errors so the LLM knows what happened.
		return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: jina proxy failed (%v); direct fetch also failed (%v)", jinaErr, directErr)
	}

	return agent.ToolResult{Text: ""}, fmt.Errorf("web_fetch: %w", jinaErr)
}

// shouldFallback returns true when a Jina proxy error is worth retrying
// with a direct fetch. 4xx errors (except 429 rate-limit) are assumed to
// be legitimate upstream responses and are not retried.
func shouldFallback(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// Network-level errors always fallback.
	if strings.Contains(s, "tls:") ||
		strings.Contains(s, "x509:") ||
		strings.Contains(s, "no such host") ||
		strings.Contains(s, "connection refused") ||
		strings.Contains(s, "connection reset by peer") ||
		strings.Contains(s, "timeout") ||
		strings.Contains(s, "temporary failure") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "context deadline exceeded") ||
		strings.Contains(s, "EOF") {
		return true
	}
	// HTTP 5xx or 429 from the proxy — the proxy itself is struggling.
	if strings.Contains(s, "HTTP 5") || strings.Contains(s, "HTTP 429") {
		return true
	}
	return false
}

// fetchViaJina calls the Jina Reader proxy and returns the rendered Markdown.
func fetchViaJina(ctx context.Context, rawURL string) (agent.ToolResult, error) {
	// Jina's contract is literal string concatenation: r.jina.ai/<rest>.
	// Do NOT QueryEscape — Jina parses the rest-of-path itself, and
	// escaping breaks routing. A `#fragment` in raw is technically lost
	// here (Jina won't see it), but fragments are never sent to servers
	// anyway, so this matches normal HTTP semantics.
	jinaURL := jinaReaderHostForTest + rawURL

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jinaURL, nil)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown,text/plain,*/*")
	// Identify ourselves so Jina can reach us about issues.
	req.Header.Set("User-Agent", version.UserAgent())

	resp, err := webFetchHTTPClient().Do(req)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return agent.ToolResult{Text: ""}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return readBody(resp.Body, rawURL, resp.Header.Get("Content-Type"))
}

// fetchDirect performs a direct HTTP GET against the original URL. It uses
// a browser-like header set so simple anti-bot checks don't immediately
// reject us, but it does NOT run JavaScript — dynamic pages will return
// their static HTML skeleton.
func fetchDirect(ctx context.Context, rawURL string) (agent.ToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return agent.ToolResult{Text: ""}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return agent.ToolResult{Text: ""}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return agent.ToolResult{Text: ""}, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return readBody(resp.Body, rawURL, resp.Header.Get("Content-Type"))
}

// readBody reads the body (capped at WebFetchMaxBytes), then either returns it
// inline (≤ WebFetchInlineBytes) or spills the full content to a temp file and
// returns a head+tail preview summary.
func readBody(r io.Reader, sourceURL, contentType string) (agent.ToolResult, error) {
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

	// Within the inline budget — return it directly. An inline body is always
	// well under WebFetchMaxBytes, so it is never truncated here.
	if len(body) <= WebFetchInlineBytes {
		return agent.ToolResult{Text: string(body)}, nil
	}

	// Larger — spill the full body to a temp file and return a preview summary.
	return spillWebFetch(body, sourceURL, contentType, truncated)
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

// spillWebFetch writes body to a temp file and returns a preview summary
// with file path, size, content-type, and head+tail lines.
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
	tailCount := webFetchPreviewTailLines
	if tailCount > len(lines)-headCount {
		tailCount = len(lines) - headCount
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
	fmt.Fprintf(&preview, "\n--- first %d lines ---\n", headCount)
	preview.WriteString(strings.Join(lines[:headCount], "\n"))
	if tailCount > 0 {
		fmt.Fprintf(&preview, "\n\n--- last %d lines ---\n", tailCount)
		preview.WriteString(strings.Join(lines[len(lines)-tailCount:], "\n"))
	}
	fmt.Fprintf(&preview, "\n\n[Full content saved to %s — use read_file or grep to inspect.]", path)

	return agent.ToolResult{Text: preview.String()}, nil
}

// writeWebFetchSpillFile persists body under ~/.octo/tmp and returns the
// absolute path. The filename is derived from the URL host + a timestamp
// so concurrent fetches never collide.
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
	name := fmt.Sprintf("webfetch-%s-%d.log", host, time.Now().UnixNano())
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

// webFetchHTTPClient is web_fetch's dedicated client. It refuses to follow a
// cross-host 3xx redirect: web_fetch always targets r.jina.ai, so a redirect
// to a different host means the proxy is bouncing us somewhere unexpected —
// a classic SSRF / data-exfil vector. Same-host redirects (path changes) are
// still followed. web_search keeps the plain webHTTPClient because search
// backends legitimately redirect across hosts.
func webFetchHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) == 0 {
				return nil
			}
			prev := via[len(via)-1].URL.Host
			if !strings.EqualFold(req.URL.Host, prev) {
				return fmt.Errorf("refusing cross-host redirect to %q (from %q); "+
					"re-issue web_fetch against the final URL if that destination is intended",
					req.URL.Host, prev)
			}
			return nil
		},
	}
}

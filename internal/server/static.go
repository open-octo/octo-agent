package server

import (
	"compress/gzip"
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

//go:embed all:webdist
var webdistFS embed.FS

// staticHandler serves the embedded Vite-built Web UI from webdist/. It strips
// the "/" prefix and falls back to index.html for SPA routing. webdist/ itself
// is gitignored (built by `make web-build` locally, by CI for releases) — on a
// fresh clone it holds only .gitkeep, in which case we serve a minimal
// built-in placeholder instead.
func (s *Server) staticHandler() http.Handler {
	sub, err := fs.Sub(webdistFS, "webdist")
	built := false
	if err == nil {
		if _, aerr := sub.Open("assets"); aerr == nil {
			built = true
		}
	}

	// UI not built (fresh clone without `make web-build`): serve a placeholder.
	if !built {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" {
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(indexHTMLFallback))
				return
			}
			http.NotFound(w, r)
		})
	}

	return staticFileHandler(sub)
}

// staticFileHandler serves a built Web UI from dist. Split from staticHandler
// so tests can drive it over an in-memory FS — webdist/ is empty in a checkout.
func staticFileHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// API routes should never reach here (mux routes them first), but
		// guard defensively so a missing API route doesn't fall through to
		// the SPA fallback.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}

		// Resolve the requested file. The root and any path that doesn't map
		// to an embedded file are served the SPA entrypoint.
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			if f, err := dist.Open(name); err == nil {
				_ = f.Close()
				setStaticCacheControl(w.Header(), name)
				serveCompressed(w, r, dist, name, fileServer)
				return
			}
		}

		// Serve index.html directly rather than rewriting the path to
		// "/index.html" and delegating to fileServer: http.FileServer
		// canonicalises any "/index.html" request with a 301 redirect to
		// "./", which resolves back to "/" and loops forever. ServeFileFS
		// keys its redirect off r.URL.Path (here "/" or an SPA route), so it
		// serves the file without redirecting.
		setStaticCacheControl(w.Header(), "index.html")
		serveCompressed(w, r, dist, "index.html", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.ServeFileFS(w, r, dist, "index.html")
		}))
	})
}

// setStaticCacheControl picks the cache policy for a Web UI file. Vite names
// everything under assets/ by content hash, so those may be cached forever: a
// rebuild changes the name, never the bytes behind an existing one. The
// entrypoint and anything else unhashed must be revalidated on every load —
// left without a policy, WKWebView caches heuristically, and a stale index.html
// pointing at hashes the upgraded binary no longer embeds is a blank window.
// Embedded files carry no modtime, so there is no validator to offer and
// no-cache means a (small) full fetch each time, exactly as before.
func setStaticCacheControl(h http.Header, name string) {
	if strings.HasPrefix(name, "assets/") {
		h.Set("Cache-Control", "public, max-age=31536000, immutable")
		return
	}
	h.Set("Cache-Control", "no-cache")
}

// compressible reports whether a Web UI file is worth gzipping on the fly.
// Images and fonts are already compressed; text-like assets shrink 3-5×.
func compressible(name string) bool {
	switch path.Ext(name) {
	case ".js", ".mjs", ".css", ".html", ".svg", ".json", ".map", ".txt", ".wasm":
		return true
	}
	return false
}

// serveCompressed serves name from dist gzipped when the client accepts it and
// the file is worth it; every other request goes to next (http.FileServer /
// ServeFileFS) untouched. FileServer sends the embedded FS as-is, so a cold
// load was moving the whole ~600 KB entry bundle uncompressed on every window
// open. Range and non-GET requests pass through: a byte range of gzipped
// output is meaningless, and a HEAD must not grow a body. So does a path
// FileServer answers with something other than the file — its ".../index.html"
// → "./" canonicalising 301 — and a name that fails to read, which FileServer
// turns into its own 404.
//
// The compressed body is produced here, from bytes read out of dist, rather
// than by wrapping the ResponseWriter handed to next. A wrapper must forward
// whatever the inner handler writes when the status is not 200, and that
// forwarding is indistinguishable, to a static analyser, from reflecting
// request data into the response (CodeQL go/reflected-xss, alert 203). Writing
// only what was read from the embedded FS leaves nothing to misread.
func serveCompressed(w http.ResponseWriter, r *http.Request, dist fs.FS, name string, next http.Handler) {
	if r.Method != http.MethodGet || r.Header.Get("Range") != "" || !compressible(name) ||
		!strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
		strings.HasSuffix(r.URL.Path, "/index.html") {
		next.ServeHTTP(w, r)
		return
	}
	data, err := fs.ReadFile(dist, name)
	if err != nil {
		next.ServeHTTP(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", staticContentType(name, data))
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	// No Content-Length: the compressed size is only known after the fact, and
	// no Accept-Ranges: a Range against this representation is not served.
	w.WriteHeader(http.StatusOK)
	gz := gzip.NewWriter(w)
	_, _ = gz.Write(data)
	_ = gz.Close()
}

// staticContentType mirrors http.FileServer's choice: the extension's MIME
// type, else sniffed from the leading bytes.
func staticContentType(name string, data []byte) string {
	if ctype := mime.TypeByExtension(path.Ext(name)); ctype != "" {
		return ctype
	}
	return http.DetectContentType(data)
}

// indexHTMLFallback is a minimal placeholder served when the embedded static/
// directory is absent. It lets the server start and respond to / even before
// the Web UI assets are built.
const indexHTMLFallback = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<title>Octo Agent</title>
<style>
body{font-family:system-ui,-apple-system,sans-serif;max-width:800px;margin:40px auto;padding:0 20px;line-height:1.6}
h1{color:#333}code{background:#f4f4f4;padding:2px 6px;border-radius:3px}
</style>
</head>
<body>
<h1>🐙 Octo Agent Server</h1>
<p>The Web UI assets are not embedded in this binary. Run <code>make web-build</code> and recompile to enable it.</p>
<p>API endpoints available:</p>
<ul>
<li><code>POST /api/chat</code> — create a new session and send a message</li>
<li><code>POST /api/chat/:id/turn</code> — send a message to an existing session</li>
<li><code>GET  /api/sessions</code> — list recent sessions</li>
<li><code>GET  /api/sessions/:id</code> — get session details</li>
<li><code>DELETE /api/sessions/:id</code> — delete one session</li>
<li><code>POST /api/sessions/delete</code> — delete sessions in bulk (body: {ids:[…]})</li>
<li><code>GET  /api/tools</code> — list available tools</li>
<li><code>GET  /api/skills</code> — list available skills</li>
<li><code>GET  /api/health</code> — health check</li>
</ul>
</body>
</html>
`

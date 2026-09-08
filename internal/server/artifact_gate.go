package server

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// The external-reference gate for HTML served from the artifact origin.
//
// A <script src> or a render-affecting <link href> may point only at the CDN
// hosts below; anything else is removed before the document leaves the
// server, and the page renders under a banner saying how many were removed.
// The same list is quoted to the model in internal/prompt/base.md and in
// internal/skills/defaults/artifact-design/SKILL.md — change all three
// together.
//
// Why an allowlist rather than fully open or fully closed: the page must also
// render on a machine with a poor route to the wider internet — offline, behind
// a national firewall — and must still render years later once saved as a
// Light App. Well-known CDNs (with mainland-China mirrors) are the pragmatic
// middle: they unlock real libraries a model can never inline, while keeping
// the page's fate out of arbitrary hosts' hands. Relative references are not
// external at all here — the artifact origin serves the page's own directory —
// so they pass through untouched.
var artifactCDNAllowlist = map[string]bool{
	"cdnjs.cloudflare.com": true,
	"cdn.jsdelivr.net":     true,
	"unpkg.com":            true,
	"fonts.googleapis.com": true,
	"fonts.gstatic.com":    true,
	// Mainland-China mirrors — the global CDNs above are flaky or blocked there.
	"cdn.bootcdn.net":        true,
	"cdn.staticfile.org":     true,
	"cdn.staticfile.net":     true,
	"registry.npmmirror.com": true,
}

// artifactCSP is the Content-Security-Policy every response from an artifact
// or Light App origin carries. The gate above removes the *static* external
// scripts and stylesheets a page declares; this makes the same allowlist hold
// for everything the page does at run time — fetch, XHR, WebSocket, beacons,
// images, fonts, media, workers, frames, dynamically inserted scripts — so a
// page that can read the files beside it cannot ship them anywhere but its own
// origin and the allowlisted CDNs. `'unsafe-inline'` and `'unsafe-eval'` are
// granted for scripts and styles: the page's own code is inline by nature and
// libraries compile templates or WebAssembly; the policy is an egress boundary,
// not an XSS defence (the same-origin boundary does that job).
//
// What it cannot close: the frame navigating itself to another site with data
// in the URL — CSP has no directive for navigation, and the sandbox only
// withholds top-level navigation. `form-action 'self'` closes the form-submit
// shape of that; the `location` shape stays open and is documented as such in
// dev-docs/artifact-origin-design.md.
//
// frame-ancestors has no IPv6 literal: CSP's host-source grammar has no
// bracket form, and one malformed source would void the whole directive. A UI
// reached over [::1] is therefore told the origin is unavailable instead.
var artifactCSP = buildArtifactCSP()

func buildArtifactCSP() string {
	hosts := make([]string, 0, len(artifactCDNAllowlist))
	for h := range artifactCDNAllowlist {
		hosts = append(hosts, "https://"+h)
	}
	sort.Strings(hosts)
	cdn := strings.Join(hosts, " ")
	return strings.Join([]string{
		"default-src 'self' data: blob: " + cdn,
		"script-src 'self' 'unsafe-inline' 'unsafe-eval' data: blob: " + cdn,
		"style-src 'self' 'unsafe-inline' data: blob: " + cdn,
		"form-action 'self'",
		"base-uri 'self'",
		"frame-ancestors http://localhost:* http://127.0.0.1:*",
	}, "; ")
}

// artifactRefAllowed reports whether an absolute reference may stay: only an
// explicit https:// URL on an allowlisted host. URL parsing, not string
// prefixing, decides the host, so `https://cdn.jsdelivr.net@evil.com/x.js`
// resolves to its real hostname and fails the lookup; a protocol-relative
// `//cdn.jsdelivr.net/x.js` has no scheme and fails too.
func artifactRefAllowed(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return u.Scheme == "https" && artifactCDNAllowlist[strings.ToLower(u.Hostname())]
}

// A reference that loads nothing external, or loads from the page's own
// origin, stays: data:, blob:, a fragment, an empty value, and anything with
// no scheme and no host (`./app.js`, `assets/x.css`, `/x.js`).
func artifactRefKept(raw string) bool {
	ref := strings.TrimSpace(raw)
	if ref == "" || strings.HasPrefix(ref, "#") {
		return true
	}
	lower := strings.ToLower(ref)
	if strings.HasPrefix(lower, "data:") || strings.HasPrefix(lower, "blob:") {
		return true
	}
	if strings.HasPrefix(ref, "//") {
		return false
	}
	u, err := url.Parse(ref)
	if err != nil {
		return false
	}
	return u.Scheme == "" && u.Host == ""
}

var (
	gateHintRe   = regexp.MustCompile(`(?i)<script|<link`)
	renderRelRe  = regexp.MustCompile(`(?i)(?:^|\s)(?:stylesheet|preload|modulepreload)(?:\s|$)`)
	bannerAnchor = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<body\b[^>]*>`),
		regexp.MustCompile(`(?i)</head\s*>`),
		regexp.MustCompile(`(?i)<html\b[^>]*>`),
		regexp.MustCompile(`(?i)<!doctype\b[^>]*>`),
	}
)

// gateArtifactHTML returns the document with its disallowed external scripts
// and stylesheets removed and a banner added when any were; the input comes
// back byte-for-byte when nothing had to go, so a self-contained page is never
// reshaped by a parse/serialize round-trip.
//
// The judgment runs on a parsed tree, not on tag regexes: the reference that
// matters is the one the browser's own parser will fetch, and only a parser
// agrees with it on what that is. A decoy `src=` inside another attribute's
// quoted value, a quoted `>` truncating the apparent tag, `<script/src=…>`
// with no whitespace — each would make a string scan judge one URL while the
// browser fetched another, turning the allowlist fail-open. Of duplicate
// attributes the first is judged, which is the one the browser keeps.
func gateArtifactHTML(src []byte, dark bool) []byte {
	if !gateHintRe.Match(src) {
		return src
	}
	doc, err := html.Parse(bytes.NewReader(src))
	if err != nil {
		return src
	}
	var doomed []*html.Node
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch n.DataAtom {
			case atom.Script:
				if ref, ok := firstAttr(n, "src"); ok && !artifactRefKept(ref) && !artifactRefAllowed(ref) {
					doomed = append(doomed, n)
				}
			case atom.Link:
				rel, _ := firstAttr(n, "rel")
				if ref, ok := firstAttr(n, "href"); ok && renderRelRe.MatchString(rel) && !artifactRefKept(ref) && !artifactRefAllowed(ref) {
					doomed = append(doomed, n)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if len(doomed) == 0 {
		return src
	}
	for _, n := range doomed {
		if n.Parent != nil {
			n.Parent.RemoveChild(n)
		}
	}
	var buf bytes.Buffer
	if err := html.Render(&buf, doc); err != nil {
		return src
	}
	return withStrippedBanner(buf.Bytes(), len(doomed), dark)
}

// firstAttr returns the first attribute with the given (lowercase) key — the
// parser keeps duplicates in source order, and the browser honours the first.
func firstAttr(n *html.Node, key string) (string, bool) {
	for _, a := range n.Attr {
		if a.Namespace == "" && a.Key == key {
			return a.Val, true
		}
	}
	return "", false
}

// The banner a stripped page renders under. It goes right after the <body>
// start tag so the page's own layout still applies to the content below it,
// with the same fallbacks the browser-side gate used before it moved here:
// after </head>, after <html …>, after <!DOCTYPE …>, and only for a bare
// fragment the very top — a <div> ahead of the DOCTYPE would drop the page
// into quirks mode. Normal flow, not fixed: a fixed bar would sit on top of
// whatever the page puts at y=0.
func withStrippedBanner(doc []byte, removed int, dark bool) []byte {
	bg, border, color := "#fff8e1", "#f0c040", "#7a5c00"
	if dark {
		bg, border, color = "#2b2111", "#594214", "#e8b339"
	}
	what := fmt.Sprintf("%d external scripts/stylesheets were", removed)
	if removed == 1 {
		what = "1 external script/stylesheet was"
	}
	banner := fmt.Sprintf(`<div style="padding:8px 12px;font:12px/1.5 system-ui,sans-serif;color:%s;background:%s;border-bottom:1px solid %s">`+
		`⚠️ %s removed — only well-known CDNs (cdnjs, jsdelivr, unpkg, bootcdn, …) load here. `+
		`The page may look or behave differently; the file itself is unchanged.</div>`, color, bg, border, what)
	for _, re := range bannerAnchor {
		loc := re.FindIndex(doc)
		if loc == nil {
			continue
		}
		out := append([]byte{}, doc[:loc[1]]...)
		out = append(out, banner...)
		return append(out, doc[loc[1]:]...)
	}
	return append([]byte(banner), doc...)
}

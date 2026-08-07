package tools

import (
	"bytes"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// webFetchMinExtractedChars is the floor below which main-content extraction is
// judged to have failed: too little text came back to plausibly be the article,
// so the whole page is converted instead. A starting value — calibrate against
// real pages, not intuition.
const webFetchMinExtractedChars = 200

// cleanHTMLToMarkdown turns a raw HTML page into readable Markdown: it finds
// the main content and converts that, falling back to a whole-page conversion
// when extraction comes up short (directory pages, indexes, stubs — where the
// links ARE the content). base resolves relative href/src so every URL in the
// output is one the model can fetch again.
//
// Returns ok=false when the page can't be parsed or yields nothing; callers
// keep the raw text in that case rather than surfacing an error.
func cleanHTMLToMarkdown(raw []byte, base *url.URL) (string, bool) {
	doc, err := html.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", false
	}
	dropNoiseSubtrees(doc)
	if href := baseHref(doc); href != "" {
		if u, err := url.Parse(href); err == nil {
			if base != nil {
				base = base.ResolveReference(u)
			} else if u.IsAbs() {
				base = u
			}
		}
	}

	var body string
	if main := extractMainContent(doc); main != nil {
		body = (&mdConv{base: base, skipChrome: true}).render(main)
	}
	// Extraction produced too little to be the article — convert the whole
	// page, chrome and all. A page that fails extraction is usually one whose
	// navigation is the substance.
	if len([]rune(strings.TrimSpace(body))) < webFetchMinExtractedChars {
		body = (&mdConv{base: base, skipChrome: false}).render(doc)
	}
	body = strings.TrimSpace(collapseBlankRuns(body))
	if body == "" {
		return "", false
	}

	// The model needs to know what it is reading, and <title> lives in the
	// <head> that extraction never looks at.
	if title := documentTitle(doc); title != "" && !titleAlreadyLeads(body, title) {
		body = "# " + title + "\n\n" + body
	}
	return body, true
}

// ── DOM pruning ────────────────────────────────────────────────────────────

// noiseTags are dropped from the tree outright, before scoring or conversion:
// they carry no reader-visible text on either path.
var noiseTags = map[atom.Atom]bool{
	atom.Script:   true,
	atom.Style:    true,
	atom.Noscript: true,
	atom.Svg:      true,
	atom.Iframe:   true,
	atom.Form:     true,
	atom.Template: true,
	atom.Object:   true,
	atom.Embed:    true,
	atom.Canvas:   true,
}

// chromeTags are page furniture. Main-content extraction skips them, but the
// whole-page fallback keeps them ON PURPOSE — see cleanHTMLToMarkdown.
var chromeTags = map[atom.Atom]bool{
	atom.Nav:    true,
	atom.Header: true,
	atom.Footer: true,
	atom.Aside:  true,
}

func dropNoiseSubtrees(n *html.Node) {
	var next *html.Node
	for c := n.FirstChild; c != nil; c = next {
		next = c.NextSibling
		if c.Type == html.CommentNode || (c.Type == html.ElementNode && (noiseTags[c.DataAtom] || isHidden(c))) {
			n.RemoveChild(c)
			continue
		}
		dropNoiseSubtrees(c)
	}
}

// isHidden reports whether an element is hidden from readers. Only the cheap,
// unambiguous signals — no CSS cascade, which would need a style engine.
func isHidden(n *html.Node) bool {
	for _, a := range n.Attr {
		switch strings.ToLower(a.Key) {
		case "hidden":
			return true
		case "aria-hidden":
			if strings.EqualFold(a.Val, "true") {
				return true
			}
		case "style":
			v := strings.ToLower(strings.ReplaceAll(a.Val, " ", ""))
			if strings.Contains(v, "display:none") || strings.Contains(v, "visibility:hidden") {
				return true
			}
		}
	}
	return false
}

// ── main-content extraction ────────────────────────────────────────────────

// scorableTags hold prose. Their text score is credited to their parent and
// grandparent, so the winner is the container that holds the most prose —
// rather than <body>, which trivially contains everything.
var scorableTags = map[atom.Atom]bool{
	atom.P:          true,
	atom.Pre:        true,
	atom.Blockquote: true,
	atom.Td:         true,
}

var positiveClassWords = []string{"article", "content", "post", "entry", "story", "main", "body", "markdown", "prose"}

var negativeClassWords = []string{"nav", "menu", "sidebar", "comment", "footer", "header", "banner", "advert", "sponsor", "promo", "share", "social", "related", "popup", "modal", "cookie", "breadcrumb", "pagination", "widget"}

// extractMainContent scores containers by the prose they hold and returns the
// best one, or nil when nothing scored.
func extractMainContent(doc *html.Node) *html.Node {
	scores := map[*html.Node]float64{}
	order := map[*html.Node]int{}
	seq := 0

	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if chromeTags[n.DataAtom] {
				return
			}
			if _, seen := order[n]; !seen {
				order[n] = seq
				seq++
			}
			if scorableTags[n.DataAtom] {
				if s, ok := proseScore(n); ok {
					if p := elementParent(n); p != nil {
						scores[p] += s
						if gp := elementParent(p); gp != nil {
							scores[gp] += s / 2
						}
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)

	var best *html.Node
	bestScore := 0.0
	for n, s := range scores {
		s *= 1 - linkDensity(n)
		s *= classIDWeight(n)
		// Map iteration is randomised; break ties by document order so the
		// same page always cleans to the same output.
		if s > bestScore || (s == bestScore && best != nil && order[n] < order[best]) {
			best, bestScore = n, s
		}
	}
	if bestScore <= 0 {
		return nil
	}
	return promoteToOutermost(best, scores)
}

// promoteToOutermost walks up from the winning candidate while each parent
// holds nearly as much prose, and returns the outermost one that still does.
//
// Without this, a reference-heavy article loses to one of its own sections:
// link density drags the full article's score down, while a code- or list-
// heavy section inside it takes almost no penalty and wins — so the page
// cleans to chapter 7 with the first six silently missing. Wikipedia does
// exactly this. The comparison uses RAW prose scores, since the whole point is
// that the parent's link density is not disqualifying.
func promoteToOutermost(best *html.Node, scores map[*html.Node]float64) *html.Node {
	const keepRatio = 0.5
	for {
		parent := elementParent(best)
		if parent == nil || scores[parent] < scores[best]*keepRatio {
			return best
		}
		best = parent
	}
}

// proseScore rates one prose element: a base point, a point per comma (a
// sentence-density proxy that works for CJK too), and a length term capped so
// one giant blob can't dominate. Short fragments don't score at all.
func proseScore(n *html.Node) (float64, bool) {
	text := strings.TrimSpace(nodeText(n, false))
	runes := len([]rune(text))
	if runes < 25 {
		return 0, false
	}
	s := 1.0
	s += float64(strings.Count(text, ",") + strings.Count(text, "，") + strings.Count(text, "、"))
	if l := float64(runes) / 100; l < 3 {
		s += l
	} else {
		s += 3
	}
	return s, true
}

// linkDensity is the share of a node's text that sits inside links. A high
// share means navigation or an index, not an article.
func linkDensity(n *html.Node) float64 {
	total := len([]rune(nodeText(n, false)))
	if total == 0 {
		return 0
	}
	linked := 0
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.ElementNode && x.DataAtom == atom.A {
			linked += len([]rune(nodeText(x, false)))
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	if linked > total {
		return 1
	}
	return float64(linked) / float64(total)
}

// classIDWeight nudges a candidate by what its class/id says it is. Multiplier,
// not an additive bonus, so it can't outvote actual text volume.
func classIDWeight(n *html.Node) float64 {
	var sb strings.Builder
	for _, a := range n.Attr {
		if k := strings.ToLower(a.Key); k == "class" || k == "id" || k == "role" {
			sb.WriteString(strings.ToLower(a.Val))
			sb.WriteByte(' ')
		}
	}
	s := sb.String()
	if s == "" {
		return 1
	}
	w := 1.0
	for _, word := range negativeClassWords {
		if strings.Contains(s, word) {
			w *= 0.3
			break
		}
	}
	for _, word := range positiveClassWords {
		if strings.Contains(s, word) {
			w *= 1.5
			break
		}
	}
	return w
}

func elementParent(n *html.Node) *html.Node {
	p := n.Parent
	for p != nil && p.Type != html.ElementNode {
		p = p.Parent
	}
	if p != nil && (p.DataAtom == atom.Html || p.DataAtom == atom.Body) {
		// Crediting <body> would make it win every page.
		return nil
	}
	return p
}

// nodeText concatenates descendant text. When skipChrome is set, nav/header/
// footer/aside subtrees are left out — matching what the extraction path will
// actually convert.
func nodeText(n *html.Node, skipChrome bool) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			sb.WriteString(x.Data)
			return
		}
		if x.Type == html.ElementNode && skipChrome && chromeTags[x.DataAtom] {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// ── document metadata ──────────────────────────────────────────────────────

func documentTitle(doc *html.Node) string {
	if t := findFirst(doc, atom.Title); t != nil {
		if s := squeezeSpace(nodeText(t, false)); s != "" {
			return s
		}
	}
	if h := findFirst(doc, atom.H1); h != nil {
		return squeezeSpace(nodeText(h, false))
	}
	return ""
}

// baseHref returns the document's <base href>, which by spec wins over the
// response URL for resolving relative links.
func baseHref(doc *html.Node) string {
	b := findFirst(doc, atom.Base)
	if b == nil {
		return ""
	}
	return strings.TrimSpace(attr(b, "href"))
}

// titleAlreadyLeads reports whether body already opens with a heading saying
// what the title says, so prepending it would just repeat. Only a real ATX
// heading counts: a first line that merely happens to contain the same words
// is body copy, and the page would end up with no heading at all.
func titleAlreadyLeads(body, title string) bool {
	first := strings.TrimSpace(body)
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if !strings.HasPrefix(first, "#") {
		return false
	}
	first = strings.TrimSpace(strings.TrimLeft(first, "#"))
	if first == "" {
		return false
	}
	a, b := strings.ToLower(first), strings.ToLower(title)
	return strings.Contains(a, b) || strings.Contains(b, a)
}

func findFirst(n *html.Node, a atom.Atom) *html.Node {
	if n.Type == html.ElementNode && n.DataAtom == a {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if got := findFirst(c, a); got != nil {
			return got
		}
	}
	return nil
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// ── HTML → Markdown ────────────────────────────────────────────────────────

type mdConv struct {
	base *url.URL
	// skipChrome drops nav/header/footer/aside. Set on the extraction path,
	// cleared on the whole-page fallback.
	skipChrome bool
}

// render converts n's children to Markdown. Block-level results are joined by
// blank lines; inline runs accumulate until a block boundary flushes them.
func (c *mdConv) render(n *html.Node) string {
	var blocks []string
	var inline strings.Builder

	flush := func() {
		if s := strings.TrimSpace(squeezeInline(inline.String())); s != "" {
			blocks = append(blocks, s)
		}
		inline.Reset()
	}

	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			inline.WriteString(ch.Data)
		case html.ElementNode:
			if c.skip(ch) {
				continue
			}
			if blk, ok := c.block(ch); ok {
				flush()
				if blk = strings.TrimRight(blk, "\n"); strings.TrimSpace(blk) != "" {
					blocks = append(blocks, blk)
				}
				continue
			}
			inline.WriteString(c.inline(ch))
		}
	}
	flush()
	return strings.Join(blocks, "\n\n")
}

func (c *mdConv) skip(n *html.Node) bool {
	return noiseTags[n.DataAtom] || (c.skipChrome && chromeTags[n.DataAtom])
}

// block renders block-level elements. ok=false means "not a block" and the
// caller treats it as inline.
func (c *mdConv) block(n *html.Node) (string, bool) {
	switch n.DataAtom {
	case atom.H1, atom.H2, atom.H3, atom.H4, atom.H5, atom.H6:
		level, _ := strconv.Atoi(strings.TrimPrefix(n.Data, "h"))
		if level < 1 || level > 6 {
			level = 1
		}
		text := strings.TrimSpace(squeezeInline(c.inlineChildren(n)))
		if text == "" {
			return "", true
		}
		return strings.Repeat("#", level) + " " + text, true

	case atom.P:
		return strings.TrimSpace(squeezeInline(c.inlineChildren(n))), true

	case atom.Br:
		return "", false // inline

	case atom.Hr:
		return "---", true

	case atom.Pre:
		return c.renderPre(n), true

	case atom.Blockquote:
		inner := strings.TrimSpace(c.render(n))
		if inner == "" {
			return "", true
		}
		return prefixLines(inner, "> "), true

	case atom.Ul, atom.Ol:
		return c.renderList(n), true

	case atom.Table:
		return c.renderTable(n), true

	case atom.Head:
		// <title>/<meta> are document metadata, not body copy. The title is
		// re-attached deliberately by cleanHTMLToMarkdown; emitting it here
		// too would put an unmarked duplicate at the top of every page.
		return "", true

	case atom.Div, atom.Section, atom.Article, atom.Main, atom.Figure,
		atom.Figcaption, atom.Details, atom.Summary, atom.Dl, atom.Dd, atom.Dt,
		atom.Address, atom.Fieldset, atom.Body, atom.Html:
		// Structural containers: pass through, keeping their children's blocks.
		return c.render(n), true

	case atom.Nav, atom.Header, atom.Footer, atom.Aside:
		// Only reachable on the whole-page fallback (skipChrome is false there).
		return c.render(n), true
	}
	return "", false
}

// inline renders inline elements to a Markdown fragment.
func (c *mdConv) inline(n *html.Node) string {
	switch n.DataAtom {
	case atom.A:
		text := strings.TrimSpace(squeezeInline(c.inlineChildren(n)))
		href := c.resolve(attr(n, "href"))
		if href == "" || text == "" {
			return text
		}
		return "[" + text + "](" + href + ")"

	case atom.Img:
		src := c.resolve(attr(n, "src"))
		if src == "" {
			return ""
		}
		return "![" + squeezeSpace(attr(n, "alt")) + "](" + src + ")"

	case atom.Br:
		return "\n"

	case atom.Strong, atom.B:
		inner := strings.TrimSpace(squeezeInline(c.inlineChildren(n)))
		if inner == "" {
			return ""
		}
		return "**" + inner + "**"

	case atom.Em, atom.I:
		inner := strings.TrimSpace(squeezeInline(c.inlineChildren(n)))
		if inner == "" {
			return ""
		}
		return "*" + inner + "*"

	case atom.Code, atom.Kbd, atom.Samp:
		inner := strings.TrimSpace(squeezeSpace(nodeText(n, c.skipChrome)))
		if inner == "" {
			return ""
		}
		return "`" + inner + "`"
	}
	return c.inlineChildren(n)
}

// inlineChildren walks children as inline content. A block-level element
// nested inside inline context (common in hand-written HTML) is rendered by
// the block path and spliced in surrounded by newlines.
func (c *mdConv) inlineChildren(n *html.Node) string {
	var sb strings.Builder
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		switch ch.Type {
		case html.TextNode:
			sb.WriteString(ch.Data)
		case html.ElementNode:
			if c.skip(ch) {
				continue
			}
			if blk, ok := c.block(ch); ok {
				if strings.TrimSpace(blk) != "" {
					sb.WriteString("\n" + strings.TrimSpace(blk) + "\n")
				}
				continue
			}
			sb.WriteString(c.inline(ch))
		}
	}
	return sb.String()
}

// renderPre emits a fenced code block, keeping the source's own whitespace and
// picking up a language hint from the usual language-xxx / lang-xxx classes.
func (c *mdConv) renderPre(n *html.Node) string {
	code := nodeText(n, false)
	code = strings.Trim(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	if strings.TrimSpace(code) == "" {
		return ""
	}
	lang := preLanguage(n)
	fence := "```"
	// A body containing ``` needs a longer fence to stay one block.
	for strings.Contains(code, fence) {
		fence += "`"
	}
	return fence + lang + "\n" + code + "\n" + fence
}

func preLanguage(n *html.Node) string {
	classes := attr(n, "class")
	if inner := findFirst(n, atom.Code); inner != nil {
		classes += " " + attr(inner, "class")
	}
	for _, f := range strings.Fields(strings.ToLower(classes)) {
		for _, prefix := range []string{"language-", "lang-", "highlight-source-"} {
			if strings.HasPrefix(f, prefix) {
				if lang := strings.TrimPrefix(f, prefix); lang != "" {
					return lang
				}
			}
		}
	}
	return ""
}

func (c *mdConv) renderList(n *html.Node) string {
	ordered := n.DataAtom == atom.Ol
	start := 1
	if s := attr(n, "start"); s != "" {
		if v, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
			start = v
		}
	}
	var lines []string
	i := start
	for li := n.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode || li.DataAtom != atom.Li {
			continue
		}
		content := strings.TrimSpace(c.render(li))
		if content == "" {
			continue
		}
		marker := "- "
		if ordered {
			marker = strconv.Itoa(i) + ". "
			i++
		}
		// First line carries the marker; continuation lines (nested lists,
		// extra paragraphs) indent to match.
		indented := prefixLines(content, strings.Repeat(" ", len(marker)))
		lines = append(lines, marker+strings.TrimPrefix(indented, strings.Repeat(" ", len(marker))))
	}
	return strings.Join(lines, "\n")
}

// renderTable emits GFM for a regular table. Anything with colspan/rowspan, a
// nested table, or ragged rows degrades to one line per row — content intact,
// alignment lost. Handling the irregular cases properly is where table
// converters go to die; the degraded form is honest and cheap.
func (c *mdConv) renderTable(n *html.Node) string {
	rows := c.tableRows(n)
	if len(rows) == 0 {
		return ""
	}
	regular := !tableIsIrregular(n)
	width := len(rows[0])
	for _, r := range rows {
		if len(r) != width || width == 0 {
			regular = false
			break
		}
	}
	if !regular {
		var lines []string
		for _, r := range rows {
			lines = append(lines, strings.Join(r, " | "))
		}
		return strings.Join(lines, "\n")
	}

	var sb strings.Builder
	sb.WriteString("| " + strings.Join(rows[0], " | ") + " |\n")
	sb.WriteString("|" + strings.Repeat(" --- |", width) + "\n")
	for _, r := range rows[1:] {
		sb.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func (c *mdConv) tableRows(n *html.Node) [][]string {
	var rows [][]string
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			switch ch.DataAtom {
			case atom.Thead, atom.Tbody, atom.Tfoot:
				walk(ch)
			case atom.Tr:
				var cells []string
				for cell := ch.FirstChild; cell != nil; cell = cell.NextSibling {
					if cell.Type != html.ElementNode {
						continue
					}
					if cell.DataAtom != atom.Td && cell.DataAtom != atom.Th {
						continue
					}
					text := strings.TrimSpace(squeezeInline(c.inlineChildren(cell)))
					// A literal pipe or newline would break the row apart.
					text = strings.ReplaceAll(text, "|", "\\|")
					text = strings.ReplaceAll(text, "\n", " ")
					cells = append(cells, text)
				}
				if len(cells) > 0 {
					rows = append(rows, cells)
				}
			}
		}
	}
	walk(n)
	return rows
}

func tableIsIrregular(n *html.Node) bool {
	irregular := false
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for ch := x.FirstChild; ch != nil; ch = ch.NextSibling {
			if ch.Type != html.ElementNode {
				continue
			}
			if ch.DataAtom == atom.Table {
				irregular = true
				return
			}
			if ch.DataAtom == atom.Td || ch.DataAtom == atom.Th {
				if attr(ch, "colspan") != "" || attr(ch, "rowspan") != "" {
					irregular = true
					return
				}
			}
			walk(ch)
			if irregular {
				return
			}
		}
	}
	walk(n)
	return irregular
}

// resolve turns a relative href/src into an absolute URL against the page's
// base. Non-navigable schemes and unparseable values return "" so the caller
// keeps just the text.
func (c *mdConv) resolve(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "#") {
		return ""
	}
	switch strings.ToLower(schemeOf(raw)) {
	case "javascript", "data", "about", "vbscript":
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if c.base == nil {
		if !u.IsAbs() {
			return ""
		}
		return u.String()
	}
	return c.base.ResolveReference(u).String()
}

func schemeOf(raw string) string {
	i := strings.IndexByte(raw, ':')
	if i <= 0 {
		return ""
	}
	if j := strings.IndexAny(raw, "/?#"); j >= 0 && j < i {
		return ""
	}
	return raw[:i]
}

// ── text helpers ───────────────────────────────────────────────────────────

// squeezeInline collapses runs of spaces/tabs to one space while keeping
// explicit newlines (from <br>) and trimming each line.
func squeezeInline(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimSpace(squeezeSpace(ln))
	}
	return strings.Join(lines, "\n")
}

// squeezeSpace collapses every whitespace run (newlines included) to one space.
// U+00A0 counts: &nbsp; is everywhere in real HTML and would otherwise survive
// into the output as a stray character that looks like a space but isn't.
func squeezeSpace(s string) string {
	var sb strings.Builder
	sb.Grow(len(s))
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' || r == '\v' || r == '\u00a0' {
			space = true
			continue
		}
		if space && sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		space = false
		sb.WriteRune(r)
	}
	return sb.String()
}

func prefixLines(s, prefix string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			lines[i] = strings.TrimRight(prefix, " ")
			continue
		}
		lines[i] = prefix + ln
	}
	return strings.Join(lines, "\n")
}

// collapseBlankRuns squashes three-or-more newlines down to a paragraph break.
func collapseBlankRuns(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

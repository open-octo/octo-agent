package telegram

import (
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// markdown.go converts agent-authored Markdown into the small HTML subset
// Telegram's Bot API accepts under parse_mode=HTML.
//
// Why HTML instead of hand-rolling MarkdownV2's escaping (#1119): MarkdownV2
// requires escaping ~18 reserved characters throughout the text, and any
// single miss makes Telegram reject the *entire* chunk — exactly the "one
// unbalanced '*' loses all formatting" failure mode this issue is about.
// HTML mode only requires escaping &, <, > in literal text. Because we parse
// the real Markdown with goldmark first (already an indirect dependency via
// glamour, used for TUI rendering in cmd/octo/markdown.go) rather than
// pattern-matching on the raw string, a stray '*' in the model's output is
// just literal text to us — never a broken parse.
var telegramMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.Strikethrough, extension.Linkify),
)

// renderTelegramHTML converts src into Telegram-safe HTML. It never errors:
// anything goldmark doesn't recognize as a supported construct is emitted as
// escaped literal text, so worst case the output is over-literal, never
// malformed.
func renderTelegramHTML(src string) string {
	source := []byte(src)
	doc := telegramMarkdown.Parser().Parse(text.NewReader(source))
	return strings.TrimSpace(renderBlockChildren(doc, source))
}

func renderBlockChildren(n ast.Node, source []byte) string {
	var blocks []string
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if s := renderBlock(c, source); s != "" {
			blocks = append(blocks, s)
		}
	}
	return strings.Join(blocks, "\n\n")
}

// renderBlock renders a single block-level node. Telegram's HTML subset has
// no block tags beyond <blockquote> and <pre>, so headings/thematic breaks
// degrade to plain text conventions rather than being dropped.
func renderBlock(n ast.Node, source []byte) string {
	switch node := n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return renderInlineChildren(n, source)
	case *ast.Heading:
		return "<b>" + renderInlineChildren(n, source) + "</b>"
	case *ast.Blockquote:
		return "<blockquote>" + renderBlockChildren(n, source) + "</blockquote>"
	case *ast.List:
		return renderList(node, source)
	case *ast.CodeBlock:
		return renderCodeBlock("", node.Lines().Value(source))
	case *ast.FencedCodeBlock:
		return renderCodeBlock(string(node.Language(source)), node.Lines().Value(source))
	case *ast.ThematicBreak:
		return "──────────"
	case *ast.HTMLBlock:
		// The model's raw HTML is untrusted input to Telegram's parser
		// (unsupported tags make the whole message fail); render it as
		// literal escaped text instead of passing it through.
		return htmlEscapeBytes(node.Lines().Value(source))
	default:
		return renderBlockChildren(n, source)
	}
}

func renderList(list *ast.List, source []byte) string {
	var items []string
	for i, item := 0, list.FirstChild(); item != nil; item = item.NextSibling() {
		li, ok := item.(*ast.ListItem)
		if !ok {
			continue
		}
		marker := "• "
		if list.IsOrdered() {
			marker = strconv.Itoa(list.Start+i) + ". "
		}
		items = append(items, marker+strings.TrimSpace(renderBlockChildren(li, source)))
		i++
	}
	return strings.Join(items, "\n")
}

func renderCodeBlock(lang string, code []byte) string {
	body := strings.TrimRight(htmlEscapeBytes(code), "\n")
	if lang == "" {
		return "<pre><code>" + body + "</code></pre>"
	}
	return `<pre><code class="language-` + htmlEscapeString(lang) + `">` + body + "</code></pre>"
}

func renderInlineChildren(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderInline(&b, c, source)
	}
	return b.String()
}

func renderInline(b *strings.Builder, n ast.Node, source []byte) {
	switch node := n.(type) {
	case *ast.Text:
		b.WriteString(htmlEscapeBytes(node.Segment.Value(source)))
		if node.SoftLineBreak() || node.HardLineBreak() {
			b.WriteString("\n")
		}
	case *ast.String:
		b.WriteString(htmlEscapeBytes(node.Value))
	case *ast.Emphasis:
		tag := "i"
		if node.Level >= 2 {
			tag = "b"
		}
		b.WriteString("<" + tag + ">")
		renderInlineInto(b, n, source)
		b.WriteString("</" + tag + ">")
	case *ast.CodeSpan:
		b.WriteString("<code>")
		b.WriteString(htmlEscapeString(codeSpanText(node, source)))
		b.WriteString("</code>")
	case *ast.Link:
		b.WriteString(`<a href="`)
		b.WriteString(htmlEscapeBytes(util.URLEscape(node.Destination, true)))
		b.WriteString(`">`)
		renderInlineInto(b, n, source)
		b.WriteString("</a>")
	case *ast.AutoLink:
		b.WriteString(`<a href="`)
		b.WriteString(htmlEscapeBytes(util.URLEscape(node.URL(source), true)))
		b.WriteString(`">`)
		b.WriteString(htmlEscapeBytes(node.Label(source)))
		b.WriteString("</a>")
	case *ast.Image:
		// Telegram HTML has no inline image tag; keep the alt text and
		// surface the URL rather than silently dropping the reference.
		b.WriteString(htmlEscapeString(plainText(n, source)))
		if len(node.Destination) > 0 {
			b.WriteString(" (")
			b.WriteString(htmlEscapeBytes(node.Destination))
			b.WriteString(")")
		}
	case *ast.RawHTML:
		b.WriteString(htmlEscapeBytes(node.Segments.Value(source)))
	case *east.Strikethrough:
		b.WriteString("<s>")
		renderInlineInto(b, n, source)
		b.WriteString("</s>")
	default:
		renderInlineInto(b, n, source)
	}
}

func renderInlineInto(b *strings.Builder, n ast.Node, source []byte) {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		renderInline(b, c, source)
	}
}

// codeSpanText concatenates a CodeSpan's text children. Per CommonMark, code
// spans can't contain nested inline formatting, only Text nodes.
func codeSpanText(n *ast.CodeSpan, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(source))
		}
	}
	return b.String()
}

// plainText flattens a node's text content, ignoring formatting — used for
// image alt text where Telegram has nowhere to render nested markup.
func plainText(n ast.Node, source []byte) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch t := c.(type) {
		case *ast.Text:
			b.Write(t.Segment.Value(source))
		case *ast.String:
			b.Write(t.Value)
		default:
			b.WriteString(plainText(c, source))
		}
	}
	return b.String()
}

func htmlEscapeBytes(v []byte) string {
	return string(util.EscapeHTML(v))
}

func htmlEscapeString(s string) string {
	return htmlEscapeBytes([]byte(s))
}

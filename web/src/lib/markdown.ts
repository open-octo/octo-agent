import { marked, Renderer, type Tokens } from "marked"
import DOMPurify from "dompurify"
import hljs from "highlight.js/lib/core"
import javascript from "highlight.js/lib/languages/javascript"
import typescript from "highlight.js/lib/languages/typescript"
import go from "highlight.js/lib/languages/go"
import python from "highlight.js/lib/languages/python"
import bash from "highlight.js/lib/languages/bash"
import json from "highlight.js/lib/languages/json"
import xml from "highlight.js/lib/languages/xml"
// Side-effect import: bundles the GitHub Dark highlight.js CSS theme so that
// hljs-* class names (hljs-keyword, hljs-string, etc.) are styled in the main
// DOM (ChatView, ProfileView, etc.). Without this, highlight() runs and
// produces the right HTML spans but the colors are invisible.
import "highlight.js/styles/github-dark.css"

hljs.registerLanguage("javascript", javascript)
hljs.registerLanguage("typescript", typescript)
hljs.registerLanguage("go", go)
hljs.registerLanguage("python", python)
hljs.registerLanguage("bash", bash)
hljs.registerLanguage("json", json)
hljs.registerLanguage("xml", xml)

export function escapeHtml(s: string): string {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;")
}

function isSafeHref(href: string): boolean {
  if (!href) return false
  const lower = href.trim().toLowerCase()
  return lower.startsWith("http://") || lower.startsWith("https://") || lower.startsWith("mailto:") || lower.startsWith("tel:")
}

// Custom renderer, installed ONCE at module scope. marked.use() re-wraps the
// already-installed renderer methods in a fresh closure layer on every call,
// so installing per render() grew an unbounded wrapper chain on the global
// marked instance — a monotonic memory leak in streaming chat views (#2089).
// None of the overrides capture per-call state, so a single install is
// semantically identical.
const renderer = new Renderer()

renderer.code = function ({ text: codeText, lang }: { text: string; lang?: string }) {
  const language = lang && hljs.getLanguage(lang) ? lang : "plaintext"
  let highlighted: string
  if (language !== "plaintext") {
    highlighted = hljs.highlight(codeText, { language }).value
  } else {
    highlighted = escapeHtml(codeText)
  }
  return `<div class="code-block">
  <div class="code-header">
    <span class="code-lang">${escapeHtml(language)}</span>
    <button class="copy-btn">Copy</button>
  </div>
  <pre><code class="hljs language-${escapeHtml(language)}">${highlighted}</code></pre>
</div>`
}

renderer.link = function ({ href, title, tokens }: Tokens.Link) {
  // Only allow safe URL schemes; strip everything else.
  const safe = isSafeHref(href)
  const titleAttr = title ? ` title="${escapeHtml(title)}"` : ""
  // Render the label from the token's parsed inline children — token.text is
  // the raw markdown source (same bug class as blockquote below), so escaping
  // it leaked literal ** and backticks into every formatted link label.
  return `<a href="${safe ? escapeHtml(href) : ""}"${titleAttr} target="_blank" rel="noopener">${this.parser.parseInline(tokens)}</a>`
}

// Render the quote's children, don't interpolate token.text — that field is
// the raw markdown source, so `> quoted **bold**` reached the DOM with the
// asterisks intact and every link, inline code span and nested list inside a
// quote came out as literal text. The default renderer parses token.tokens;
// this override exists only to add the md-bq class, so it has to do the same.
renderer.blockquote = function ({ tokens }: Tokens.Blockquote) {
  return `<blockquote class="md-bq">${this.parser.parse(tokens)}</blockquote>`
}

// Wrap marked's default table in a horizontal-scroll container so wide
// tables don't blow out the flex bubble / .md-content column. The table
// itself stays a real <table> (reusing the default tablecell/tablerow), so
// thead/tbody columns keep aligning and per-cell alignment survives.
renderer.table = function (token: Tokens.Table) {
  let header = ""
  for (const cell of token.header) header += this.tablecell(cell)
  header = this.tablerow({ text: header })
  let body = ""
  for (const row of token.rows) {
    let cells = ""
    for (const cell of row) cells += this.tablecell(cell)
    body += this.tablerow({ text: cells })
  }
  if (body) body = `<tbody>${body}</tbody>`
  return `<div class="table-scroll"><table><thead>${header}</thead>${body}</table></div>`
}

// Raw HTML in reply text is shown as text, never handed to the DOM. marked
// routes both block-level and inline HTML tokens through renderer.html, and
// letting them pass meant a model writing a .svelte/.html file inline (no code
// fence — some harnesses narrate and paste at once) put real nodes into the
// chat: a GitDiffModal.svelte source dropped a live <div class="backdrop">/
// <div class="modal"> plus its <style> into the bubble, where the app's global
// CSS made it a fixed full-screen overlay covering the page. DOMPurify does not
// stop that — it strips scripts, event handlers and javascript: URLs, not
// layout markup or CSS — so the escape has to happen at the renderer.
// Returning false here would make marked.use() fall back to the default
// pass-through renderer; an escaped string (empty included) never does.
renderer.html = function ({ text }: Tokens.HTML | Tokens.Tag) {
  return escapeHtml(text)
}

marked.use({ renderer })

export function renderMarkdown(text: string, showReasoning = true): string {
  if (!text) return ""

  // 1. Extract <think>...</think> blocks, replace with placeholders
  const thinkSegments: string[] = []
  const PLACEHOLDER = "\x00THINK_BLOCK_\x00"

  const textWithPlaceholders = text.replace(/<think>([\s\S]*?)<\/think>/g, (_match, content: string) => {
    if (!showReasoning) return ""
    const index = thinkSegments.length
    thinkSegments.push(content)
    return `${PLACEHOLDER}${index}\x00`
  })

  // 2. Parse remaining text with marked
  const renderedMain = marked.parse(textWithPlaceholders) as string

  // 3. Build think block HTML for each segment
  const thinkBlocks = thinkSegments.map((segment) => {
    const renderedSegment = DOMPurify.sanitize(marked.parse(segment) as string)
    return `<details class="think-block"><summary class="think-summary"><iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>Thoughts</summary><div class="think-body">${renderedSegment}</div></details>`
  })

  // 4. Replace placeholders with think block HTML, then sanitize the combined
  //    output. DOMPurify removes dangerous markup (script, event handlers,
  //    javascript: URLs) while preserving the allowed structure.
  const combined = renderedMain.replace(
    new RegExp(`${PLACEHOLDER.replace(/\x00/g, "\\x00")}(\\d+)\\x00`, "g"),
    (_match, indexStr: string) => {
      return thinkBlocks[parseInt(indexStr, 10)] ?? ""
    }
  )

  // 5. Return combined sanitized HTML
  return DOMPurify.sanitize(combined, { ADD_ATTR: ["target"] })
}

export function setupCopyButtons(el: HTMLElement): { destroy: () => void } {
  function onClick(e: MouseEvent) {
    const btn = (e.target as HTMLElement).closest<HTMLButtonElement>("button.copy-btn")
    if (!btn || !el.contains(btn)) return
    const block = btn.closest(".code-block")
    const code = block?.querySelector("pre code")
    const content = code?.textContent ?? ""
    navigator.clipboard.writeText(content).then(() => {
      const original = btn.textContent
      btn.textContent = "Copied!"
      setTimeout(() => {
        btn.textContent = original
      }, 1500)
    })
  }
  el.addEventListener("click", onClick)
  return { destroy: () => el.removeEventListener("click", onClick) }
}

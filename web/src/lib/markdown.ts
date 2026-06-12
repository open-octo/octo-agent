import { marked, Renderer } from "marked"
import hljs from "highlight.js/lib/core"
import javascript from "highlight.js/lib/languages/javascript"
import typescript from "highlight.js/lib/languages/typescript"
import go from "highlight.js/lib/languages/go"
import python from "highlight.js/lib/languages/python"
import bash from "highlight.js/lib/languages/bash"
import json from "highlight.js/lib/languages/json"
import xml from "highlight.js/lib/languages/xml"

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

export function renderMarkdown(text: string): string {
  if (!text) return ""

  // 1. Extract <think>...</think> blocks, replace with placeholders
  const thinkSegments: string[] = []
  const PLACEHOLDER = "\x00THINK_BLOCK_\x00"

  const textWithPlaceholders = text.replace(/<think>([\s\S]*?)<\/think>/g, (_match, content: string) => {
    const index = thinkSegments.length
    thinkSegments.push(content)
    return `${PLACEHOLDER}${index}\x00`
  })

  // 3. Set up custom renderer
  const renderer = new Renderer()

  renderer.code = function ({ text: codeText, lang }: { text: string; lang?: string }) {
    const language = lang && hljs.getLanguage(lang) ? lang : "plaintext"
    let highlighted: string
    if (language !== "plaintext") {
      highlighted = hljs.highlight(codeText, { language }).value
    } else {
      highlighted = escapeHtml(codeText)
    }
    const rawForCopy = escapeHtml(codeText)
    return `<div class="code-block">
  <div class="code-header">
    <span class="code-lang">${escapeHtml(language)}</span>
    <button class="copy-btn" data-copy="${rawForCopy}">Copy</button>
  </div>
  <pre><code class="hljs language-${escapeHtml(language)}">${highlighted}</code></pre>
</div>`
  }

  renderer.link = function ({ href, title, text }: { href: string; title?: string | null; text: string }) {
    const titleAttr = title ? ` title="${escapeHtml(title)}"` : ""
    return `<a href="${href}"${titleAttr} target="_blank" rel="noopener">${text}</a>`
  }

  renderer.blockquote = function ({ text: bqText }: { text: string }) {
    return `<blockquote class="md-bq">${bqText}</blockquote>`
  }

  marked.use({ renderer })

  // 3. Parse remaining text with marked
  const renderedMain = marked.parse(textWithPlaceholders) as string

  // 2. Build think block HTML for each segment
  const thinkBlocks = thinkSegments.map((segment) => {
    const renderedSegment = marked.parse(segment) as string
    return `<details class="think-block"><summary class="think-summary"><iconify-icon icon="ant-design:bulb-outlined" width="13"></iconify-icon>Thoughts</summary><div class="think-body">${renderedSegment}</div></details>`
  })

  // 4. Replace placeholders with think block HTML
  const result = renderedMain.replace(
    new RegExp(`${PLACEHOLDER.replace(/\x00/g, "\\x00")}(\\d+)\\x00`, "g"),
    (_match, indexStr: string) => {
      return thinkBlocks[parseInt(indexStr, 10)] ?? ""
    }
  )

  // 5. Return combined HTML
  return result
}

export function setupCopyButtons(el: HTMLElement): void {
  const buttons = el.querySelectorAll<HTMLButtonElement>("button[data-copy]")
  buttons.forEach((btn) => {
    btn.addEventListener("click", () => {
      const content = btn.dataset.copy ?? ""
      navigator.clipboard.writeText(content).then(() => {
        const original = btn.textContent
        btn.textContent = "Copied!"
        setTimeout(() => {
          btn.textContent = original
        }, 1500)
      })
    })
  })
}

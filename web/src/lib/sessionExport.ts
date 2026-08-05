import DOMPurify from 'dompurify'
import { escapeHtml, renderMarkdown } from './markdown'

export type ExportEvent = {
  type?: string
  content?: string
  thinking?: string
  text?: string
  tool_name?: string
  name?: string
}

export type ExportableMessage = {
  id: string
  type: 'user' | 'assistant'
  content?: string
  thinking?: string
}

type MarkdownOptions = {
  title: string
  includeTools?: boolean
}

type HtmlOptions = {
  title: string
  exportedAt?: Date | string | number
  locale?: string
  watermark?: string
}

export function sanitizeExportTitle(title: string): string {
  return (title || 'session').replace(/[^\w.-]+/g, '_')
}

export function filterEventsBySelectedMessages(
  events: ExportEvent[],
  localMessages: Array<{ id: string; type: string }>,
  selectedIds: Set<string>
): ExportEvent[] {
  const localUA = localMessages.filter((m) => m.type === 'user' || m.type === 'assistant')
  let uaIdx = 0
  const result: ExportEvent[] = []

  for (const ev of events) {
    const etype = ev.type ?? ''
    const isEmptyAssistant =
      etype === 'assistant_message' &&
      !(ev.content ?? '').trim() &&
      !(ev.thinking ?? '').trim()

    if ((etype === 'history_user_message' || etype === 'assistant_message') && !isEmptyAssistant) {
      const local = localUA[uaIdx]
      uaIdx++
      if (local && selectedIds.has(local.id)) result.push(ev)
    } else {
      result.push(ev)
    }
  }

  return result
}

export function pickSelectedMessages(
  messages: ExportableMessage[],
  selectedIds: Set<string>
): ExportableMessage[] {
  return messages.filter((msg) => selectedIds.has(msg.id))
}

export function exportAsMarkdown(
  events: ExportEvent[],
  options: MarkdownOptions
): { content: string; omittedToolEvents: boolean; filename: string; mime: string } {
  const lines: string[] = [`# ${options.title}`, '']
  let omittedToolEvents = false

  for (const ev of events) {
    const type = ev.type ?? ''
    if (type === 'history_user_message') {
      lines.push('## You', '')
      lines.push(ev.content ?? '', '')
    } else if (type === 'assistant_message') {
      lines.push('## Octo', '')
      if (ev.thinking) {
        lines.push('<details><summary>Thoughts</summary>', '', ev.thinking, '', '</details>', '')
      }
      lines.push(ev.content ?? '', '')
    } else if (type === 'thinking' && ev.text) {
      lines.push('<!-- Thinking -->', ev.text, '')
    } else if (type === 'tool_call' || type === 'tool_result') {
      if (options.includeTools) {
        if (type === 'tool_call') {
          lines.push(`- **Tool call**: ${ev.tool_name ?? ev.name ?? 'unknown'}`, '')
        } else {
          lines.push(
            `- **Tool result**: ${typeof ev.content === 'string' ? ev.content.slice(0, 500) : '(non-text result)'}`,
            ''
          )
        }
      } else {
        omittedToolEvents = true
      }
    }
  }

  const filename = `${sanitizeExportTitle(options.title)}.md`
  return {
    content: lines.join('\n'),
    omittedToolEvents,
    filename,
    mime: 'text/markdown',
  }
}

export function exportAsJSON(
  events: ExportEvent[],
  title: string
): { content: string; filename: string; mime: string } {
  return {
    content: JSON.stringify(events, null, 2),
    filename: `${sanitizeExportTitle(title)}.json`,
    mime: 'application/json',
  }
}

function nl2br(text: string): string {
  return escapeHtml(text).replace(/\n/g, '<br>')
}

function formatExportTime(value: Date | string | number | undefined, locale: string): string {
  const date = value instanceof Date ? value : new Date(value ?? Date.now())
  return new Intl.DateTimeFormat(locale.startsWith('zh') ? 'zh-CN' : 'en-US', {
    dateStyle: 'medium',
    timeStyle: 'short',
  }).format(date)
}

function conversationStyles(): string {
  return `
    :root {
      color-scheme: light;
      --bg: #ffffff;
      --panel: #f9fafb;
      --panel-border: #e5e7eb;
      --bubble-user: #eff6ff;
      --bubble-assistant: #f8fafc;
      --bubble-thinking: #f1f5f9;
      --text: #111827;
      --muted: #64748b;
      --accent: #2563eb;
      --code-bg: #f1f5f9;
      --shadow: 0 2px 8px rgba(0, 0, 0, 0.06);
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      padding: 32px 20px 40px;
      background: #f8fafc;
      color: var(--text);
      font: 14px/1.6 Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    .export-shell {
      width: min(960px, 100%);
      margin: 0 auto;
    }
    .export-header {
      margin-bottom: 20px;
      padding: 20px 24px;
      border: 1px solid var(--panel-border);
      border-radius: 18px;
      background: var(--bg);
      box-shadow: var(--shadow);
    }
    .export-title {
      margin: 0;
      font-size: 24px;
      line-height: 1.25;
      color: var(--text);
    }
    .export-meta {
      margin-top: 8px;
      color: var(--muted);
      font-size: 13px;
    }
    .conversation {
      display: flex;
      flex-direction: column;
      gap: 14px;
    }
    .msg {
      display: flex;
      flex-direction: column;
      gap: 8px;
      padding: 16px 18px;
      border: 1px solid var(--panel-border);
      border-radius: 18px;
      background: var(--panel);
      box-shadow: var(--shadow);
    }
    .msg.user {
      background: var(--bubble-user);
    }
    .msg.assistant {
      background: var(--bubble-assistant);
    }
    .msg-head {
      display: flex;
      align-items: center;
      justify-content: space-between;
      gap: 12px;
      color: var(--muted);
      font-size: 12px;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .msg-index {
      color: var(--accent);
      font-weight: 700;
    }
    .msg-label {
      font-weight: 700;
    }
    .msg-body {
      color: var(--text);
      min-width: 0;
      overflow-wrap: anywhere;
    }
    .msg-body > :first-child,
    .msg-thinking > :first-child {
      margin-top: 0;
    }
    .msg-body > :last-child,
    .msg-thinking > :last-child {
      margin-bottom: 0;
    }
    .msg-thinking-wrap {
      border-top: 1px solid var(--panel-border);
      padding-top: 10px;
    }
    .msg-thinking-label {
      margin-bottom: 8px;
      color: var(--muted);
      font-size: 12px;
      font-weight: 700;
      text-transform: uppercase;
      letter-spacing: 0.08em;
    }
    .msg-thinking {
      padding: 12px 14px;
      border: 1px solid var(--panel-border);
      border-radius: 14px;
      background: var(--bubble-thinking);
    }
    p, ul, ol, pre, blockquote {
      margin: 0 0 12px;
    }
    pre {
      overflow: auto;
      padding: 14px;
      border-radius: 14px;
      background: var(--code-bg);
      border: 1px solid var(--panel-border);
      white-space: pre-wrap;
      word-break: break-word;
    }
    code {
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
    }
    .code-block {
      border: 1px solid var(--panel-border);
      border-radius: 14px;
      overflow: hidden;
      background: var(--code-bg);
    }
    .code-header {
      display: flex;
      align-items: center;
      justify-content: space-between;
      padding: 8px 12px;
      background: #e2e8f0;
      color: var(--muted);
      font-size: 12px;
    }
    .copy-btn {
      display: none;
    }
    a {
      color: #2563eb;
      text-decoration: none;
    }
    blockquote {
      margin-left: 0;
      padding-left: 14px;
      border-left: 3px solid rgba(37, 99, 235, 0.45);
      color: #475569;
    }
  `
}

function messageLabel(type: 'user' | 'assistant'): string {
  return type === 'user' ? 'You' : 'Octo'
}

export function buildConversationMarkup(messages: ExportableMessage[]): string {
  return messages
    .map((msg, index) => {
      const contentHtml =
        msg.type === 'assistant'
          ? renderMarkdown(msg.content ?? '', true)
          : `<div>${nl2br(msg.content ?? '')}</div>`
      const thinkingHtml = msg.thinking
        ? `
          <div class="msg-thinking-wrap">
            <div class="msg-thinking-label">Thoughts</div>
            <div class="msg-thinking">${renderMarkdown(msg.thinking, true)}</div>
          </div>
        `
        : ''

      return `
        <article class="msg ${msg.type}">
          <div class="msg-head">
            <span class="msg-index">#${index + 1}</span>
            <span class="msg-label">${messageLabel(msg.type)}</span>
          </div>
          <div class="msg-body">${contentHtml}</div>
          ${msg.type === 'assistant' ? thinkingHtml : ''}
        </article>
      `
    })
    .join('\n')
}

export function exportAsHTML(
  messages: ExportableMessage[],
  options: HtmlOptions
): { content: string; filename: string; mime: string } {
  const locale = options.locale || 'en'
  const exportTime = formatExportTime(options.exportedAt, locale)
  const watermark = options.watermark || 'Exported from octo'
  const conversation = buildConversationMarkup(messages)
  const dirty = `<!DOCTYPE html>
<html lang="${escapeHtml(locale.startsWith('zh') ? 'zh' : 'en')}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>${escapeHtml(options.title)} - Octo Export</title>
  <style>${conversationStyles()}</style>
</head>
<body>
  <main class="export-shell">
    <header class="export-header">
      <h1 class="export-title">${escapeHtml(options.title)}</h1>
      <div class="export-meta">${escapeHtml(exportTime)} · ${escapeHtml(watermark)}</div>
    </header>
    <section class="conversation">
      ${conversation}
    </section>
  </main>
</body>
</html>`

  const content = DOMPurify.sanitize(dirty, {
    WHOLE_DOCUMENT: true,
    ADD_ATTR: ['target', 'rel', 'class'],
  })

  return {
    content,
    filename: `${sanitizeExportTitle(options.title)}.html`,
    mime: 'text/html',
  }
}

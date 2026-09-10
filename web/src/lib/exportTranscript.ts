// Shared pieces of the transcript export, kept out of ChatView so the rules
// every format has to agree on are testable on their own.
//
// The export bar offers one "include tool calls" checkbox for five formats.
// applyToolToggle is what that checkbox means: MD, JSON, PNG and HTML all run
// their events through it, and PDF reaches the same result through the
// .print-omit-tools rule in app.css because it prints the live DOM.
import { renderMarkdown, escapeHtml } from './markdown'

export function isToolEvent(ev: any): boolean {
  const type = ev?.type ?? ''
  return type === 'tool_call' || type === 'tool_result'
}

// omittedTools reports that something was actually dropped, which is what
// earns the "tool calls not included" toast — a transcript with no tool calls
// in it shouldn't announce their absence.
export function applyToolToggle(
  events: any[],
  includeTools: boolean,
): { events: any[]; omittedTools: boolean } {
  if (includeTools) return { events, omittedTools: false }
  const kept = events.filter((ev) => !isToolEvent(ev))
  return { events: kept, omittedTools: kept.length !== events.length }
}

// A tool result can be a whole terminal dump. Both the Markdown export and
// this one cut it at the same point: PNG renders every line of it as pixels,
// and neither document has anywhere to scroll.
export const TOOL_RESULT_CHARS = 500

// Inner conversation markup for the HTML and PNG exports. Tool events arrive
// here only when the toggle is on — applyToolToggle has dropped them
// otherwise — and render as flat cards rather than the live UI's expandable
// groups, since the exported document carries no scripting.
export function buildExportConversation(events: any[]): string {
  return events
    .map((ev) => {
      const type = ev.type ?? ''
      if (type === 'history_user_message') {
        return `<article class="msg user"><div class="msg-head"><span class="msg-label">You</span></div><div class="msg-body">${escapeHtml(ev.content ?? '').replace(/\n/g, '<br>')}</div></article>`
      }
      if (type === 'assistant_message') {
        const thinking = ev.thinking
          ? `<div class="msg-thinking-wrap"><div class="msg-thinking-label">Thoughts</div><div class="msg-thinking">${renderMarkdown(ev.thinking, true)}</div></div>`
          : ''
        return `<article class="msg assistant"><div class="msg-head"><span class="msg-label">Octo</span></div><div class="msg-body">${renderMarkdown(ev.content ?? '', true)}</div>${thinking}</article>`
      }
      if (type === 'tool_call') {
        const name = escapeHtml(String(ev.tool_name ?? ev.name ?? 'unknown'))
        return `<article class="msg tool"><div class="msg-head"><span class="msg-label">Tool call</span><span class="tool-name">${name}</span></div></article>`
      }
      if (type === 'tool_result') {
        const body = typeof ev.result === 'string'
          ? escapeHtml(ev.result.slice(0, TOOL_RESULT_CHARS))
          : '(non-text result)'
        return `<article class="msg tool"><div class="msg-head"><span class="msg-label">Tool result</span></div><pre class="tool-result">${body}</pre></article>`
      }
      return ''
    })
    .filter(Boolean)
    .join('\n')
}

// Styles for the exported document. They live beside buildExportConversation
// because the two have to move together: the tool cards below are markup this
// module emits, and the export is a standalone file with no other stylesheet.
export function exportConversationStyles(): string {
  return `
    :root { color-scheme: light; }
    * { box-sizing: border-box; }
    body {
      margin: 0; padding: 32px 20px 40px;
      background: #f8fafc; color: #111827;
      font: 14px/1.6 Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
    }
    .export-shell { width: min(960px, 100%); margin: 0 auto; }
    .export-header {
      margin-bottom: 20px; padding: 20px 24px;
      border: 1px solid #e5e7eb; border-radius: 18px;
      background: #ffffff; box-shadow: 0 2px 8px rgba(0,0,0,0.06);
    }
    .export-title { margin: 0; font-size: 24px; line-height: 1.25; }
    .export-meta { margin-top: 8px; color: #64748b; font-size: 13px; }
    .conversation { display: flex; flex-direction: column; gap: 14px; }
    .msg {
      display: flex; flex-direction: column; gap: 8px;
      padding: 16px 18px; border: 1px solid #e5e7eb; border-radius: 18px;
      background: #f9fafb; box-shadow: 0 2px 8px rgba(0,0,0,0.06);
    }
    .msg.user { background: #eff6ff; }
    .msg.assistant { background: #f8fafc; }
    /* Dashed and unshadowed so a tool card reads as an aside next to the
       message bubbles, with a border dark enough to actually show. */
    .msg.tool {
      gap: 6px; padding: 12px 16px;
      background: #f1f5f9; border: 1px dashed #cbd5e1; box-shadow: none;
    }
    .tool-name {
      font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace;
      text-transform: none; letter-spacing: 0; color: #334155;
    }
    /* The card is already the container — the global pre chrome would nest a
       second panel inside it. */
    .tool-result {
      margin: 0; padding: 0; font-size: 13px; color: #334155;
      background: transparent; border: 0;
    }
    .msg-head {
      display: flex; align-items: center; gap: 12px;
      color: #64748b; font-size: 12px;
      text-transform: uppercase; letter-spacing: 0.08em;
    }
    .msg-label { font-weight: 700; }
    .msg-body { min-width: 0; overflow-wrap: anywhere; }
    .msg-body > :first-child, .msg-thinking > :first-child { margin-top: 0; }
    .msg-body > :last-child, .msg-thinking > :last-child { margin-bottom: 0; }
    .msg-thinking-wrap { border-top: 1px solid #e5e7eb; padding-top: 10px; }
    .msg-thinking-label {
      margin-bottom: 8px; color: #64748b; font-size: 12px; font-weight: 700;
      text-transform: uppercase; letter-spacing: 0.08em;
    }
    .msg-thinking {
      padding: 12px 14px; border: 1px solid #e5e7eb; border-radius: 14px;
      background: #f1f5f9;
    }
    p, ul, ol, pre, blockquote { margin: 0 0 12px; }
    pre {
      overflow: auto; padding: 14px; border-radius: 14px;
      background: #f1f5f9; border: 1px solid #e5e7eb;
      white-space: pre-wrap; word-break: break-word;
    }
    code { font-family: "SFMono-Regular", Consolas, "Liberation Mono", Menlo, monospace; }
    a { color: #2563eb; text-decoration: none; }
    blockquote {
      margin-left: 0; padding-left: 14px;
      border-left: 3px solid rgba(37,99,235,0.45); color: #475569;
    }
  `
}

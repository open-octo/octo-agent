import { describe, it, expect } from 'vitest'
import {
  sanitizeExportTitle,
  filterEventsBySelectedMessages,
  pickSelectedMessages,
  exportAsMarkdown,
  exportAsJSON,
  exportAsHTML,
  buildConversationMarkup,
  type ExportableMessage,
} from './sessionExport'

const userEvent = { type: 'history_user_message', content: 'hello' }
const assistantEvent = { type: 'assistant_message', content: 'hi there', thinking: 'think' }
const toolCallEvent = { type: 'tool_call', tool_name: 'bash' }
const toolResultEvent = { type: 'tool_result', content: 'done' }

describe('sanitizeExportTitle', () => {
  it('keeps word characters, dots and dashes', () => {
    expect(sanitizeExportTitle('session-1.2')).toBe('session-1.2')
  })

  it('replaces unsafe filename characters with underscores', () => {
    // consecutive unsafe chars collapse into one underscore ([^\w.-]+)
    expect(sanitizeExportTitle('a/b:c*?')).toBe('a_b_c_')
  })

  it('falls back to "session" for empty titles', () => {
    expect(sanitizeExportTitle('')).toBe('session')
    expect(sanitizeExportTitle(undefined as unknown as string)).toBe('session')
  })
})

describe('filterEventsBySelectedMessages', () => {
  const localMsgs = [
    { id: 'u1', type: 'user' },
    { id: 'a1', type: 'assistant' },
    { id: 'u2', type: 'user' },
  ]

  it('keeps only events whose local message is selected', () => {
    const result = filterEventsBySelectedMessages(
      [userEvent, assistantEvent, toolCallEvent],
      localMsgs,
      new Set(['u1', 'a1'])
    )
    expect(result.map(e => e.type)).toEqual(['history_user_message', 'assistant_message', 'tool_call'])
  })

  it('drops unselected user/assistant events but keeps tool events', () => {
    const result = filterEventsBySelectedMessages(
      [userEvent, assistantEvent, toolResultEvent],
      localMsgs,
      new Set(['u1']) // only u1 selected → assistant dropped
    )
    expect(result.map(e => e.type)).toEqual(['history_user_message', 'tool_result'])
  })

  it('skips empty assistant turns so the index stays aligned', () => {
    // u2 selected but a1 not: without the empty-assistant skip, the real
    // assistant event would wrongly consume u2's slot and be kept.
    const events = [
      userEvent,
      { type: 'assistant_message', content: '', thinking: '' },
      assistantEvent,
    ]
    const result = filterEventsBySelectedMessages(events, localMsgs, new Set(['u1', 'u2']))
    const assistants = result.filter(e => e.type === 'assistant_message')
    expect(assistants).toHaveLength(1) // only the empty one rides along
    expect(assistants[0].content).toBe('')
  })
})

describe('pickSelectedMessages', () => {
  it('returns only messages whose id is selected', () => {
    const msgs: ExportableMessage[] = [
      { id: 'a', type: 'user', content: 'x' },
      { id: 'b', type: 'assistant', content: 'y' },
    ]
    expect(pickSelectedMessages(msgs, new Set(['b']))).toEqual([msgs[1]])
  })

  it('returns empty array when nothing is selected', () => {
    expect(pickSelectedMessages([], new Set())).toEqual([])
  })
})

describe('exportAsMarkdown', () => {
  it('renders title, user and assistant headers', () => {
    const md = exportAsMarkdown([userEvent, assistantEvent], { title: 'Test' })
    expect(md.content).toContain('# Test')
    expect(md.content).toContain('## You')
    expect(md.content).toContain('hello')
    expect(md.content).toContain('## Octo')
    expect(md.content).toContain('hi there')
  })

  it('includes thinking as a details block', () => {
    const md = exportAsMarkdown([assistantEvent], { title: 'T' })
    expect(md.content).toContain('<details><summary>Thoughts</summary>')
    expect(md.content).toContain('think')
  })

  it('omits tool events by default and reports it', () => {
    const md = exportAsMarkdown([userEvent, toolCallEvent], { title: 'T' })
    expect(md.omittedToolEvents).toBe(true)
    expect(md.content).not.toContain('bash')
  })

  it('includes tool events when includeTools is set', () => {
    const md = exportAsMarkdown([toolCallEvent, toolResultEvent], { title: 'T', includeTools: true })
    expect(md.omittedToolEvents).toBe(false)
    expect(md.content).toContain('**Tool call**: bash')
    expect(md.content).toContain('**Tool result**: done')
  })

  it('produces a sanitized .md filename', () => {
    const md = exportAsMarkdown([userEvent], { title: 'a/b' })
    expect(md.filename).toBe('a_b.md')
    expect(md.mime).toBe('text/markdown')
  })
})

describe('exportAsJSON', () => {
  it('serializes all events including tools', () => {
    const json = exportAsJSON([userEvent, toolCallEvent], 'Sess')
    const parsed = JSON.parse(json.content)
    expect(parsed).toHaveLength(2)
    expect(parsed[1].type).toBe('tool_call')
    expect(json.filename).toBe('Sess.json')
    expect(json.mime).toBe('application/json')
  })

  it('pretty-prints with 2-space indent', () => {
    const json = exportAsJSON([userEvent], 'T')
    expect(json.content).toContain('\n    "type"') // nested inside the array
  })
})

describe('buildConversationMarkup', () => {
  it('renders user and assistant bubbles with sequential numbering', () => {
    const msgs: ExportableMessage[] = [
      { id: 'u', type: 'user', content: 'one' },
      { id: 'a', type: 'assistant', content: 'two' },
    ]
    const html = buildConversationMarkup(msgs)
    expect(html).toContain('msg user')
    expect(html).toContain('msg assistant')
    expect(html).toContain('#1')
    expect(html).toContain('#2')
    expect(html).toContain('one')
    expect(html).toContain('two')
  })

  it('escapes raw user content', () => {
    const msgs: ExportableMessage[] = [{ id: 'u', type: 'user', content: '<b>bold</b>' }]
    const html = buildConversationMarkup(msgs)
    expect(html).toContain('&lt;b&gt;')
    expect(html).not.toContain('<b>bold</b>')
  })
})

describe('exportAsHTML', () => {
  it('produces a self-contained document with watermark', () => {
    const msgs: ExportableMessage[] = [{ id: 'u', type: 'user', content: 'hi' }]
    const html = exportAsHTML(msgs, { title: 'Sess', exportedAt: new Date(2024, 0, 15), locale: 'en', watermark: 'My WM' })
    // DOMPurify WHOLE_DOCUMENT strips the doctype; the document shell remains
    expect(html.content).toContain('<html lang="en">')
    expect(html.content).toContain('</html>')
    expect(html.content).toContain('My WM')
    expect(html.content).toContain('Sess')
    expect(html.filename).toBe('Sess.html')
    expect(html.mime).toBe('text/html')
  })

  it('strips <script> tags via DOMPurify', () => {
    // assistant content goes through renderMarkdown → real HTML, which DOMPurify must clean
    const msgs: ExportableMessage[] = [
      { id: 'a', type: 'assistant', content: '<script>alert(1)</script>safe' },
    ]
    const html = exportAsHTML(msgs, { title: 'T' })
    expect(html.content).not.toContain('<script')
    expect(html.content).not.toContain('alert(1)')
    expect(html.content).toContain('safe')
  })

  it('strips inline event handlers', () => {
    const msgs: ExportableMessage[] = [
      { id: 'a', type: 'assistant', content: '<img src=x onerror=alert(1)>' },
    ]
    const html = exportAsHTML(msgs, { title: 'T' })
    expect(html.content).not.toContain('onerror')
  })
})

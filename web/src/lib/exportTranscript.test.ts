import { describe, it, expect } from 'vitest'
import { applyToolToggle, buildExportConversation, isToolEvent } from './exportTranscript'

const transcript = [
  { type: 'history_user_message', content: 'build it' },
  { type: 'assistant_message', content: 'on it' },
  { type: 'tool_call', name: 'terminal', args: { command: 'ls' } },
  { type: 'tool_result', result: 'a.txt\nb.txt' },
  { type: 'assistant_message', content: 'two files' },
]

describe('isToolEvent', () => {
  it('matches only tool calls and results', () => {
    expect(isToolEvent({ type: 'tool_call' })).toBe(true)
    expect(isToolEvent({ type: 'tool_result' })).toBe(true)
    expect(isToolEvent({ type: 'assistant_message' })).toBe(false)
    expect(isToolEvent({ type: 'thinking' })).toBe(false)
    expect(isToolEvent(undefined)).toBe(false)
  })
})

describe('applyToolToggle', () => {
  it('keeps everything when the toggle is on', () => {
    const { events, omittedTools } = applyToolToggle(transcript, true)
    expect(events).toEqual(transcript)
    expect(omittedTools).toBe(false)
  })

  it('drops tool events when the toggle is off', () => {
    const { events, omittedTools } = applyToolToggle(transcript, false)
    expect(events.map((e) => e.type)).toEqual([
      'history_user_message', 'assistant_message', 'assistant_message',
    ])
    expect(omittedTools).toBe(true)
  })

  // The toast this drives says tool calls were left out, so a transcript that
  // never had any must not report an omission.
  it('reports no omission when there were no tool events to drop', () => {
    const plain = transcript.filter((e) => !isToolEvent(e))
    expect(applyToolToggle(plain, false).omittedTools).toBe(false)
  })
})

describe('buildExportConversation', () => {
  it('renders tool calls and results when they survive the toggle', () => {
    const html = buildExportConversation(applyToolToggle(transcript, true).events)
    expect(html).toContain('Tool call')
    expect(html).toContain('terminal')
    expect(html).toContain('Tool result')
    expect(html).toContain('a.txt')
  })

  it('renders none of them when the toggle dropped them', () => {
    const html = buildExportConversation(applyToolToggle(transcript, false).events)
    expect(html).not.toContain('Tool call')
    expect(html).not.toContain('Tool result')
    expect(html).toContain('two files')
  })

  it('escapes tool names and results rather than emitting markup', () => {
    const html = buildExportConversation([
      { type: 'tool_call', name: '<img src=x onerror=alert(1)>' },
      { type: 'tool_result', result: '<script>alert(2)</script>' },
    ])
    expect(html).not.toContain('<img')
    expect(html).not.toContain('<script>')
    expect(html).toContain('&lt;img')
  })

  it('caps a long tool result', () => {
    const html = buildExportConversation([{ type: 'tool_result', result: 'x'.repeat(900) }])
    expect(html).toContain('x'.repeat(500))
    expect(html).not.toContain('x'.repeat(501))
  })

  it('labels a non-text tool result instead of stringifying it', () => {
    const html = buildExportConversation([{ type: 'tool_result', result: { rows: 3 } }])
    expect(html).toContain('(non-text result)')
  })

  it('skips event types it has no card for', () => {
    expect(buildExportConversation([{ type: 'thinking', text: 'hmm' }])).toBe('')
  })
})

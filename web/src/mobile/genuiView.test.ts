import { describe, it, expect } from 'vitest'
import type { ChatMessage } from '../lib/types'
import { isHiddenAt, visibleCount } from './genuiView'

let seq = 0
const m = (type: ChatMessage['type'], content: string, streaming = false): ChatMessage =>
  ({ id: `m${seq++}`, type, content, streaming, createdAt: 0, tools: [], todos: [] }) as ChatMessage
const fence = (spec: object) => '```octo-ui\n' + JSON.stringify(spec) + '\n```'
const panel = (text: string) => fence({ id: 'stats', items: [{ type: 'text', text }] })
const action = (p?: string) => '[octo-ui-action] ' + JSON.stringify({ ...(p ? { panel: p } : {}), action: 'refresh' })

describe('visibleCount / isHiddenAt', () => {
  it('hides both halves of a silent panel turn', () => {
    const msgs = [m('user', 'stats please'), m('assistant', panel('v1')), m('user', action('stats')), m('assistant', panel('v2'))]
    expect([0, 1, 2, 3].map(i => isHiddenAt(msgs, i))).toEqual([false, false, true, true])
    expect(visibleCount(msgs)).toBe(2)
  })

  it('keeps an anonymous panel action and its reply visible', () => {
    const msgs = [m('assistant', fence({ items: [{ type: 'text', text: 'x' }] })), m('user', action()), m('assistant', 'done')]
    expect(visibleCount(msgs)).toBe(3)
  })

  it('shows a reply to a silent action that answers in prose', () => {
    const msgs = [m('assistant', panel('v1')), m('user', action('stats')), m('assistant', 'Could not refresh: the API is down.')]
    expect(isHiddenAt(msgs, 2)).toBe(false)
    expect(visibleCount(msgs)).toBe(2)
  })

  it('hides a streaming reply while it could still be a silent update', () => {
    const msgs = [m('assistant', panel('v1')), m('user', action('stats')), m('assistant', '```oct', true)]
    expect(isHiddenAt(msgs, 2)).toBe(true)
    msgs[2] = m('assistant', 'Sorry,', true)
    expect(isHiddenAt(msgs, 2)).toBe(false)
  })
})

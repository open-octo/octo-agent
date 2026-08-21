import { describe, it, expect } from 'vitest'
import { projectPanels, isAnchor } from './projection'
import type { ChatMessage } from '../types'

let seq = 0
function msg(type: ChatMessage['type'], content: string): ChatMessage {
  return { id: `m${seq++}`, type, content, streaming: false, createdAt: 0, tools: [], todos: [] }
}

function fence(id: string | null, text: string): string {
  const spec: Record<string, unknown> = { items: [{ type: 'text', text }] }
  if (id) spec.id = id
  return '```octo-ui\n' + JSON.stringify(spec) + '\n```'
}

const action = (panel: string) => msg('user', `[octo-ui-action] {"panel":"${panel}","action":"refresh"}`)

describe('projectPanels', () => {
  it('is empty for messages with no addressable panel', () => {
    const msgs = [msg('user', 'hi'), msg('assistant', 'hello'), msg('assistant', fence(null, 'anon'))]
    expect(projectPanels(msgs).size).toBe(0)
  })

  it('takes the newest spec for a panel', () => {
    const msgs = [
      msg('assistant', fence('p', 'v1')),
      action('p'),
      msg('assistant', fence('p', 'v2')),
      action('p'),
      msg('assistant', fence('p', 'v3')),
    ]
    const p = projectPanels(msgs).get('p')
    expect((p?.spec.items[0] as any).text).toBe('v3')
    expect(p?.versions).toBe(3)
  })

  it('keeps the anchor put across silent turns', () => {
    const seed = msg('assistant', fence('p', 'v1'))
    const msgs = [seed, action('p'), msg('assistant', fence('p', 'v2')), action('p'), msg('assistant', fence('p', 'v3'))]
    const p = projectPanels(msgs).get('p')
    // Content advanced to v3 while the panel stayed where it was first shown.
    expect(p?.anchorMsgId).toBe(seed.id)
    expect((p?.spec.items[0] as any).text).toBe('v3')
  })

  it('moves the anchor when the model re-presents the panel in an ordinary reply', () => {
    const seed = msg('assistant', fence('p', 'v1'))
    const represent = msg('assistant', 'here it is again:\n' + fence('p', 'v2'))
    const p = projectPanels([seed, msg('user', 'show me again'), represent]).get('p')
    expect(p?.anchorMsgId).toBe(represent.id)
  })

  it('tracks several panels independently', () => {
    const a = msg('assistant', fence('a', 'A1'))
    const b = msg('assistant', fence('b', 'B1'))
    const panels = projectPanels([a, b, action('a'), msg('assistant', fence('a', 'A2'))])
    expect(panels.get('a')?.anchorMsgId).toBe(a.id)
    expect((panels.get('a')?.spec.items[0] as any).text).toBe('A2')
    expect((panels.get('b')?.spec.items[0] as any).text).toBe('B1')
  })

  it('holds the previous version steady while a new one is still streaming', () => {
    // The incomplete fence's partial spec already carries the id, but the
    // silent classification can't hold until the fence closes — without the
    // guard the anchor jumps to the hidden streaming message and the panel
    // unmounts mid-update.
    const seed = msg('assistant', fence('p', 'v1'))
    const streamingReply = msg('assistant', '```octo-ui\n{"id":"p","items":[{"type":"text","text":"v2 partial"')
    streamingReply.streaming = true
    const panels = projectPanels([seed, action('p'), streamingReply])
    const p = panels.get('p')
    expect((p?.spec.items[0] as any).text).toBe('v1')
    expect(p?.anchorMsgId).toBe(seed.id)
    expect(p?.versions).toBe(1)
    expect(isAnchor(panels, p!.spec, seed.id, 0)).toBe(true)
  })

  it('still renders a first version live while its fence streams', () => {
    const streaming = msg('assistant', '```octo-ui\n{"id":"p","items":[{"type":"text","text":"building"}')
    streaming.streaming = true
    const p = projectPanels([streaming]).get('p')
    expect(p?.anchorMsgId).toBe(streaming.id)
    expect((p?.spec.items[0] as any).text).toBe('building')
  })
})

describe('isAnchor', () => {
  it('always renders an anonymous spec where it sits', () => {
    expect(isAnchor(new Map(), { items: [] }, 'any', 0)).toBe(true)
  })

  it('renders an addressable spec only at its anchor', () => {
    const seed = msg('assistant', fence('p', 'v1'))
    const later = msg('assistant', fence('p', 'v2'))
    const panels = projectPanels([seed, action('p'), later])
    expect(isAnchor(panels, { id: 'p', items: [] }, seed.id, 0)).toBe(true)
    expect(isAnchor(panels, { id: 'p', items: [] }, later.id, 0)).toBe(false)
  })

  it('is false for a null spec', () => {
    expect(isAnchor(new Map(), null, 'm', 0)).toBe(false)
  })
})

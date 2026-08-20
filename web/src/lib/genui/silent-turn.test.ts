import { describe, it, expect } from 'vitest'
import {
  parseActionEnvelope,
  silentActionPanel,
  couldBeSilentReply,
  isSilentReply,
  isSilentPairAt,
  precedingSaid,
} from './silent-turn'
import type { ChatMessage } from '../types'

function msg(type: ChatMessage['type'], content: string, streaming = false): ChatMessage {
  return { id: `m-${content.length}-${type}`, type, content, streaming, createdAt: 0, tools: [], todos: [] }
}

const fence = (id: string) => '```octo-ui\n' + JSON.stringify({ id, items: [{ type: 'text', text: 'hi' }] }) + '\n```'

describe('parseActionEnvelope', () => {
  it('parses an action with a panel', () => {
    const env = parseActionEnvelope('[octo-ui-action] {"panel":"sales","action":"refresh","fields":{"range":"30d"}}')
    expect(env?.panel).toBe('sales')
    expect(env?.action).toBe('refresh')
  })

  it('returns null for a message that only looks like one', () => {
    expect(parseActionEnvelope('[octo-ui-action] not json')).toBeNull()
    expect(parseActionEnvelope('just a message')).toBeNull()
    // Missing the required action key.
    expect(parseActionEnvelope('[octo-ui-action] {"panel":"p"}')).toBeNull()
  })
})

describe('silentActionPanel', () => {
  it('is the panel id only when one is present', () => {
    expect(silentActionPanel(msg('user', '[octo-ui-action] {"panel":"p","action":"a"}'))).toBe('p')
    // An anonymous panel's action stays visible.
    expect(silentActionPanel(msg('user', '[octo-ui-action] {"action":"a"}'))).toBeNull()
    expect(silentActionPanel(msg('user', 'hello'))).toBeNull()
    expect(silentActionPanel(undefined)).toBeNull()
  })

  it('ignores assistant messages', () => {
    expect(silentActionPanel(msg('assistant', '[octo-ui-action] {"panel":"p","action":"a"}'))).toBeNull()
  })
})

describe('couldBeSilentReply (streaming predicate)', () => {
  it('holds through the states a silent reply passes on its way in', () => {
    expect(couldBeSilentReply('')).toBe(true)
    expect(couldBeSilentReply('```octo-ui')).toBe(true)
    expect(couldBeSilentReply('```octo-ui\n{"id":"p"')).toBe(true)
    expect(couldBeSilentReply(fence('p'))).toBe(true)
    // Trailing whitespace after a closed fence is still silent.
    expect(couldBeSilentReply(fence('p') + '\n\n')).toBe(true)
  })

  it('goes false once prose appears', () => {
    expect(couldBeSilentReply('Here you go:')).toBe(false)
    expect(couldBeSilentReply('Here you go:\n' + fence('p'))).toBe(false)
    const withTail = fence('p') + '\nanything else'
    expect(couldBeSilentReply(withTail)).toBe(false)
    expect(couldBeSilentReply(withTail + ' more')).toBe(false)
  })

  // Scans every prefix rather than a few hand-picked strings: the opening
  // marker arrives one character at a time, and an intermediate state like
  // "`" or "```oct" splits as ordinary markdown. Asserting only on whole
  // strings hides that, and the caller hides a bubble based on this — a
  // single false frame is a visible flash.
  it('never goes false while a real silent reply streams in, character by character', () => {
    const full = fence('sales')
    for (let i = 0; i <= full.length; i++) {
      const prefix = full.slice(0, i)
      expect(couldBeSilentReply(prefix), `prefix ${JSON.stringify(prefix)}`).toBe(true)
    }
  })

  it('is monotone: once false it stays false for every longer prefix', () => {
    const full = 'Updated:\n' + fence('sales') + '\nlet me know'
    let seenFalse = false
    for (let i = 0; i <= full.length; i++) {
      const prefix = full.slice(0, i)
      const cur = couldBeSilentReply(prefix)
      if (!cur) seenFalse = true
      else if (seenFalse) throw new Error(`flipped back to true at ${JSON.stringify(prefix)}`)
    }
    expect(seenFalse).toBe(true)
  })

  it('does not mistake another language fence for the opening marker', () => {
    // "```octo" prefixes the marker and is tolerated while trailing, but
    // "```json" diverges and must disqualify immediately.
    expect(couldBeSilentReply('```octo')).toBe(true)
    expect(couldBeSilentReply('```json')).toBe(false)
    expect(couldBeSilentReply('```octo-uix')).toBe(false)
  })

  it('goes false on a second fence', () => {
    expect(couldBeSilentReply(fence('p') + '\n' + fence('q'))).toBe(false)
  })
})

describe('isSilentReply', () => {
  it('accepts exactly one fence carrying the addressed id', () => {
    expect(isSilentReply(msg('assistant', fence('sales')), 'sales')).toBe(true)
  })

  it('rejects a reply that also says something', () => {
    expect(isSilentReply(msg('assistant', 'Updated:\n' + fence('sales')), 'sales')).toBe(false)
  })

  it('rejects a different panel', () => {
    expect(isSilentReply(msg('assistant', fence('other')), 'sales')).toBe(false)
  })

  it('rejects a reply with no fence at all', () => {
    expect(isSilentReply(msg('assistant', 'no can do'), 'sales')).toBe(false)
  })

  it('rejects an unclosed fence — a silent update must be complete', () => {
    expect(isSilentReply(msg('assistant', '```octo-ui\n{"id":"sales","items":[]'), 'sales')).toBe(false)
  })
})

describe('isSilentPairAt', () => {
  const action = () => msg('user', '[octo-ui-action] {"panel":"sales","action":"refresh"}')

  it('needs both halves', () => {
    expect(isSilentPairAt([action(), msg('assistant', fence('sales'))], 1)).toBe(true)
    // Reply is fine but nothing addressed it.
    expect(isSilentPairAt([msg('user', 'hi'), msg('assistant', fence('sales'))], 1)).toBe(false)
    // Action is fine but the reply talks.
    expect(isSilentPairAt([action(), msg('assistant', 'sure thing')], 1)).toBe(false)
  })

  it('looks past tool calls between the action and the reply', () => {
    // "The panel needs data the model doesn't have" is the archetypal silent
    // turn, and it is exactly the case where a tool group sits in between.
    const msgs = [action(), msg('tool_group', ''), msg('progress', ''), msg('assistant', fence('sales'))]
    expect(isSilentPairAt(msgs, 3)).toBe(true)
  })

  it('does not look past something the model actually said', () => {
    const msgs = [action(), msg('assistant', 'working on it'), msg('assistant', fence('sales'))]
    expect(isSilentPairAt(msgs, 2)).toBe(false)
  })
})

describe('precedingSaid', () => {
  it('skips turn scaffolding but stops at real messages', () => {
    const msgs = [msg('user', 'hi'), msg('tool_group', ''), msg('progress', '')]
    expect(precedingSaid(msgs, 3)?.content).toBe('hi')
    expect(precedingSaid(msgs, 1)?.content).toBe('hi')
    expect(precedingSaid(msgs, 0)).toBeUndefined()
  })
})

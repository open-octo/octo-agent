import { describe, it, expect } from 'vitest'
import { parseActionEnvelope, silentActionPanel, couldBeSilentReply, isSilentReply, isSilentPair } from './silent-turn'
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

  it('goes false once prose appears, and is monotone', () => {
    expect(couldBeSilentReply('Here you go:')).toBe(false)
    expect(couldBeSilentReply('Here you go:\n' + fence('p'))).toBe(false)
    // Prose after the fence also disqualifies, and adding more text can never
    // bring it back — the caller relies on that to keep a bubble shown.
    const withTail = fence('p') + '\nanything else'
    expect(couldBeSilentReply(withTail)).toBe(false)
    expect(couldBeSilentReply(withTail + ' more')).toBe(false)
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

describe('isSilentPair', () => {
  it('needs both halves', () => {
    const action = msg('user', '[octo-ui-action] {"panel":"sales","action":"refresh"}')
    expect(isSilentPair(action, msg('assistant', fence('sales')))).toBe(true)
    // Reply is fine but nothing addressed it.
    expect(isSilentPair(msg('user', 'hi'), msg('assistant', fence('sales')))).toBe(false)
    // Action is fine but the reply talks.
    expect(isSilentPair(action, msg('assistant', 'sure thing'))).toBe(false)
  })
})

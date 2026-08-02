import { describe, it, expect } from 'vitest'
import { normalizeHash } from './hashRouting'

describe('normalizeHash', () => {
  it('keeps the active session id on the chat view', () => {
    expect(normalizeHash('chat', 'sess-42')).toBe('#/chat/sess-42')
  })

  it('falls back to bare #/chat without a session', () => {
    expect(normalizeHash('chat', null)).toBe('#/chat')
  })

  it('uses the bare view hash for non-chat views', () => {
    expect(normalizeHash('agents', null)).toBe('#/agents')
    expect(normalizeHash('agents', 'sess-42')).toBe('#/agents')
  })

  it('percent-encodes session ids for the URL', () => {
    expect(normalizeHash('chat', 'a/b c?d')).toBe('#/chat/a%2Fb%20c%3Fd')
  })
})

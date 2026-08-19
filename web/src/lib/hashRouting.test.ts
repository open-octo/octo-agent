import { describe, it, expect } from 'vitest'
import { normalizeHash, hashPicksChatTarget } from './hashRouting'

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

describe('hashPicksChatTarget', () => {
  it('treats a bare chat hash as the landing page, not an empty slot', () => {
    expect(hashPicksChatTarget('#/chat')).toBe(true)
  })

  it('treats a session hash as chosen', () => {
    expect(hashPicksChatTarget('#/chat/sess-42')).toBe(true)
  })

  it('leaves an absent hash to the auto-select fallback', () => {
    expect(hashPicksChatTarget('')).toBe(false)
    expect(hashPicksChatTarget('#')).toBe(false)
  })

  it('ignores non-chat views', () => {
    expect(hashPicksChatTarget('#/agents')).toBe(false)
    // Not a prefix match on the raw string: another view must not qualify
    // just because its name starts with "chat".
    expect(hashPicksChatTarget('#/chatter')).toBe(false)
  })
})

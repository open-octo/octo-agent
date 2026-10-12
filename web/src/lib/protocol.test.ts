import { describe, it, expect } from 'vitest'
import { normalizeProtocol } from './api'

// normalizeProtocol seeds the endpoint and model form selects from a stored
// protocol. A stored value must come back unchanged, or editing an endpoint
// would silently switch its wire format; absent or unknown reads as the
// server's Custom default, openai.
describe('normalizeProtocol', () => {
  it('keeps each known protocol', () => {
    expect(normalizeProtocol('openai')).toBe('openai')
    expect(normalizeProtocol('anthropic')).toBe('anthropic')
    expect(normalizeProtocol('openai-responses')).toBe('openai-responses')
  })

  it('treats absent or unknown as openai', () => {
    expect(normalizeProtocol(undefined)).toBe('openai')
    expect(normalizeProtocol('')).toBe('openai')
    expect(normalizeProtocol('grpc')).toBe('openai')
  })
})

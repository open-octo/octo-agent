import { describe, it, expect } from 'vitest'
import { generateEndpointID } from './api'
import type { EndpointConfig } from './api'

// generateEndpointID produces the human-readable endpoint id the wizard
// assigns from the provider name, with overwrite-reuse and suffix-on-conflict
// rules. This TS test mirrors cmd/octo/endpoint_id_test.go (the canonical
// impl is the Go side; this locks the same contract in the web shim so a
// future drift between the two is caught by CI).
describe('generateEndpointID', () => {
  const ep = (id: string, provider: string, base_url?: string): EndpointConfig => ({
    id, provider, base_url, has_api_key: false, models: [],
  })

  it('named vendor with no existing endpoints', () => {
    expect(generateEndpointID('anthropic', '', [])).toBe('anthropic')
  })

  it('named vendor with base_url and empty existing', () => {
    expect(generateEndpointID('openai', 'https://api.openai.com/v1', [])).toBe('openai')
  })

  it('custom vendor falls back to custom', () => {
    expect(generateEndpointID('custom', 'https://relay.example.com', [])).toBe('custom')
  })

  it('empty provider falls back to custom', () => {
    expect(generateEndpointID('', '', [])).toBe('custom')
  })

  it('overwrite: same provider + base_url reuses existing id', () => {
    expect(generateEndpointID('anthropic', '', [ep('anthropic', 'anthropic', '')])).toBe('anthropic')
  })

  it('overwrite: same provider + explicit base_url reuse', () => {
    expect(generateEndpointID('anthropic', 'https://api.anthropic.com', [ep('anthropic', 'anthropic', 'https://api.anthropic.com')])).toBe('anthropic')
  })

  it('conflict: natural id taken by different provider gets suffix', () => {
    expect(generateEndpointID('anthropic', 'https://relay.example.com', [ep('anthropic', 'openai', 'https://api.openai.com')])).toBe('anthropic-1')
  })

  it('conflict: natural id and -1 both taken', () => {
    expect(generateEndpointID('anthropic', '', [
      ep('anthropic', 'anthropic', 'https://api.anthropic.com'),
      ep('anthropic-1', 'anthropic', 'https://relay1.example.com'),
    ])).toBe('anthropic-2')
  })

  it('custom conflict: custom taken gets custom-1', () => {
    expect(generateEndpointID('custom', 'https://another.example.com', [ep('custom', 'custom', 'https://relay.example.com')])).toBe('custom-1')
  })

  it('different base_url same provider does not trigger overwrite', () => {
    expect(generateEndpointID('anthropic', 'https://relay.example.com', [ep('anthropic', 'anthropic', '')])).toBe('anthropic-1')
  })
})

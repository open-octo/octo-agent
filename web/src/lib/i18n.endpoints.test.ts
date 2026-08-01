import { describe, it, expect } from 'vitest'
import { en, zh } from './i18n'

// Agent-First UI alignment (PR #1827): the endpoints section in SettingsModal
// is now read-only display — configuration happens through conversation via
// the config-setup skill. Only display keys remain.
const ENDPOINT_KEYS = [
  'settings.endpoints.title',
  'settings.endpoints.configure',
  'settings.endpoints.configure_with_agent',
  'settings.endpoints.empty',
  'settings.endpoints.api_key',
  'settings.endpoints.api_key.set',
  'settings.endpoints.api_key.missing',
  'settings.endpoints.models',
  'settings.endpoints.models.vision',
  'settings.endpoints.badge.default',
  'settings.endpoints.badge.lite',
] as const

describe('settings.endpoints.* i18n coverage', () => {
  it.each(ENDPOINT_KEYS)('en has a non-empty string for %s', (key) => {
    const v = en[key]
    expect(typeof v).toBe('string')
    expect(v.length).toBeGreaterThan(0)
  })
  it.each(ENDPOINT_KEYS)('zh has a non-empty string for %s', (key) => {
    const v = zh[key]
    expect(typeof v).toBe('string')
    expect(v.length).toBeGreaterThan(0)
  })
  it('en and zh carry the same set of settings.endpoints.* keys', () => {
    const enKeys = Object.keys(en).filter((k) => k.startsWith('settings.endpoints.')).sort()
    const zhKeys = Object.keys(zh).filter((k) => k.startsWith('settings.endpoints.')).sort()
    expect(zhKeys).toEqual(enKeys)
  })
})

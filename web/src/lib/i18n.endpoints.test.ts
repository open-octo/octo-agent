import { describe, it, expect } from 'vitest'
import { en, zh } from './i18n'

// PR4b (design §15.1): the new settings.endpoints.* namespace must exist in
// both en and zh with no missing keys. A missing key would render as the raw
// key string in the UI, which is the regression this test guards against.
//
// This is the PR4b read-only subset of §15.1. The full table also lists
// CRUD-related keys (add, delete, rename_confirm, error.*) that PR5 will add
// when the write path lands. Don't assume §15.1 is fully covered just because
// this test passes — extend ENDPOINT_KEYS in PR5.
const ENDPOINT_KEYS = [
  'settings.endpoints.title',
  'settings.endpoints.empty',
  'settings.endpoints.readonly_notice',
  'settings.endpoints.id',
  'settings.endpoints.name',
  'settings.endpoints.provider',
  'settings.endpoints.base_url',
  'settings.endpoints.protocol',
  'settings.endpoints.api_key',
  'settings.endpoints.api_key.set',
  'settings.endpoints.api_key.missing',
  'settings.endpoints.lite_model',
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

import { describe, it, expect } from 'vitest'
import { en, zh } from './i18n'

// Settings → Endpoints is a direct-edit UI (EndpointsSection.svelte): card
// list, inline create/edit form, per-model chip actions, plus a secondary
// "edit with agent" conversational entry. Every key it renders must exist in
// both locales.
const ENDPOINT_KEYS = [
  'settings.endpoints.title',
  'settings.endpoints.list_label',
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
  'settings.endpoints.add_model',
  'settings.endpoints.add_model.placeholder',
  'settings.endpoints.set_default',
  'settings.endpoints.set_lite',
  'settings.endpoints.unset_lite',
  'settings.endpoints.pick_model',
  'settings.endpoints.delete_confirm',
  'settings.endpoints.delete_model_confirm',
  'settings.endpoints.form.title.edit',
  'settings.endpoints.form.id',
  'settings.endpoints.form.name',
  'settings.endpoints.form.key_keep',
  'settings.endpoints.form.initial_model',
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

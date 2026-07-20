import { describe, it, expect } from 'vitest'
import { en, zh } from './i18n'

// PR4b (design §15.1): the new settings.endpoints.* namespace must exist in
// both en and zh with no missing keys. A missing key would render as the raw
// key string in the UI, which is the regression this test guards against.
//
// PR4b added the read-only subset; PR6 extends with CRUD-related keys
// (add/delete/edit/rename_confirm/error.*/field.*/modal.*). The full §15.1
// table is now covered.
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
  // PR6 CRUD keys:
  'settings.endpoints.add',
  'settings.endpoints.delete',
  'settings.endpoints.edit',
  'settings.endpoints.set_default',
  'settings.endpoints.set_lite',
  'settings.endpoints.unset_lite',
  'settings.endpoints.models.add',
  'settings.endpoints.models.model',
  'settings.endpoints.models.vision_hint',
  'settings.endpoints.rename_confirm',
  'settings.endpoints.confirm_delete',
  'settings.endpoints.confirm_delete_model',
  'settings.endpoints.error.duplicate_id',
  'settings.endpoints.error.invalid_id',
  'settings.endpoints.error.not_found',
  'settings.endpoints.error.model_not_found',
  'settings.endpoints.error.empty',
  'settings.endpoints.field.id',
  'settings.endpoints.field.name',
  'settings.endpoints.field.provider',
  'settings.endpoints.field.base_url',
  'settings.endpoints.field.protocol',
  'settings.endpoints.field.api_key',
  'settings.endpoints.field.api_key_hint',
  'settings.endpoints.modal.add_title',
  'settings.endpoints.modal.edit_title',
  'settings.endpoints.modal.save',
  'settings.endpoints.modal.cancel',
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

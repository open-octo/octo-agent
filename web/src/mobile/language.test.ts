import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { locale } from '../lib/i18n'
import { getLang, pinnedLocale, setLang } from './language'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts).
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

describe('mobile language', () => {
  beforeEach(() => {
    localStorage.clear()
    locale.set('en')
  })

  it('follows the server until pinned', () => {
    expect(getLang()).toBe('server')
    expect(pinnedLocale()).toBeNull()
  })

  it('pins this device and applies the locale at once', () => {
    setLang('zh', 'en')
    expect(getLang()).toBe('zh')
    expect(pinnedLocale()).toBe('zh')
    expect(get(locale)).toBe('zh')
  })

  it('going back to the server applies its language and clears the pin', () => {
    setLang('zh', 'en')
    setLang('server', 'zh-TW')
    expect(pinnedLocale()).toBeNull()
    expect(get(locale)).toBe('zh-TW')
  })

  it('treats an unknown stored value as following the server', () => {
    localStorage.setItem('octo.mobileLanguage', 'fr')
    expect(getLang()).toBe('server')
  })
})

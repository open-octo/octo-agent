import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { locale } from '../lib/i18n'
import { getLang, pinnedLocale, pinLang, followServer } from './language'

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
    pinLang('zh')
    expect(getLang()).toBe('zh')
    expect(pinnedLocale()).toBe('zh')
    expect(get(locale)).toBe('zh')
  })

  it('following the server again clears the pin and applies its language', async () => {
    pinLang('en')
    await followServer(async () => 'zh')
    expect(pinnedLocale()).toBeNull()
    expect(get(locale)).toBe('zh')
  })

  it('a failed server fetch unpins but keeps the current locale', async () => {
    pinLang('zh')
    await followServer(() => Promise.reject(new Error('offline')))
    expect(pinnedLocale()).toBeNull()
    expect(get(locale)).toBe('zh')
  })

  it('a pick made while the server fetch is in flight wins', async () => {
    let resolve!: (l: string) => void
    const pending = followServer(() => new Promise<string>(r => { resolve = r }))
    pinLang('zh')
    resolve('en')
    await pending
    expect(pinnedLocale()).toBe('zh')
    expect(get(locale)).toBe('zh')
  })

  it('treats an unknown stored value as following the server', () => {
    localStorage.setItem('octo.mobileLanguage', 'fr')
    expect(getLang()).toBe('server')
  })
})

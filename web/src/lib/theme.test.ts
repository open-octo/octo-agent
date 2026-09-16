import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getPack, setPack, PACKS } from './theme'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts), and the
// pack choice persists through it.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

const KEY = 'octo.themePack'

beforeEach(() => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme-pack')
})

describe('getPack', () => {
  it('defaults to azure when nothing is stored', () => {
    expect(getPack()).toBe('azure')
  })

  it('returns a stored pack that is still shipped', () => {
    localStorage.setItem(KEY, 'blossom')
    expect(getPack()).toBe('blossom')
  })

  it('reads the pre-rename default id as its current name', () => {
    localStorage.setItem(KEY, 'klook')
    expect(getPack()).toBe('azure')
  })

  it('falls back to the default for an id the app no longer ships', () => {
    localStorage.setItem(KEY, 'not-a-pack')
    expect(getPack()).toBe('azure')
  })
})

describe('setPack', () => {
  it('writes the attribute for a non-default pack', () => {
    setPack('vogue')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBe('vogue')
    expect(getPack()).toBe('vogue')
  })

  // The default palette lives in bare :root, so it is addressed by the absence
  // of the attribute — writing data-theme-pack="azure" would match no rule.
  it('removes the attribute for the default pack', () => {
    setPack('vogue')
    setPack('azure')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBeNull()
    expect(getPack()).toBe('azure')
  })
})

describe('PACKS', () => {
  it('lists the default first and has no duplicate ids', () => {
    expect(PACKS[0].id).toBe('azure')
    expect(new Set(PACKS.map((p) => p.id)).size).toBe(PACKS.length)
  })
})

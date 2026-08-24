import { describe, it, expect, vi, afterEach } from 'vitest'
import {
  parseSidebarWidth,
  readSidebarWidth,
  saveSidebarWidth,
  SIDEBAR_MIN,
  SIDEBAR_MAX,
  SIDEBAR_DEFAULT,
} from './sidebarWidth'

// This jsdom setup exposes no localStorage at all, so every storage case here is
// an explicit stub — which is what these tests are about anyway: the sidebar
// must survive whatever the host gives it. A missing global lands in the same
// catch as a browser that throws on access.
function stubStorage(store: Record<string, string> = {}) {
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => (k in store ? store[k] : null),
    setItem: (k: string, v: string) => { store[k] = v },
  })
  return store
}

afterEach(() => {
  vi.unstubAllGlobals()
})

describe('parseSidebarWidth', () => {
  it('falls back to the default for anything unusable', () => {
    expect(parseSidebarWidth(null)).toBe(SIDEBAR_DEFAULT)
    expect(parseSidebarWidth('')).toBe(SIDEBAR_DEFAULT)
    expect(parseSidebarWidth('abc')).toBe(SIDEBAR_DEFAULT)
    expect(parseSidebarWidth('0')).toBe(SIDEBAR_DEFAULT)
    expect(parseSidebarWidth('-40')).toBe(SIDEBAR_DEFAULT)
  })

  it('clamps a width stored under different bounds instead of discarding it', () => {
    expect(parseSidebarWidth('9999')).toBe(SIDEBAR_MAX)
    expect(parseSidebarWidth('50')).toBe(SIDEBAR_MIN)
  })

  it('keeps a width already inside the bounds', () => {
    expect(parseSidebarWidth('340')).toBe(340)
  })
})

describe('readSidebarWidth', () => {
  it('reads a persisted width', () => {
    stubStorage({ 'octo.sidebarWidth': '340' })
    expect(readSidebarWidth()).toBe(340)
  })

  it('defaults when nothing is stored', () => {
    stubStorage()
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT)
  })

  // The sidebar is always mounted, so an exception escaping here would take the
  // whole app tree down instead of degrading to the default width.
  it('survives storage that throws on read (privacy mode)', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => { throw new Error('SecurityError') },
      setItem: () => {},
    })
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT)
  })

  it('survives storage being absent entirely', () => {
    vi.stubGlobal('localStorage', undefined)
    expect(readSidebarWidth()).toBe(SIDEBAR_DEFAULT)
  })
})

describe('saveSidebarWidth', () => {
  it('rounds to whole pixels', () => {
    const store = stubStorage()
    saveSidebarWidth(287.6)
    expect(store['octo.sidebarWidth']).toBe('288')
  })

  it('survives storage that throws on write', () => {
    vi.stubGlobal('localStorage', {
      getItem: () => null,
      setItem: () => { throw new Error('QuotaExceededError') },
    })
    expect(() => saveSidebarWidth(300)).not.toThrow()
  })
})

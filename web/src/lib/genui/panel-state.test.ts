import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { loadPanelFields, savePanelField, pruneSessions, resetPanelState } from './panel-state'

const KEY = 'octo.genui-panel-state'

// jsdom under Node 26 exposes no localStorage — see the same stand-in and the
// same reasoning in unread.test.ts. Installed after the import above, so the
// module's own initial load() ran against nothing, which is the private-mode
// path and degrades to in-memory exactly as intended.
const backing = new Map<string, string>()
let writesThrow = false
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => {
    if (writesThrow) throw new Error('QuotaExceededError')
    backing.set(k, String(v))
  },
  removeItem: (k: string) => {
    backing.delete(k)
  },
  clear: () => backing.clear(),
})

beforeEach(() => {
  writesThrow = false
  // Reset first: it flushes an empty store, so clearing afterwards leaves the
  // backing map genuinely empty for "nothing written yet" assertions.
  resetPanelState()
  backing.clear()
})

afterEach(() => {
  vi.useRealTimers()
})

describe('panel state', () => {
  it('round-trips a field', () => {
    savePanelField('s1', 'sales', 'range', '30d')
    expect(loadPanelFields('s1', 'sales')).toEqual({ range: '30d' })
  })

  it('keeps value types intact', () => {
    savePanelField('s1', 'p', 'n', 30)
    savePanelField('s1', 'p', 'b', true)
    savePanelField('s1', 'p', 's', '30')
    const f = loadPanelFields('s1', 'p')
    // A slider's 30 must come back a number, not "30" — range predicates and
    // the action payload both depend on it.
    expect(f.n).toBe(30)
    expect(f.b).toBe(true)
    expect(f.s).toBe('30')
  })

  it('keeps panels and sessions separate', () => {
    savePanelField('s1', 'a', 'x', '1')
    savePanelField('s1', 'b', 'x', '2')
    savePanelField('s2', 'a', 'x', '3')
    expect(loadPanelFields('s1', 'a').x).toBe('1')
    expect(loadPanelFields('s1', 'b').x).toBe('2')
    expect(loadPanelFields('s2', 'a').x).toBe('3')
  })

  it('returns a copy, so a caller cannot mutate the store', () => {
    savePanelField('s1', 'p', 'x', '1')
    const f = loadPanelFields('s1', 'p')
    f.x = 'tampered'
    expect(loadPanelFields('s1', 'p').x).toBe('1')
  })

  it('is empty for an unknown panel', () => {
    expect(loadPanelFields('nope', 'nope')).toEqual({})
  })

  it('debounces the write but keeps reads immediate', () => {
    vi.useFakeTimers()
    savePanelField('s1', 'p', 'x', '1')
    // Visible right away…
    expect(loadPanelFields('s1', 'p').x).toBe('1')
    // …but not yet persisted, which is what keeps a slider drag cheap.
    expect(localStorage.getItem(KEY)).toBeNull()
    vi.advanceTimersByTime(250)
    expect(JSON.parse(localStorage.getItem(KEY) as string).s1.panels.p.x).toBe('1')
  })

  it('flushes a pending write when the tab is hidden', () => {
    vi.useFakeTimers()
    savePanelField('s1', 'p', 'x', 'mid-drag')
    expect(localStorage.getItem(KEY)).toBeNull()
    vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden')
    document.dispatchEvent(new Event('visibilitychange'))
    expect(JSON.parse(localStorage.getItem(KEY) as string).s1.panels.p.x).toBe('mid-drag')
  })

  it('drops sessions that no longer exist', () => {
    savePanelField('live', 'p', 'x', '1')
    savePanelField('deleted', 'p', 'x', '2')
    pruneSessions(['live'])
    expect(loadPanelFields('live', 'p').x).toBe('1')
    expect(loadPanelFields('deleted', 'p')).toEqual({})
  })

  it('evicts the least recently written past the session cap', () => {
    for (let i = 0; i < 55; i++) savePanelField(`s${i}`, 'p', 'x', String(i))
    // The earliest writes are gone; the most recent survive.
    expect(loadPanelFields('s0', 'p')).toEqual({})
    expect(loadPanelFields('s54', 'p').x).toBe('54')
  })

  it('survives a corrupt stored value', () => {
    localStorage.setItem(KEY, '{not json')
    resetPanelState()
    expect(loadPanelFields('s1', 'p')).toEqual({})
  })

  it('degrades to memory-only when storage refuses writes', () => {
    vi.useFakeTimers()
    writesThrow = true
    savePanelField('s1', 'p', 'x', '1')
    expect(() => vi.advanceTimersByTime(250)).not.toThrow()
    // The in-memory copy still serves the page.
    expect(loadPanelFields('s1', 'p').x).toBe('1')
  })
})

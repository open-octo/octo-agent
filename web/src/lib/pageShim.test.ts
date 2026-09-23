import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The script the server splices into every artifact and Light App page
// (internal/server/page_shim.js), run as shipped. vitest's cwd is web/.
const SHIM_JS = readFileSync(join(process.cwd(), '..', 'internal', 'server', 'page_shim.js'), 'utf8')

// A minimal Storage over a Map: the page's frame and the UI share one of these
// in the browser, which is the whole reason the shim exists.
function makeStorage(seed: Record<string, string> = {}) {
  const m = new Map(Object.entries(seed))
  return {
    map: m,
    get length() { return m.size },
    key: (i: number) => [...m.keys()][i] ?? null,
    getItem: (k: string) => (m.has(k) ? m.get(k)! : null),
    setItem: (k: string, v: string) => { m.set(k, String(v)) },
    removeItem: (k: string) => { m.delete(k) },
    clear: () => m.clear(),
  }
}

// Boot the shim in a stand-in window holding `real`, and return what the page
// would then see as `window.localStorage`.
function boot(ns: string, real: ReturnType<typeof makeStorage>) {
  const win: Record<string, unknown> = { localStorage: real, __octoPage: { ns, download: false } }
  win.parent = win // not framed: the download half stays off
  new Function('window', 'document', SHIM_JS)(win, {})
  return win.localStorage as Storage & Record<string, string>
}

describe('page shim localStorage namespace', () => {
  it('keeps the page under its prefix and away from the UI keys', () => {
    const real = makeStorage({ 'octo.panelWidth': '420', 'octo.themePack': 'azure' })
    const ls = boot('demo', real)

    ls.setItem('score', '10')
    ls.level = '3'
    expect(real.map.get('octo.page.demo:score')).toBe('10')
    expect(real.map.get('octo.page.demo:level')).toBe('3')
    expect(ls.getItem('score')).toBe('10')
    expect(ls.level).toBe('3')
    expect(ls.getItem('octo.panelWidth')).toBeNull() // the UI's keys are not the page's
    expect(ls.length).toBe(2)
    expect(Object.keys(ls).sort()).toEqual(['level', 'score'])
    expect('score' in ls).toBe(true)
    expect(ls.key(5)).toBeNull()

    ls.clear()
    expect(ls.length).toBe(0)
    expect(real.map.get('octo.panelWidth')).toBe('420')
    expect(real.map.get('octo.themePack')).toBe('azure')
  })

  it('keeps two pages apart', () => {
    const real = makeStorage()
    const a = boot('a', real)
    const b = boot('b', real)
    a.setItem('k', 'from-a')
    b.setItem('k', 'from-b')
    expect(a.getItem('k')).toBe('from-a')
    expect(b.getItem('k')).toBe('from-b')
    delete (a as Record<string, string>).k
    expect(a.getItem('k')).toBeNull()
    expect(b.getItem('k')).toBe('from-b')
  })

  it('reads what an earlier load of the same page wrote', () => {
    const real = makeStorage({ 'octo.page.demo:saved': 'yes' })
    expect(boot('demo', real).getItem('saved')).toBe('yes')
  })

  it('leaves a window without localStorage alone', () => {
    const win: Record<string, unknown> = { __octoPage: { ns: 'x' } }
    win.parent = win
    Object.defineProperty(win, 'localStorage', { get() { throw new DOMException('denied', 'SecurityError') } })
    expect(() => new Function('window', 'document', SHIM_JS)(win, {})).not.toThrow()
  })
})

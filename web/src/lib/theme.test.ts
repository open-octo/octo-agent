import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getPack, setPack, PACKS } from './theme'
import { en, zh } from './i18n'

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

  it('has an i18n label for every pack in both locales', () => {
    // $t(pack.labelKey) is a dynamic call, so i18n.coverage.test.ts's literal
    // scan cannot see these keys. A missing one renders the bare key as UI text
    // rather than failing, so it needs checking here.
    for (const pack of PACKS) {
      expect(en[pack.labelKey], `en is missing ${pack.labelKey}`).toBeTruthy()
      expect(zh[pack.labelKey], `zh is missing ${pack.labelKey}`).toBeTruthy()
    }
  })
})

// WCAG relative luminance, per the formula the contrast ratio is defined on.
function luminance(hex: string): number {
  const ch = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2]
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}

function block(css: string, selector: string): Record<string, string> {
  const start = css.indexOf(selector + ' {')
  if (start === -1) throw new Error(`no block for ${selector}`)
  const body = css.slice(start, css.indexOf('}', start))
  const vars: Record<string, string> = {}
  for (const m of body.matchAll(/(--[\w-]+):\s*([^;]+);/g)) vars[m[1]] = m[2].trim()
  return vars
}

// Every pack states an accent fill and the foreground that sits on it. White
// is only legible over azure's blue; a pack that keeps it while lightening the
// accent — which is exactly what the first cut of these packs did — lands
// between 1.9:1 and 2.6:1. The pairing is plain data, so it can be checked.
describe('pack accent contrast', () => {
  // vitest runs with cwd = web/, and import.meta.url is not a file:// URL
  // under jsdom — same reason i18n.coverage.test.ts resolves from cwd.
  const css = readFileSync(join(process.cwd(), 'src', 'app.css'), 'utf8')

  for (const pack of PACKS.filter((p) => p.id !== 'azure')) {
    const light = block(css, `:root[data-theme-pack="${pack.id}"]`)
    const dark = block(css, `:root[data-theme-pack="${pack.id}"][data-theme="dark"]`)

    it(`${pack.id} reaches AA on its accent in both modes`, () => {
      const fg = light['--on-accent']
      expect(fg, `${pack.id} must set --on-accent`).toBeTruthy()
      // The dark block deliberately omits --on-accent and inherits the light
      // one, so both modes are measured against the same foreground.
      expect(dark['--on-accent']).toBeUndefined()

      expect(contrast(light['--blue-6'], fg)).toBeGreaterThanOrEqual(4.5)
      expect(contrast(dark['--blue-6'], fg)).toBeGreaterThanOrEqual(4.5)
    })
  }
})

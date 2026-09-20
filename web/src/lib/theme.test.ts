import { readFileSync, readdirSync } from 'node:fs'
import { join } from 'node:path'
import { describe, it, expect, beforeEach, vi } from 'vitest'
import { getPack, setPack, PACKS, packs, loadUserPacks } from './theme'
import { listThemes } from './api'
import { get } from 'svelte/store'

vi.mock('./api', () => ({ listThemes: vi.fn() }))
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

// The set of known pack ids is module state that loadUserPacks replaces, so
// every case starts from "no user themes" — the state a fresh boot is in
// before the themes request comes back.
beforeEach(async () => {
  localStorage.clear()
  document.documentElement.removeAttribute('data-theme-pack')
  vi.mocked(listThemes).mockReset()
  vi.mocked(listThemes).mockResolvedValue([])
  await loadUserPacks()
  for (const el of document.querySelectorAll('link[data-octo-theme]')) el.remove()
})

// The themes octo ships are seeded to ~/.octo/themes and arrive as user
// themes, so a non-default pack only exists once they have loaded.
async function withOcean() {
  vi.mocked(listThemes).mockResolvedValue([{ id: 'ocean', name: 'Ocean' }])
  await loadUserPacks()
}

describe('getPack', () => {
  it('defaults to azure when nothing is stored', () => {
    expect(getPack()).toBe('azure')
  })

  it('returns a stored pack that is loaded', async () => {
    await withOcean()
    localStorage.setItem(KEY, 'ocean')
    expect(getPack()).toBe('ocean')
  })

  // celestia was retired for ocean. Someone who was on it gets ocean, not a
  // silent drop back to the default — but only once ocean itself has loaded.
  it('reads the retired pack id as the theme that replaced it', async () => {
    localStorage.setItem(KEY, 'celestia')
    expect(getPack()).toBe('azure')

    await withOcean()
    expect(getPack()).toBe('ocean')
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
  it('writes the attribute for a non-default pack', async () => {
    await withOcean()
    setPack('ocean')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBe('ocean')
    expect(getPack()).toBe('ocean')
  })

  // The default palette lives in bare :root, so it is addressed by the absence
  // of the attribute — writing data-theme-pack="azure" would match no rule.
  it('removes the attribute for the default pack', async () => {
    await withOcean()
    setPack('ocean')
    setPack('azure')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBeNull()
    expect(getPack()).toBe('azure')
  })

  it('normalizes an id the app does not ship instead of persisting it', () => {
    setPack('not-a-pack')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBeNull()
    expect(localStorage.getItem('octo.themePack')).toBe('azure')
    expect(getPack()).toBe('azure')
  })

  it('stores the current id when handed a renamed one', () => {
    setPack('klook')
    expect(localStorage.getItem('octo.themePack')).toBe('azure')
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

// The themes octo ships are files now (internal/server/themes, seeded to
// ~/.octo/themes on first run), not blocks in app.css. These two guards move
// with them: they check exactly what the docs tell a theme author to get
// right, against the themes we ourselves ship.
//
// vitest runs with cwd = web/, and import.meta.url is not a file:// URL under
// jsdom — same reason i18n.coverage.test.ts resolves from cwd.
const SEED_ROOT = join(process.cwd(), '..', 'internal', 'server', 'themes')
const SEED_IDS = readdirSync(SEED_ROOT, { withFileTypes: true })
  .filter((e) => e.isDirectory())
  .map((e) => e.name)
  .sort()

function seedCSS(id: string): string {
  return readFileSync(join(SEED_ROOT, id, 'theme.css'), 'utf8')
}

// Guard the guard: a rename or a move that empties this list would turn both
// suites below into silent no-ops.
describe('seed themes', () => {
  it('ships the themes the picker is expected to offer', () => {
    expect(SEED_IDS).toEqual(['blossom', 'ocean', 'vogue'])
  })
})

// A pack's light block does not outrank the default dark block on specificity
// — both are (0,2,0) — it only wins by being linked later. So in dark mode a
// var set only in a theme's light block overrides the default DARK value. For
// --radius-* and --font-* that is intended (the default dark block never sets
// them, so a theme states them once and gets both modes); for a color it means
// the theme's light value leaks into dark mode. CSS reports nothing when it
// happens, so this is the guard.
describe('theme light/dark blocks stay paired', () => {
  const appCSS = readFileSync(join(process.cwd(), 'src', 'app.css'), 'utf8')
  const defaultDark = block(appCSS, ':root[data-theme="dark"]')

  for (const id of SEED_IDS) {
    it(`${id} restates in dark every var the default dark block sets`, () => {
      const css = seedCSS(id)
      const light = block(css, `:root[data-theme-pack="${id}"]`)
      const dark = block(css, `:root[data-theme-pack="${id}"][data-theme="dark"]`)

      const leaked = Object.keys(light).filter((v) => v in defaultDark && !(v in dark))
      expect(
        leaked,
        `${id} sets these in its light block and the default dark block sets `
          + `them too, but its dark block does not — they would keep their light `
          + `values in dark mode: ${leaked.join(', ')}`,
      ).toEqual([])
    })
  }
})

// Every theme states an accent fill and the foreground that sits on it. White
// is only legible over a dark accent; a theme that keeps it while lightening
// the accent — which is exactly what the first cut of these packs did — lands
// between 1.9:1 and 2.6:1. The pairing is plain data, so it can be checked.
describe('theme accent contrast', () => {
  for (const id of SEED_IDS) {
    it(`${id} reaches AA on its accent in both modes`, () => {
      const css = seedCSS(id)
      const light = block(css, `:root[data-theme-pack="${id}"]`)
      const dark = block(css, `:root[data-theme-pack="${id}"][data-theme="dark"]`)

      const lightFg = light['--on-accent']
      expect(lightFg, `${id} must set --on-accent`).toBeTruthy()
      // A theme whose accent keeps its lightness across modes states one
      // foreground and inherits it into dark; one that flips a deep accent for
      // a bright one (ocean) has to state a second, or white would sit on a
      // near-white fill. Either shape is fine as long as both measure up.
      const darkFg = dark['--on-accent'] ?? lightFg
      const darkAccent = dark['--blue-6'] ?? light['--blue-6']

      expect(contrast(light['--blue-6'], lightFg)).toBeGreaterThanOrEqual(4.5)
      expect(contrast(darkAccent, darkFg)).toBeGreaterThanOrEqual(4.5)
    })
  }
})

describe('loadUserPacks', () => {
  // The pack list and the set of known ids are module state, so each case
  // starts by loading an empty set: that is also the path a user who deleted
  // their themes takes, and it must leave the built-ins standing.
  beforeEach(async () => {
    vi.mocked(listThemes).mockReset()
    vi.mocked(listThemes).mockResolvedValue([])
    await loadUserPacks()
    for (const el of document.querySelectorAll('link[data-octo-theme]')) el.remove()
    localStorage.clear()
    document.documentElement.removeAttribute('data-theme-pack')
  })

  it('links a user theme and lets its id pass normalization', async () => {
    vi.mocked(listThemes).mockResolvedValue([{ id: 'ocean', name: 'Ocean' }])
    localStorage.setItem(KEY, 'ocean')

    // Before the themes are known the id cannot be honoured, but the stored
    // choice must survive so it can be applied once they are.
    expect(getPack()).toBe('azure')
    expect(localStorage.getItem(KEY)).toBe('ocean')

    await loadUserPacks()

    expect(getPack()).toBe('ocean')
    expect(document.documentElement.getAttribute('data-theme-pack')).toBe('ocean')
    const link = document.querySelector('link[data-octo-theme="ocean"]')
    expect(link?.getAttribute('href')).toBe('/api/themes/ocean/theme.css')
  })

  it('adds user themes to the picker list after the built-ins', async () => {
    vi.mocked(listThemes).mockResolvedValue([
      { id: 'ocean', name: 'Ocean', author: 'someone' },
    ])
    await loadUserPacks()

    const list = get(packs)
    expect(list.length).toBe(PACKS.length + 1)
    const last = list[list.length - 1]
    expect(last.id).toBe('ocean')
    expect(last.label).toBe('Ocean')
    expect(last.author).toBe('someone')
    // A built-in names itself through i18n; a user theme never does.
    expect(last.labelKey).toBeUndefined()
  })

  it('falls back to a neutral swatch when the manifest has none', async () => {
    vi.mocked(listThemes).mockResolvedValue([{ id: 'ocean', name: 'Ocean' }])
    await loadUserPacks()

    const ocean = get(packs).find((p) => p.id === 'ocean')!
    expect(ocean.swatch[0]).toBe('var(--text-tertiary)')
    expect(ocean.swatch[1]).toBe('var(--bg-container)')
  })

  it('keeps a manifest swatch when it is a pair', async () => {
    vi.mocked(listThemes).mockResolvedValue([
      { id: 'ocean', name: 'Ocean', swatch: ['#0E7490', '#F0F7F9'] },
    ])
    await loadUserPacks()

    const ocean = get(packs).find((p) => p.id === 'ocean')!
    expect(ocean.swatch).toEqual(['#0E7490', '#F0F7F9'])
  })

  it('ignores a user theme that reuses a built-in id', async () => {
    vi.mocked(listThemes).mockResolvedValue([{ id: 'azure', name: 'Not azure' }])
    await loadUserPacks()

    expect(get(packs).length).toBe(PACKS.length)
    expect(document.querySelector('link[data-octo-theme="azure"]')).toBeNull()
  })

  it('leaves the app on the built-in packs when the request fails', async () => {
    vi.mocked(listThemes).mockRejectedValue(new Error('offline'))
    localStorage.setItem(KEY, 'ocean')

    await expect(loadUserPacks()).resolves.toBeUndefined()

    expect(get(packs).length).toBe(PACKS.length)
    expect(getPack()).toBe('azure')
  })

  it('links each stylesheet once across repeated loads', async () => {
    vi.mocked(listThemes).mockResolvedValue([{ id: 'ocean', name: 'Ocean' }])
    await loadUserPacks()
    await loadUserPacks()

    expect(document.querySelectorAll('link[data-octo-theme="ocean"]').length).toBe(1)
  })
})

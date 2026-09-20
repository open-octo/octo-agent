// Theme system — two orthogonal axes applied as <html> attributes:
//   data-theme       resolved mode: "light" | "dark"
//   data-theme-pack  palette family: "azure" (default) | one of PACKS below
//                    | the id of a user theme under ~/.octo/themes/
//
// The user's *choice* of mode is "light" | "dark" | "system"; "system" tracks
// the OS preference live. The two axes are independent: every pack ships both
// a light and a dark palette, so switching pack never changes mode.

import { writable } from 'svelte/store'
import { listThemes } from './api'

export type ThemeMode = 'light' | 'dark' | 'system'

const MODE_KEY = 'octo.themeMode'
const PACK_KEY = 'octo.themePack'
const DEFAULT_PACK = 'azure'

// Ids this app has shipped under a different name. A stored value listed here
// is read as its current id, so a user keeps the pack they chose across a
// rename. Renaming the *default* pack would survive without this — an unknown
// id already falls back to the default — but relying on that only works for
// that one pack, and silently stops working the day a pack reuses the old id.
// celestia was retired in favour of ocean, which now ships as a seed theme
// (internal/server/themes). Mapping it keeps anyone who was using it on a
// theme rather than dropping them back to the default.
const RENAMED_PACKS: Record<string, string> = { klook: 'azure', celestia: 'ocean' }

// The one pack compiled into the app: something has to render before any
// theme file is read, and that is the default. Every other theme octo ships
// (ocean, blossom, vogue) is seeded to ~/.octo/themes/ on first run and
// arrives here through loadUserPacks like a theme anyone else wrote — which
// is the point: they are editable, deletable, and worked examples.
//
// `swatch` is the pair the picker draws — accent over surface, in the pack's
// *light* palette — so the control can show what it looks like without
// applying it. `labelKey` is the i18n key for the display name.
export type ThemePack = {
  id: string
  labelKey: string
  swatch: [accent: string, surface: string]
}

export const PACKS: ThemePack[] = [
  { id: 'azure', labelKey: 'settings.pack_azure', swatch: ['#007AFF', '#F5F5F7'] },
]

const PACK_IDS = new Set(PACKS.map((p) => p.id))

let systemMql: MediaQueryList | null = null
let systemListener: ((e: MediaQueryListEvent) => void) | null = null

// ─── User theme packs ───────────────────────────────────────────────────────
//
// A theme under ~/.octo/themes/<id>/ is a manifest plus a stylesheet that
// redefines the same variables app.css declares, scoped to its own
// `[data-theme-pack="<id>"]`. The server lists and serves them; this module
// links the stylesheet and lets the id pass normalizePack.

// What the picker renders: the built-in packs name their label through i18n, a
// user theme carries the name its manifest gave. Kept separate from ThemePack
// so the built-in list stays a compile-time constant with a required labelKey.
export type PackChoice = {
  id: string
  labelKey?: string
  label?: string
  // Per-locale names from the manifest; the picker prefers the current
  // locale's and falls back to `label`. This is how the seeded themes keep
  // the Chinese names they had as i18n keys (少女, 时尚, 深海).
  labels?: Record<string, string>
  swatch: [accent: string, surface: string]
  author?: string
  homepage?: string
}

// A user theme without a swatch still needs a chip. Current-colour over the
// container surface reads as "no preview", which is honest, and both values are
// ours rather than the manifest's.
const NEUTRAL_SWATCH: [string, string] = ['var(--text-tertiary)', 'var(--bg-container)']

const builtinChoices = (): PackChoice[] =>
  PACKS.map((p) => ({ id: p.id, labelKey: p.labelKey, swatch: p.swatch }))

// Ids discovered at runtime. Empty until loadUserPacks resolves — see initTheme
// for why boot does not wait on it.
let userPackIDs = new Set<string>()

// The picker's list. Replaced wholesale once user themes arrive.
export const packs = writable<PackChoice[]>(builtinChoices())

export function getMode(): ThemeMode {
  return (localStorage.getItem(MODE_KEY) as ThemeMode) || 'light'
}

// A pack id that is no longer shipped (a removed pack, a hand-edited value)
// falls back to the default rather than writing an attribute no CSS matches,
// which would otherwise leave the app on the default palette with the picker
// showing nothing selected. A user theme counts as shipped once its stylesheet
// is linked, and stops counting the moment its directory goes away.
function normalizePack(id: string | null): string {
  if (!id) return DEFAULT_PACK
  const current = RENAMED_PACKS[id] ?? id
  return PACK_IDS.has(current) || userPackIDs.has(current) ? current : DEFAULT_PACK
}

export function getPack(): string {
  return normalizePack(localStorage.getItem(PACK_KEY))
}

function prefersDark(): boolean {
  return typeof matchMedia !== 'undefined' && matchMedia('(prefers-color-scheme: dark)').matches
}

function resolveMode(mode: ThemeMode): 'light' | 'dark' {
  if (mode === 'system') return prefersDark() ? 'dark' : 'light'
  return mode
}

// apply writes the attributes and, for "system", installs a listener so the
// app follows the OS theme as it changes.
function apply(mode: ThemeMode, pack: string): void {
  const root = document.documentElement
  root.setAttribute('data-theme', resolveMode(mode))
  // The default pack lives in :root, so only set the attribute for others.
  if (pack && pack !== DEFAULT_PACK) root.setAttribute('data-theme-pack', pack)
  else root.removeAttribute('data-theme-pack')

  if (systemMql && systemListener) {
    systemMql.removeEventListener('change', systemListener)
    systemMql = null
    systemListener = null
  }
  if (mode === 'system' && typeof matchMedia !== 'undefined') {
    systemMql = matchMedia('(prefers-color-scheme: dark)')
    systemListener = () => {
      document.documentElement.setAttribute('data-theme', prefersDark() ? 'dark' : 'light')
    }
    systemMql.addEventListener('change', systemListener)
  }
}

export function setMode(mode: ThemeMode): void {
  localStorage.setItem(MODE_KEY, mode)
  apply(mode, getPack())
}

// Normalizes the same way getPack does, so the two can never disagree: an id
// the app does not ship would otherwise be persisted and written as an
// attribute no CSS matches, while getPack went on reporting the default.
export function setPack(pack: string): void {
  const id = normalizePack(pack)
  localStorage.setItem(PACK_KEY, id)
  apply(getMode(), id)
}

// initTheme applies the persisted choice on boot. Call once at app start.
export function initTheme(): void {
  apply(getMode(), getPack())
}

// linkThemeStylesheet appends a user theme's stylesheet. Order matters: the
// default dark palette (`:root[data-theme="dark"]` in app.css) has the same
// specificity as a pack's own block, so a theme only wins by coming later in
// the cascade — which appending to <head> guarantees, app.css being an import
// of the entry module.
function linkThemeStylesheet(id: string): void {
  if (document.querySelector(`link[data-octo-theme="${id}"]`)) return
  const link = document.createElement('link')
  link.rel = 'stylesheet'
  link.href = `/api/themes/${encodeURIComponent(id)}/theme.css`
  link.setAttribute('data-octo-theme', id)
  document.head.appendChild(link)
}

// loadUserPacks discovers the themes under ~/.octo/themes/, links them and
// re-applies the stored choice.
//
// The re-apply is the point: boot runs before this resolves, so a stored user
// theme id normalized to the default — getPack only reads, never writes back,
// so the choice itself survived and simply needs applying once its stylesheet
// is known. A user on a built-in pack sees nothing happen.
//
// A failure here is not worth surfacing: the app is fully usable on the
// built-in packs, and the picker just shows four entries instead of five.
export async function loadUserPacks(): Promise<void> {
  let themes
  try {
    themes = await listThemes()
  } catch {
    // A failed request says nothing about what is on disk, so the list is left
    // exactly as it is rather than being emptied.
    return
  }

  const choices: PackChoice[] = []
  for (const th of themes) {
    if (PACK_IDS.has(th.id)) continue
    linkThemeStylesheet(th.id)
    choices.push({
      id: th.id,
      label: th.name || th.id,
      labels: th.names,
      swatch:
        th.swatch && th.swatch.length === 2
          ? [th.swatch[0], th.swatch[1]]
          : NEUTRAL_SWATCH,
      author: th.author,
      homepage: th.homepage,
    })
  }

  // An empty answer is a real answer — the user deleted their themes — so the
  // list and the known ids are rebuilt either way, and the re-apply drops
  // anyone whose pack just stopped existing back to the default.
  userPackIDs = new Set(choices.map((c) => c.id))
  packs.set([...builtinChoices(), ...choices])
  apply(getMode(), getPack())
}

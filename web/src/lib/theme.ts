// Theme system — two orthogonal axes applied as <html> attributes:
//   data-theme       resolved mode: "light" | "dark"
//   data-theme-pack  palette family: "azure" (default) | one of PACKS below
//
// The user's *choice* of mode is "light" | "dark" | "system"; "system" tracks
// the OS preference live. The two axes are independent: every pack ships both
// a light and a dark palette, so switching pack never changes mode.

export type ThemeMode = 'light' | 'dark' | 'system'

const MODE_KEY = 'octo.themeMode'
const PACK_KEY = 'octo.themePack'
const DEFAULT_PACK = 'azure'

// Ids this app has shipped under a different name. A stored value listed here
// is read as its current id, so a user keeps the pack they chose across a
// rename. Renaming the *default* pack would survive without this — an unknown
// id already falls back to the default — but relying on that only works for
// that one pack, and silently stops working the day a pack reuses the old id.
const RENAMED_PACKS: Record<string, string> = { klook: 'azure' }

// Packs, in the order the settings picker lists them. `swatch` is the pair the
// picker draws — accent over surface, in that pack's *light* palette — purely
// so the control can show what a pack looks like without applying it; the real
// values all live in app.css. `labelKey` is the i18n key for the display name.
export type ThemePack = {
  id: string
  labelKey: string
  swatch: [accent: string, surface: string]
}

export const PACKS: ThemePack[] = [
  { id: 'azure', labelKey: 'settings.pack_azure', swatch: ['#007AFF', '#F5F5F7'] },
  { id: 'blossom', labelKey: 'settings.pack_blossom', swatch: ['#FF6FA5', '#FFF5F8'] },
  { id: 'celestia', labelKey: 'settings.pack_celestia', swatch: ['#2BB3FF', '#F0F9FF'] },
  { id: 'vogue', labelKey: 'settings.pack_vogue', swatch: ['#C0A062', '#FAFAFA'] },
]

const PACK_IDS = new Set(PACKS.map((p) => p.id))

let systemMql: MediaQueryList | null = null
let systemListener: ((e: MediaQueryListEvent) => void) | null = null

export function getMode(): ThemeMode {
  return (localStorage.getItem(MODE_KEY) as ThemeMode) || 'light'
}

// A pack id that is no longer shipped (a removed pack, a hand-edited value)
// falls back to the default rather than writing an attribute no CSS matches,
// which would otherwise leave the app on the default palette with the picker
// showing nothing selected.
export function getPack(): string {
  const stored = localStorage.getItem(PACK_KEY)
  if (!stored) return DEFAULT_PACK
  const current = RENAMED_PACKS[stored] ?? stored
  return PACK_IDS.has(current) ? current : DEFAULT_PACK
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

export function setPack(pack: string): void {
  localStorage.setItem(PACK_KEY, pack)
  apply(getMode(), pack)
}

// initTheme applies the persisted choice on boot. Call once at app start.
export function initTheme(): void {
  apply(getMode(), getPack())
}

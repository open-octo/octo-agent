// Theme system — two orthogonal axes applied as <html> attributes:
//   data-theme       resolved mode: "light" | "dark"
//   data-theme-pack  palette family: "klook" (default) | future packs
//
// The user's *choice* of mode is "light" | "dark" | "system"; "system" tracks
// the OS preference live. Pack is reserved for future multi-pack support — the
// API already round-trips it so adding packs needs no call-site changes.

export type ThemeMode = 'light' | 'dark' | 'system'

const MODE_KEY = 'octo.themeMode'
const PACK_KEY = 'octo.themePack'
const DEFAULT_PACK = 'klook'

let systemMql: MediaQueryList | null = null
let systemListener: ((e: MediaQueryListEvent) => void) | null = null

export function getMode(): ThemeMode {
  return (localStorage.getItem(MODE_KEY) as ThemeMode) || 'light'
}

export function getPack(): string {
  return localStorage.getItem(PACK_KEY) || DEFAULT_PACK
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

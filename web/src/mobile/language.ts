// The phone's own UI language. 'server' follows the server's configured
// language like every other client; 'en' / 'zh' pin this device only, so a
// phone can read Chinese while the desktop stays English (or the reverse)
// without touching the shared config.
import { setLocale } from '../lib/i18n'

export type LangChoice = 'server' | 'en' | 'zh'

const KEY = 'octo.mobileLanguage'

export function getLang(): LangChoice {
  try {
    const v = localStorage.getItem(KEY)
    return v === 'en' || v === 'zh' ? v : 'server'
  } catch {
    return 'server'
  }
}

// The pinned locale, or null when the server's language applies.
export function pinnedLocale(): 'en' | 'zh' | null {
  const c = getLang()
  return c === 'server' ? null : c
}

// serverLanguage is the config's language, applied when switching back to
// following the server.
export function setLang(c: LangChoice, serverLanguage: string): void {
  try {
    if (c === 'server') localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, c)
  } catch { /* storage blocked: the choice lasts this run only */ }
  setLocale(c === 'server' ? serverLanguage || 'en' : c)
}

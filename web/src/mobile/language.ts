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

// Bumped by every choice, so a server-language fetch that resolves after a
// later pick is dropped instead of undoing it.
let choiceSeq = 0

export function pinLang(l: 'en' | 'zh'): void {
  choiceSeq++
  try {
    localStorage.setItem(KEY, l)
  } catch { /* storage blocked: the choice lasts this run only */ }
  setLocale(l)
}

// Unpins at once, then applies the server's language once fetched. A failed
// fetch or an empty language leaves the current locale, as boot does.
export async function followServer(fetchLanguage: () => Promise<string>): Promise<void> {
  const seq = ++choiceSeq
  try {
    localStorage.removeItem(KEY)
  } catch { /* nothing stored to clear */ }
  const l = await fetchLanguage().catch(() => '')
  if (seq === choiceSeq && l) setLocale(l)
}

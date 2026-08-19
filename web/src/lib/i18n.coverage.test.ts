import { describe, it, expect } from 'vitest'
import { readFileSync, readdirSync, statSync } from 'node:fs'
import { join, extname } from 'node:path'
import { en, zh } from './i18n'

// A key used by a component but absent from the tables is not a crash: t()
// falls back to the raw key, so the UI silently renders "chat.cancel" as a
// tooltip. That shipped once (ChatView's cancel-edit button), which is why
// this sweeps the whole source tree instead of listing keys by hand.
//
// Only literal `$t('some.key')` / `t('some.key')` call sites are visible here;
// keys assembled at runtime (e.g. PERM_LABEL_KEY[mode]) are invisible to a
// regex and are the reason this file never asserts the reverse direction —
// "defined but unused" would flag those as dead and invite a wrong deletion.
// vitest runs with cwd = web/ (see vitest.config.ts); import.meta.url is not
// a file:// URL under the jsdom environment, so resolve from cwd instead.
const SRC = join(process.cwd(), 'src')
const KEY_CALL = /\$?\bt\(\s*'([A-Za-z0-9_.]+)'/g

function sourceFiles(dir: string): string[] {
  const out: string[] = []
  for (const name of readdirSync(dir)) {
    const p = join(dir, name)
    if (statSync(p).isDirectory()) {
      out.push(...sourceFiles(p))
      continue
    }
    if (!['.svelte', '.ts'].includes(extname(name))) continue
    if (name.includes('i18n') || name.endsWith('.test.ts')) continue
    out.push(p)
  }
  return out
}

function usedKeys(): Map<string, string> {
  const first = new Map<string, string>()
  for (const file of sourceFiles(SRC)) {
    const lines = readFileSync(file, 'utf8').split('\n')
    lines.forEach((line, i) => {
      for (const m of line.matchAll(KEY_CALL)) {
        if (!first.has(m[1])) first.set(m[1], `${file.slice(SRC.length + 1)}:${i + 1}`)
      }
    })
  }
  return first
}

describe('i18n coverage', () => {
  it('every literal key used in the app is defined in both locales', () => {
    const missing: string[] = []
    for (const [key, where] of usedKeys()) {
      if (!(key in en)) missing.push(`${key} (en) — ${where}`)
      if (!(key in zh)) missing.push(`${key} (zh) — ${where}`)
    }
    expect(missing).toEqual([])
  })

  it('finds a meaningful number of call sites', () => {
    // Guards the guard: a regex that silently stops matching would make the
    // test above pass vacuously.
    expect(usedKeys().size).toBeGreaterThan(200)
  })

  it('en and zh define exactly the same keys', () => {
    expect(Object.keys(zh).sort()).toEqual(Object.keys(en).sort())
  })

  it('no key maps to an empty string', () => {
    const blank = [
      ...Object.entries(en).filter(([, v]) => !v.trim()).map(([k]) => `en:${k}`),
      ...Object.entries(zh).filter(([, v]) => !v.trim()).map(([k]) => `zh:${k}`),
    ]
    expect(blank).toEqual([])
  })
})

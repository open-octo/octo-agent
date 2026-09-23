import { describe, it, expect, vi } from 'vitest'
import 'fake-indexeddb/auto'
import { get } from 'svelte/store'
import { ensureLightAppStorage, lightappStorageReady, migratedKey, pageKeyPrefix, LA_DB_NAME, LA_STORE, EXPORT_TIMEOUT_MS } from './laStorage'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts). One Map
// stands in for the storage the UI and its app frames share.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  get length() { return backing.size },
  key: (i: number) => [...backing.keys()][i] ?? null,
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

// Unique slug per test: the module caches its IndexedDB connection and its
// per-slug migration promise, so the slug is the isolation.
let n = 0
const slug = () => 'app-' + n++

// What the old srcdoc shim left behind: rows keyed `{ns}:{key}` in the host's
// IndexedDB, plus the marker the retired origin's migration set.
function seedIdb(rows: Record<string, unknown>): Promise<void> {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open(LA_DB_NAME, 1)
    req.onupgradeneeded = () => {
      const db = req.result
      if (!db.objectStoreNames.contains(LA_STORE)) db.createObjectStore(LA_STORE)
    }
    req.onerror = () => reject(req.error)
    req.onsuccess = () => {
      const db = req.result
      const t = db.transaction(LA_STORE, 'readwrite')
      const s = t.objectStore(LA_STORE)
      for (const [k, v] of Object.entries(rows)) s.put(v, k)
      t.oncomplete = () => { db.close(); resolve() }
      t.onerror = () => reject(t.error)
    }
  })
}

// jsdom's URL is http://localhost:3000, so the host frames the retired
// origin's export page; answer from it the way the page does.
async function answerExport(s: string, value: Record<string, string>, origin = `http://${s}.apps.localhost:3000`) {
  await vi.waitFor(() => {
    if (!exportFrame(s)) throw new Error('no export frame yet')
  })
  const frame = exportFrame(s)!
  window.dispatchEvent(new MessageEvent('message', {
    data: { __laBridge: 1, op: 'export', ns: s, value },
    source: frame.contentWindow,
    origin,
  }))
}

function exportFrame(s: string): HTMLIFrameElement | undefined {
  return [...document.querySelectorAll('iframe')].find((f) => f.src === `http://${s}.apps.localhost:3000/__octo_export`)
}

const keysOf = (s: string) =>
  Object.fromEntries([...backing].filter(([k]) => k.startsWith(pageKeyPrefix(s))).map(([k, v]) => [k.slice(pageKeyPrefix(s).length), v]))

describe('Light App storage migration', () => {
  it('moves the retired origin and the legacy rows into the namespace, newer origin first', async () => {
    const s = slug()
    await seedIdb({ [`${s}:score`]: '1', [`${s}:legacy`]: 'idb' })
    const done = ensureLightAppStorage(s)
    await answerExport(s, { score: '10', name: 'x' })
    await done

    expect(keysOf(s)).toEqual({ score: '10', name: 'x', legacy: 'idb' })
    expect(backing.get(migratedKey(s))).toBe('1')
    expect(get(lightappStorageReady).has(s)).toBe(true)
    expect(exportFrame(s)).toBeUndefined() // the helper frame is gone
  })

  it('never overwrites what the namespace already holds', async () => {
    const s = slug()
    backing.set(pageKeyPrefix(s) + 'score', 'newer')
    const done = ensureLightAppStorage(s)
    await answerExport(s, { score: 'old', extra: 'y' })
    await done
    expect(keysOf(s)).toEqual({ score: 'newer', extra: 'y' })
  })

  it('skips legacy rows the retired origin already took', async () => {
    const s = slug()
    await seedIdb({ [`${s}:gone`]: 'deleted-since', [`__octo_migrated__:${s}`]: 1 })
    const done = ensureLightAppStorage(s)
    await answerExport(s, {})
    await done
    expect(keysOf(s)).toEqual({})
  })

  it('runs once: a migrated app opens without the export frame', async () => {
    const s = slug()
    backing.set(migratedKey(s), '1')
    await ensureLightAppStorage(s)
    expect(exportFrame(s)).toBeUndefined()
    expect(get(lightappStorageReady).has(s)).toBe(true)
  })

  it('ignores an export from anywhere but its own frame and origin', async () => {
    const s = slug()
    const done = ensureLightAppStorage(s)
    await answerExport(s, { evil: '1' }, 'http://evil.example')
    await answerExport(s, { good: '1' })
    await done
    expect(keysOf(s)).toEqual({ good: '1' })
  })

  it('opens the app without the export when the page never answers', async () => {
    vi.useFakeTimers()
    try {
      const s = slug()
      const done = ensureLightAppStorage(s)
      await vi.advanceTimersByTimeAsync(EXPORT_TIMEOUT_MS + 10)
      await done
      expect(get(lightappStorageReady).has(s)).toBe(true)
      expect(backing.get(migratedKey(s))).toBe('1')
    } finally {
      vi.useRealTimers()
    }
  })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts), and stores
// touches it on import.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

const getLanding = vi.fn()
vi.mock('./api', () => ({ getLanding: () => getLanding() }))

// The read is once per page load, so each case needs its own module instance
// rather than a reset hook that only tests would use.
async function freshStores() {
  vi.resetModules()
  return await import('./stores')
}

beforeEach(() => {
  getLanding.mockReset()
})

describe('loadLanding', () => {
  it('reads once however many callers ask', async () => {
    getLanding.mockResolvedValue({ title: 'mine' })
    const { landing, loadLanding } = await freshStores()

    await Promise.all([loadLanding(), loadLanding(), loadLanding()])

    expect(getLanding).toHaveBeenCalledTimes(1)
    expect(get(landing).title).toBe('mine')
  })

  it('leaves the built-in cards in place when the request fails', async () => {
    getLanding.mockRejectedValue(new Error('offline'))
    const { landing, loadLanding } = await freshStores()

    await expect(loadLanding()).resolves.toBeUndefined()
    expect(get(landing)).toEqual({})
  })
})

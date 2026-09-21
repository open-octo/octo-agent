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
const listLightApps = vi.fn()
vi.mock('./api', () => ({
  getLanding: () => getLanding(),
  listLightApps: () => listLightApps(),
}))

// The module holds the config in a store, so each case needs its own instance
// rather than a reset hook that only tests would use.
async function freshStores() {
  vi.resetModules()
  return await import('./stores')
}

beforeEach(() => {
  getLanding.mockReset()
  listLightApps.mockReset()
  listLightApps.mockResolvedValue([])
})

describe('loadLanding', () => {
  it('shares one request between callers asking at the same time', async () => {
    getLanding.mockResolvedValue({ title: 'mine' })
    const { landing, loadLanding } = await freshStores()

    await Promise.all([loadLanding(), loadLanding(), loadLanding()])

    expect(getLanding).toHaveBeenCalledTimes(1)
    expect(get(landing).title).toBe('mine')
  })

  // The config is a file the user edits in another window, and the agent
  // rewrites it mid-session. The desktop shell has no refresh, so a read
  // cached for the page's lifetime meant quitting the app to see the edit.
  it('reads again once the previous read has settled', async () => {
    getLanding.mockResolvedValueOnce({ title: 'first' }).mockResolvedValueOnce({ title: 'second' })
    const { landing, loadLanding } = await freshStores()

    await loadLanding()
    expect(get(landing).title).toBe('first')

    await loadLanding()

    expect(getLanding).toHaveBeenCalledTimes(2)
    expect(get(landing).title).toBe('second')
  })

  // A failed read must not wedge the sharing slot shut, or the next visit to
  // the start screen would never ask again.
  it('asks again after a failed read', async () => {
    getLanding.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ title: 'back' })
    const { landing, loadLanding } = await freshStores()

    await loadLanding()
    await loadLanding()

    expect(getLanding).toHaveBeenCalledTimes(2)
    expect(get(landing).title).toBe('back')
  })

  it('leaves the built-in cards in place when the request fails', async () => {
    getLanding.mockRejectedValue(new Error('offline'))
    const { landing, loadLanding } = await freshStores()

    await expect(loadLanding()).resolves.toBeUndefined()
    expect(get(landing)).toEqual({})
  })
})

// The start screen resolves its hero app and its pinned shortcuts against the
// installed Light Apps, so that list has to be re-readable for the same reason
// the config is: the agent can create an app and pin it in the same breath.
describe('loadLightApps', () => {
  it('reads again once the previous read has settled', async () => {
    listLightApps
      .mockResolvedValueOnce([{ slug: 'first', name: 'First' }])
      .mockResolvedValueOnce([{ slug: 'first', name: 'First' }, { slug: 'second', name: 'Second' }])
    const { lightapps, loadLightApps } = await freshStores()

    await loadLightApps()
    expect(get(lightapps).map((a) => a.slug)).toEqual(['first'])

    await loadLightApps()

    expect(listLightApps).toHaveBeenCalledTimes(2)
    expect(get(lightapps).map((a) => a.slug)).toEqual(['first', 'second'])
  })

  it('shares one request between callers asking at the same time', async () => {
    const { loadLightApps } = await freshStores()

    await Promise.all([loadLightApps(), loadLightApps(), loadLightApps()])

    expect(listLightApps).toHaveBeenCalledTimes(1)
  })
})

// The gate both callers share — an effect on the stores, and a focus listener
// reading them with get(). One answer, so they cannot drift apart.
describe('onStartScreen', () => {
  it('is the blank chat, on a desktop-shaped UI', async () => {
    const { onStartScreen } = await freshStores()

    expect(onStartScreen('chat', null, false)).toBe(true)
    expect(onStartScreen('chat', '', false)).toBe(true)
  })

  it('is not a session, another view, or the mobile UI', async () => {
    const { onStartScreen } = await freshStores()

    expect(onStartScreen('chat', 'sess-1', false)).toBe(false)
    expect(onStartScreen('skills', null, false)).toBe(false)
    // Mobile renders none of the start screen, so it must not pay for it —
    // a phone browser fires focus on every tab switch.
    expect(onStartScreen('chat', null, true)).toBe(false)
  })
})

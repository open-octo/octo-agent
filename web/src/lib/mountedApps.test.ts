import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { lightapps, localAccess, mountedViews, lightappURL, loadLightApps } from './stores'
import * as api from './api'
import type { LightApp } from './api'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts), and stores
// touches it on import.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

// `mount` is widened past the client type on purpose: a stale app on disk can
// still carry the retired "panel", and the store has to hold the line.
function app(slug: string, mount?: string): LightApp {
  return { slug, name: slug, description: '', icon: '🎨', created_at: '', mount: mount as LightApp['mount'] }
}

beforeEach(() => {
  lightapps.set([])
  localAccess.set(true)
  document.documentElement.removeAttribute('data-theme')
})

describe('mounted Light Apps', () => {
  it('picks out the apps claiming their own page', () => {
    lightapps.set([app('plain'), app('as-view', 'view')])

    expect(get(mountedViews).map((a) => a.slug)).toEqual(['as-view'])
  })

  // "panel" was the other placement once. The server normalises it away, but
  // an app carrying it must never reappear in the navigation on the strength
  // of the word alone.
  it('gives the retired panel placement nothing', () => {
    lightapps.set([app('as-panel', 'panel')])

    expect(get(mountedViews)).toEqual([])
  })

  it('offers the same entries to a remote browser', () => {
    // The app renders from a path on the server's own origin, so a browser
    // behind a tunnel or on another machine loads it just the same.
    lightapps.set([app('as-view', 'view')])
    localAccess.set(false)

    expect(get(mountedViews).map((a) => a.slug)).toEqual(['as-view'])
  })

  it('treats an app claiming no mount as belonging to the Light Apps page only', () => {
    lightapps.set([app('plain')])

    expect(get(mountedViews)).toEqual([])
  })
})

describe('loadLightApps', () => {
  // A switch writes, then re-reads. A read already in flight may have left
  // before the write; handing that one back would put the old list on screen.
  it('fresh waits out a read in flight and reads again after it', async () => {
    let releaseStale!: (v: LightApp[]) => void
    const calls: string[] = []
    const spy = vi.spyOn(api, 'listLightApps')
      .mockImplementationOnce(() => { calls.push('stale'); return new Promise((r) => { releaseStale = r }) })
      .mockImplementationOnce(async () => { calls.push('fresh'); return [app('as-view', 'view')] })

    const focusRead = loadLightApps()
    const afterWrite = loadLightApps({ fresh: true })
    releaseStale([app('as-view')])
    await Promise.all([focusRead, afterWrite])

    expect(calls).toEqual(['stale', 'fresh'])
    expect(get(mountedViews).map((a) => a.slug)).toEqual(['as-view'])
    spy.mockRestore()
  })

  it('shares a read in flight when freshness does not matter', async () => {
    let release!: (v: LightApp[]) => void
    const spy = vi.spyOn(api, 'listLightApps').mockImplementation(() => new Promise((r) => { release = r }))
    const a = loadLightApps()
    const b = loadLightApps()
    release([])
    await Promise.all([a, b])
    expect(spy).toHaveBeenCalledTimes(1)
    spy.mockRestore()
  })
})

describe('lightappURL', () => {
  // A path on this page's own origin, whatever host the UI was reached on.
  it('addresses the app under /_apps/ on this origin, carrying the resolved theme', () => {
    document.documentElement.setAttribute('data-theme', 'dark')
    const url = new URL(lightappURL('sketch', 3), 'https://octo.example.com')

    expect(url.origin).toBe('https://octo.example.com')
    expect(url.pathname).toBe('/_apps/sketch/')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('v')).toBe('3')
  })

  it('escapes the slug into one path segment', () => {
    expect(lightappURL('a b').startsWith('/_apps/a%20b/?')).toBe(true)
  })

  it('reports light for any theme that is not dark', () => {
    document.documentElement.setAttribute('data-theme', 'light')
    expect(new URL(lightappURL('sketch'), 'http://localhost').searchParams.get('theme')).toBe('light')
  })
})

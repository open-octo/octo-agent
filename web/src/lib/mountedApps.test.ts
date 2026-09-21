import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { lightapps, localAccess, mountedViews, lightappURL } from './stores'
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

  it('offers nothing to a remote browser', () => {
    // A mounted entry there would open a frame on <slug>.apps.localhost, which
    // resolves to the viewer's own machine rather than the server's — better no
    // entry at all than one that cannot load.
    lightapps.set([app('as-view', 'view')])
    localAccess.set(false)

    expect(get(mountedViews)).toEqual([])
  })

  it('treats an app claiming no mount as belonging to the Light Apps page only', () => {
    lightapps.set([app('plain')])

    expect(get(mountedViews)).toEqual([])
  })
})

describe('lightappURL', () => {
  // Parsed rather than matched as a string: a prefix check would also accept
  // sketch.apps.localhost.example.com, and the host is the whole point of the
  // app having an origin of its own.
  it('addresses the app on its own origin, carrying the resolved theme', () => {
    document.documentElement.setAttribute('data-theme', 'dark')
    const url = new URL(lightappURL('sketch', 3))

    expect(url.protocol).toBe('http:')
    expect(url.hostname).toBe('sketch.apps.localhost')
    expect(url.searchParams.get('theme')).toBe('dark')
    expect(url.searchParams.get('v')).toBe('3')
  })

  it('reports light for any theme that is not dark', () => {
    document.documentElement.setAttribute('data-theme', 'light')
    expect(new URL(lightappURL('sketch')).searchParams.get('theme')).toBe('light')
  })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { lightapps, localAccess, mountedViews, mountedPanels, lightappURL } from './stores'
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

function app(slug: string, mount?: 'view' | 'panel'): LightApp {
  return { slug, name: slug, description: '', icon: '🎨', created_at: '', mount }
}

beforeEach(() => {
  lightapps.set([])
  localAccess.set(true)
  document.documentElement.removeAttribute('data-theme')
})

describe('mounted Light Apps', () => {
  it('splits the installed list by what each app claims', () => {
    lightapps.set([app('plain'), app('as-view', 'view'), app('as-panel', 'panel')])

    expect(get(mountedViews).map((a) => a.slug)).toEqual(['as-view'])
    expect(get(mountedPanels).map((a) => a.slug)).toEqual(['as-panel'])
  })

  it('offers nothing to a remote browser', () => {
    // A mounted entry there would open a frame on <slug>.apps.localhost, which
    // resolves to the viewer's own machine rather than the server's — better no
    // entry at all than one that cannot load.
    lightapps.set([app('as-view', 'view'), app('as-panel', 'panel')])
    localAccess.set(false)

    expect(get(mountedViews)).toEqual([])
    expect(get(mountedPanels)).toEqual([])
  })

  it('treats an app claiming no mount as belonging to the Light Apps page only', () => {
    lightapps.set([app('plain')])

    expect(get(mountedViews)).toEqual([])
    expect(get(mountedPanels)).toEqual([])
  })
})

describe('lightappURL', () => {
  it('addresses the app on its own origin, carrying the resolved theme', () => {
    document.documentElement.setAttribute('data-theme', 'dark')
    const url = lightappURL('sketch', 3)

    expect(url.startsWith('http://sketch.apps.localhost')).toBe(true)
    expect(url).toContain('theme=dark')
    expect(url).toContain('v=3')
  })

  it('reports light for any theme that is not dark', () => {
    document.documentElement.setAttribute('data-theme', 'light')
    expect(lightappURL('sketch')).toContain('theme=light')
  })
})

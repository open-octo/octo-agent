import { describe, it, expect, vi } from 'vitest'
import { _api, iconLoaded, loadIcon } from 'iconify-icon'
import { initIcons } from './icons'

// Captured at import time, before any test calls initIcons. iconify grabs its
// own fetch reference when the module loads, so spying on globalThis.fetch
// cannot observe what it does — comparing that captured reference is the only
// way to see the network path actually being closed.
const fetchBeforeInit = _api.getFetch()

const iconPrototype = () =>
  (customElements.get('iconify-icon') as unknown as { prototype: { startObserver?: unknown } }).prototype
const startObserverBeforeInit = iconPrototype().startObserver

describe('initIcons', () => {
  it('registers the bundled icon data', () => {
    initIcons()
    // A representative icon from each collection the UI leans on.
    expect(iconLoaded('ant-design:setting-outlined')).toBe(true)
    expect(iconLoaded('lucide:circle')).toBe(true)
  })

  it('registers the custom element without a CDN script', () => {
    initIcons()
    expect(customElements.get('iconify-icon')).toBeTruthy()
  })

  it('replaces iconify\'s fetch so the API cannot be reached', async () => {
    initIcons()
    const installed = _api.getFetch()
    expect(installed).not.toBe(fetchBeforeInit)
    // An agent profile's `icon:` is user-authored YAML and can name anything.
    // The component's reflex for an unknown icon is to ask Iconify's API for
    // it; that door has to be shut or the privacy claim is false.
    await expect((installed as unknown as () => Promise<unknown>)()).rejects.toThrow(/not reachable by design/)
  })

  it('fails an unbundled icon instead of fetching it', async () => {
    initIcons()
    await expect(loadIcon('mdi:definitely-not-bundled')).rejects.toBeDefined()
  })

  // The opt-out patches a method of the component rather than a public API,
  // so pin that the method is really there: an upgrade that renames it would
  // otherwise make the patch a silent no-op and bring the lag back (#2721).
  it('patches the observer method the component actually has', () => {
    expect(typeof startObserverBeforeInit).toBe('function')
    initIcons()
    expect(iconPrototype().startObserver).not.toBe(startObserverBeforeInit)
  })

  it('gives icons no per-element IntersectionObserver', () => {
    const created = vi.fn()
    const original = globalThis.IntersectionObserver
    globalThis.IntersectionObserver = class {
      constructor() { created() }
      observe() {}
      disconnect() {}
    } as unknown as typeof IntersectionObserver
    try {
      initIcons()
      const el = document.createElement('iconify-icon')
      el.setAttribute('icon', 'lucide:circle')
      document.body.appendChild(el)
      el.remove()
      expect(created).not.toHaveBeenCalled()
    } finally {
      globalThis.IntersectionObserver = original
    }
  })
})

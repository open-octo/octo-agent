import { describe, it, expect } from 'vitest'
import { _api, iconLoaded, loadIcon } from 'iconify-icon'
import { initIcons } from './icons'

// Captured at import time, before any test calls initIcons. iconify grabs its
// own fetch reference when the module loads, so spying on globalThis.fetch
// cannot observe what it does — comparing that captured reference is the only
// way to see the network path actually being closed.
const fetchBeforeInit = _api.getFetch()

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
})

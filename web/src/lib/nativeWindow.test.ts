import { describe, it, expect, vi, beforeEach } from 'vitest'
import { get } from 'svelte/store'

const mocks = vi.hoisted(() => ({ toggle: vi.fn(), state: vi.fn() }))
vi.mock('./stores', async () => {
  const { writable } = await import('svelte/store')
  return { nativeShell: writable(false) }
})
vi.mock('./api', () => ({
  nativeToggleMaximise: mocks.toggle,
  nativeWindowState: mocks.state,
}))

import { nativeShell } from './stores'
import { isMaximised, flipMaximise, refreshMaximised, titlebarDblClick } from './nativeWindow'

// A double-click carries no coordinates worth modelling — only what it landed
// on, since the handler has to let controls inside a titlebar keep their clicks.
function dblclickOn(html: string, selector: string) {
  document.body.innerHTML = `<div class="bar">${html}</div>`
  document.querySelector('.bar')!.addEventListener('dblclick', titlebarDblClick as EventListener)
  document.querySelector(selector)!.dispatchEvent(new MouseEvent('dblclick', { bubbles: true }))
}

describe('nativeWindow', () => {
  beforeEach(() => {
    mocks.toggle.mockReset().mockResolvedValue(undefined)
    mocks.state.mockReset().mockResolvedValue(false)
    nativeShell.set(false)
    isMaximised.set(false)
  })

  it('ignores titlebar double-clicks outside the desktop shell', () => {
    dblclickOn('<span class="brand">Octo</span>', '.brand')
    expect(mocks.toggle).not.toHaveBeenCalled()
  })

  it('zooms on a double-click in the titlebar itself', async () => {
    nativeShell.set(true)
    dblclickOn('<span class="brand">Octo</span>', '.brand')
    await vi.waitFor(() => expect(mocks.toggle).toHaveBeenCalledTimes(1))
    expect(get(isMaximised)).toBe(true)
  })

  it('leaves double-clicks on a titlebar control alone', () => {
    nativeShell.set(true)
    dblclickOn('<button class="search">s</button>', '.search')
    expect(mocks.toggle).not.toHaveBeenCalled()
  })

  // The icon lives in one header while both headers can flip the state, so the
  // shared store — not a component-local copy — is what has to move.
  it('flips the shared state on every toggle', async () => {
    await flipMaximise()
    expect(get(isMaximised)).toBe(true)
    await flipMaximise()
    expect(get(isMaximised)).toBe(false)
  })

  it('resyncs from the OS when a toggle fails', async () => {
    mocks.toggle.mockRejectedValue(new Error('bridge down'))
    mocks.state.mockResolvedValue(true)
    await flipMaximise()
    expect(get(isMaximised)).toBe(true) // the OS value, not the optimistic guess
  })

  // A focus refresh that started before a toggle must not land after it and
  // report the pre-toggle state.
  it('drops a stale refresh that a toggle overtook', async () => {
    let releaseState: (v: boolean) => void = () => {}
    mocks.state.mockReturnValue(new Promise<boolean>(r => { releaseState = r }))
    const stale = refreshMaximised()
    await flipMaximise()
    expect(get(isMaximised)).toBe(true)
    releaseState(false)
    await stale
    expect(get(isMaximised)).toBe(true)
  })
})

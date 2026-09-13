import { describe, it, expect, vi, beforeEach } from 'vitest'
import { get } from 'svelte/store'

const mocks = vi.hoisted(() => ({ toggle: vi.fn(), state: vi.fn() }))
vi.mock('./stores', async () => {
  const { writable } = await import('svelte/store')
  return { nativeShell: writable(false), macosMajor: writable(0), isDesktopShell: true }
})
vi.mock('./api', () => ({
  nativeToggleMaximise: mocks.toggle,
  nativeWindowState: mocks.state,
}))

import { nativeShell, macosMajor } from './stores'
import { isMaximised, flipMaximise, refreshMaximised, titlebarDblClick, titlebarPaddingPx, applyTitlebarLift } from './nativeWindow'

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
    macosMajor.set(0)
    isMaximised.set(false)
    document.documentElement.style.removeProperty('--titlebar-pad-top')
    document.documentElement.style.removeProperty('--titlebar-pad-bottom')
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

  // The padding exists to put the rows' axis on the traffic lights' centre,
  // measured for this window style (hidden titlebar + wails' toolbar): 20px
  // from the window top through macOS 15, 26pt on macOS 26 (windows stamped
  // with the macOS 26 SDK). 4px of bottom padding lands the pinned 44px row at
  // (44 - 4) / 2 = 20; 8px of top padding lands it at 8 + (44 - 8) / 2 = 26.
  describe('titlebarPaddingPx', () => {
    it('pads 4px from below through macOS 15', () => {
      expect(titlebarPaddingPx(true, true, 15)).toEqual({ top: 0, bottom: 4 })
    })

    it('pads 8px from above on macOS 26', () => {
      expect(titlebarPaddingPx(true, true, 26)).toEqual({ top: 8, bottom: 0 })
      expect(titlebarPaddingPx(true, true, 27)).toEqual({ top: 8, bottom: 0 })
    })

    it('keeps the legacy padding when the host version is unknown', () => {
      expect(titlebarPaddingPx(true, true, 0)).toEqual({ top: 0, bottom: 4 })
    })

    it('pads nothing off the mac desktop shell', () => {
      expect(titlebarPaddingPx(false, true, 15)).toEqual({ top: 0, bottom: 0 })
      expect(titlebarPaddingPx(true, false, 15)).toEqual({ top: 0, bottom: 0 })
    })
  })

  describe('applyTitlebarLift', () => {
    function onMacPlatform(v: string) {
      Object.defineProperty(window.navigator, 'platform', { value: v, configurable: true })
    }

    it('publishes 8px top / 0px bottom on a macOS 26 desktop shell', () => {
      onMacPlatform('MacIntel')
      macosMajor.set(26)
      applyTitlebarLift()
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-top')).toBe('8px')
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-bottom')).toBe('0px')
    })

    it('publishes 0px top / 4px bottom on a pre-26 macOS desktop shell', () => {
      onMacPlatform('MacIntel')
      macosMajor.set(15)
      applyTitlebarLift()
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-top')).toBe('0px')
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-bottom')).toBe('4px')
    })

    it('publishes the legacy values when the host version is unknown', () => {
      onMacPlatform('MacIntel')
      macosMajor.set(0)
      applyTitlebarLift()
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-top')).toBe('0px')
      expect(document.documentElement.style.getPropertyValue('--titlebar-pad-bottom')).toBe('4px')
    })
  })
})

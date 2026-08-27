// Dismissal coverage for the version badge popover. Possible because
// vitest.config.ts sets resolve.conditions: ['browser'] — see
// src/lib/genui/components.test.ts.
//
// The popover used to rely on a fixed full-bleed scrim, which the sidebar's
// backdrop-filter silently confined to the sidebar itself, so a click anywhere
// else left it open. These tests pin the window-level dismissal that replaced
// it — including the two clicks that must NOT close it.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import VersionBadge from './VersionBadge.svelte'
import * as api from '../../lib/api'
import { ws } from '../../lib/ws'

let target: HTMLElement
let app: Record<string, unknown> | null = null
let wsHandlers: Record<string, (ev: unknown) => void>

function versionPayload(over: Record<string, unknown> = {}) {
  return {
    current: '1.16.7',
    latest: '1.16.7',
    needs_update: false,
    cli_command: 'octo',
    upgrade_mode: 'cli',
    local: true,
    native: false,
    self_update: false,
    ...over,
  }
}

async function render(over: Record<string, unknown> = {}) {
  vi.spyOn(api, 'getVersion').mockResolvedValue(versionPayload(over) as never)
  app = mount(VersionBadge, { target }) as Record<string, unknown>
  // Let onMount's checkVersion() settle so the badge actually renders.
  await new Promise((r) => setTimeout(r, 0))
  flushSync()
  return target.querySelector('.vb-badge') as HTMLElement
}

const pop = () => target.querySelector('.vb-pop')

beforeEach(() => {
  wsHandlers = {}
  vi.spyOn(ws, 'on').mockImplementation(((type: string, fn: (ev: unknown) => void) => {
    wsHandlers[type] = fn
    return () => {}
  }) as never)
  target = document.createElement('div')
  document.body.appendChild(target)
})

afterEach(() => {
  if (app) unmount(app)
  app = null
  target.remove()
  vi.restoreAllMocks()
})

describe('VersionBadge popover dismissal', () => {
  it('opens on the badge and closes on a click anywhere outside', async () => {
    const badge = await render()
    expect(badge).toBeTruthy()

    badge.click()
    flushSync()
    expect(pop()).toBeTruthy()

    // A click outside the sidebar entirely — the case the scrim never covered.
    const elsewhere = document.createElement('div')
    document.body.appendChild(elsewhere)
    elsewhere.click()
    flushSync()
    expect(pop()).toBe(null)
    elsewhere.remove()
  })

  it('stays open when the click lands inside the popover', async () => {
    const badge = await render()
    badge.click()
    flushSync()

    ;(pop() as HTMLElement).click()
    flushSync()
    expect(pop()).toBeTruthy()
  })

  it('clicking the badge again closes it rather than reopening', async () => {
    const badge = await render()
    badge.click()
    flushSync()
    expect(pop()).toBeTruthy()

    // The capture-phase dismisser must not fire for a click on the badge, or
    // toggle() would reopen what it just closed and the popover would stick.
    badge.click()
    flushSync()
    expect(pop()).toBe(null)
  })

  it('cannot be dismissed mid-upgrade', async () => {
    await render()
    // An upgrade broadcast opens the popover and locks it: dismissing here
    // would leave the install running with no surface.
    wsHandlers['upgrade_log']?.({ line: 'downloading...' })
    flushSync()
    expect(pop()).toBeTruthy()

    document.body.click()
    flushSync()
    expect(pop()).toBeTruthy()
  })

  it('is dismissable again once the upgrade needs a restart', async () => {
    await render()
    wsHandlers['upgrade_log']?.({ line: 'downloading...' })
    flushSync()
    wsHandlers['upgrade_complete']?.({ success: true })
    flushSync()
    expect(pop()).toBeTruthy()

    document.body.click()
    flushSync()
    expect(pop()).toBe(null)
  })
})

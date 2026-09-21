import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The script the server appends to every Light App it serves
// (internal/server/lightapp_origin.go), run as the bytes that ship.
// vitest's cwd is web/ (vitest.config.ts).
const BRIDGE_JS = readFileSync(join(process.cwd(), '..', 'internal', 'server', 'lightapp_bridge.js'), 'utf8')

type Sent = Record<string, unknown>

function boot(cfg: Record<string, unknown>): { win: Record<string, any>; sent: Sent[] } {
  const sent: Sent[] = []
  const win: Record<string, any> = {
    parent: { postMessage: (d: Sent) => sent.push(d) },
    __octoLightApp: cfg,
    addEventListener() {},
  }
  new Function('window', 'document', BRIDGE_JS)(win, { addEventListener() {} })
  return { win, sent }
}

// `pushState` / `onDelivery` were a real channel once. Removing them outright
// would break the apps written while they were: those call
// `window.octo.pushState(...)` unguarded, from pointerup and change handlers,
// where a TypeError takes the rest of the handler with it.
describe('the retired octo.pushState / octo.onDelivery', () => {
  it('are still callable', () => {
    const { win } = boot({ ns: 'sketch' })

    expect(typeof win.octo.pushState).toBe('function')
    expect(typeof win.octo.onDelivery).toBe('function')
    expect(() => win.octo.pushState({ digest: 'three strokes', image: null })).not.toThrow()
    expect(() => win.octo.onDelivery(() => {})).not.toThrow()
  })

  it('send nothing to the host', () => {
    const { win, sent } = boot({ ns: 'sketch' })

    win.octo.pushState({ digest: 'three strokes', summary: { strokes: 3 } })

    // migrate-ready is all a Light App says at boot; no snapshot follows it.
    expect(sent.map((m) => m.op)).toEqual(['migrate-ready'])
  })
})

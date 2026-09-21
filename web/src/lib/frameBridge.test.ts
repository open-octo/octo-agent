import { describe, it, expect } from 'vitest'
import { readFileSync } from 'node:fs'
import { join } from 'node:path'

// The script the server appends to every framed page it serves — Light App or
// session artifact (internal/server/lightapp_origin.go, artifact_origin.go).
// The tests run the very bytes that ship. vitest's cwd is web/.
const BRIDGE_JS = readFileSync(join(process.cwd(), '..', 'internal', 'server', 'frame_bridge.js'), 'utf8')

type Sent = Record<string, unknown>

// Boot the bridge the way a served page does: framed, with the config the
// origin injected just above it.
function boot(cfg: Record<string, unknown>): { win: Record<string, any>; sent: Sent[] } {
  const sent: Sent[] = []
  const win: Record<string, any> = {
    parent: { postMessage: (d: Sent) => sent.push(d) },
    __octoBridge: cfg,
    addEventListener() {},
  }
  new Function('window', 'document', BRIDGE_JS)(win, { addEventListener() {} })
  return { win, sent }
}

describe('frame bridge, artifact kind', () => {
  it('publishes a snapshot under the identity the host injected', () => {
    const { win, sent } = boot({ kind: 'artifact', ns: 'sess-1\n/tmp/page.html' })

    win.octo.pushState({ digest: 'a chart', summary: { bars: 8 } })

    expect(sent.length).toBe(1)
    expect(sent[0].op).toBe('state')
    expect(sent[0].ns).toBe('sess-1\n/tmp/page.html')
    expect(sent[0].digest).toBe('a chart')
    expect(sent[0].summary).toEqual({ bars: 8 })
  })

  it('does not boot the light-app storage migration', () => {
    const { sent } = boot({ kind: 'artifact', ns: 'sess-1\n/tmp/page.html' })

    // A Light App announces itself with migrate-ready at boot; an artifact has
    // no storage contract to migrate and must stay silent until it publishes.
    expect(sent.length).toBe(0)
  })
})

describe('frame bridge, lightapp kind', () => {
  // The mirror was a Light App feature before it moved to artifacts, and the
  // apps written back then call `window.octo.pushState(...)` unguarded, from
  // pointerup and change handlers. Retiring the mirror must not throw inside
  // those handlers and take the rest of the app's logic with it.
  it('leaves window.octo present and inert', () => {
    const { win, sent } = boot({ kind: 'lightapp', ns: 'sketch' })

    expect(typeof win.octo.pushState).toBe('function')
    expect(typeof win.octo.onDelivery).toBe('function')

    win.octo.pushState({ digest: 'three strokes', image: null })
    win.octo.onDelivery(() => {})

    // migrate-ready is the only thing a Light App says at boot; no state ever
    // leaves it, so nothing can reach the mirror in its name.
    expect(sent.filter((m) => m.op === 'state')).toEqual([])
    expect(sent.map((m) => m.op)).toEqual(['migrate-ready'])
  })
})

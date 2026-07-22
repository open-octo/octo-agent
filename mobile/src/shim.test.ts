import { describe, it, expect, afterEach } from 'vitest'
import { installShim, type Shim } from './shim'
import { decodeFrame, encodeFrame, type ShimFrame, type HttpRequestFrame } from './frames'
import type { Transport } from './transport'

class MockTransport implements Transport {
  sent: string[] = []
  private handler: ((f: string) => void) | null = null
  async connect(): Promise<void> {}
  async send(f: string): Promise<void> {
    this.sent.push(f)
  }
  onMessage(h: (f: string) => void): void {
    this.handler = h
  }
  async disconnect(): Promise<void> {}
  push(f: ShimFrame): void {
    this.handler?.(encodeFrame(f))
  }
  last(): ShimFrame {
    return decodeFrame(this.sent[this.sent.length - 1])
  }
}

const tick = () => new Promise<void>((r) => setTimeout(r, 0))

describe('installShim', () => {
  const origFetch = globalThis.fetch
  const origWS = globalThis.WebSocket
  let shim: Shim | null = null

  afterEach(() => {
    shim?.uninstall()
    shim = null
    globalThis.fetch = origFetch
    globalThis.WebSocket = origWS
  })

  it('tunnels an /api fetch and resolves with the host response', async () => {
    const t = new MockTransport()
    shim = installShim(t)
    const respP = fetch('/api/version?x=1')
    await tick()
    const req = t.last() as HttpRequestFrame
    expect(req.kind).toBe('http-req')
    expect(req.method).toBe('GET')
    expect(req.path).toBe('/api/version?x=1')
    t.push({ kind: 'http-resp', id: req.id, status: 200, headers: { 'content-type': 'application/json' }, body: '{"ok":true}' })
    const resp = await respP
    expect(resp.status).toBe(200)
    expect(await resp.json()).toEqual({ ok: true })
  })

  it('carries a POST body', async () => {
    const t = new MockTransport()
    shim = installShim(t)
    void fetch('/api/chat', { method: 'POST', body: '{"prompt":"hi"}' })
    await tick()
    const req = t.last() as HttpRequestFrame
    expect(req.method).toBe('POST')
    expect(req.body).toBe('{"prompt":"hi"}')
  })

  it('passes a non-/api fetch through to the original', async () => {
    let seen = ''
    globalThis.fetch = (async (input: unknown) => {
      seen = String(input)
      return new Response('external')
    }) as typeof fetch
    const t = new MockTransport()
    shim = installShim(t)
    const r = await fetch('https://example.com/data')
    expect(seen).toBe('https://example.com/data')
    expect(await r.text()).toBe('external')
    expect(t.sent).toHaveLength(0)
  })

  it('tunnels a /ws socket in both directions', async () => {
    const t = new MockTransport()
    shim = installShim(t)
    const ws = new (globalThis.WebSocket as unknown as new (u: string) => any)('ws://localhost/ws')
    const open = t.last() as { kind: string; id: string; path: string }
    expect(open.kind).toBe('ws-open')
    expect(open.path).toBe('/ws')
    const id = open.id

    let opened = false
    ws.onopen = () => (opened = true)
    const got: string[] = []
    ws.onmessage = (e: { data: string }) => got.push(e.data)
    let closed = false
    ws.onclose = () => (closed = true)

    await tick()
    expect(opened).toBe(true)
    expect(ws.readyState).toBe(1)

    ws.send('hello')
    expect(t.last()).toMatchObject({ kind: 'ws-msg', id, data: 'hello' })

    t.push({ kind: 'ws-msg', id, data: 'world' })
    expect(got).toEqual(['world'])

    t.push({ kind: 'ws-close', id, code: 1000 })
    expect(closed).toBe(true)
    expect(ws.readyState).toBe(3)
  })

  it('exposes the readyState constants on the patched constructor', () => {
    const t = new MockTransport()
    shim = installShim(t)
    expect((globalThis.WebSocket as unknown as { OPEN: number }).OPEN).toBe(1)
    expect((globalThis.WebSocket as unknown as { CONNECTING: number }).CONNECTING).toBe(0)
  })

  it('restores the originals on uninstall', () => {
    const t = new MockTransport()
    shim = installShim(t)
    expect(globalThis.fetch).not.toBe(origFetch)
    shim.uninstall()
    shim = null
    expect(globalThis.fetch).toBe(origFetch)
    expect(globalThis.WebSocket).toBe(origWS)
  })
})

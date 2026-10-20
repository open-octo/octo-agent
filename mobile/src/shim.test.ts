import { describe, it, expect, afterEach, vi } from 'vitest'
import { installShim, type Shim } from './shim'

const conn = { base: 'http://100.64.0.5:8088', key: 'k+1/2' }
const PAGE = 'capacitor://localhost'

class FakeWebSocket {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSING = 2
  static readonly CLOSED = 3
  static last: FakeWebSocket | null = null
  constructor(
    readonly url: string,
    readonly protocols?: string | string[],
  ) {
    FakeWebSocket.last = this
  }
}

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

  function withFakes() {
    const fetchMock = vi.fn(async () => new Response('{}'))
    globalThis.fetch = fetchMock as unknown as typeof fetch
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket
    shim = installShim(conn, PAGE)
    return fetchMock
  }

  it('rewrites a relative /api fetch to the server and attaches the key', async () => {
    const fetchMock = withFakes()
    await fetch('/api/sessions?limit=1')
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.5:8088/api/sessions?limit=1')
    expect(new Headers(init.headers).get('X-Access-Key')).toBe('k+1/2')
  })

  it('keeps method, body and the caller headers', async () => {
    const fetchMock = withFakes()
    await fetch('/api/chat', { method: 'POST', body: '{"prompt":"hi"}', headers: { 'Content-Type': 'application/json' } })
    const [, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(init.method).toBe('POST')
    expect(init.body).toBe('{"prompt":"hi"}')
    const h = new Headers(init.headers)
    expect(h.get('Content-Type')).toBe('application/json')
    expect(h.get('X-Access-Key')).toBe('k+1/2')
  })

  it('rewrites a Request object, carrying its body', async () => {
    const fetchMock = withFakes()
    await fetch(new Request('http://localhost/api/chat', { method: 'POST', body: 'x' }))
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit]
    expect(url).toBe('http://100.64.0.5:8088/api/chat')
    expect(init.method).toBe('POST')
    expect(new TextDecoder().decode(init.body as ArrayBuffer)).toBe('x')
  })

  it('passes non-/api and foreign-host fetches through untouched', async () => {
    const fetchMock = withFakes()
    await fetch('/assets/index.js')
    await fetch('https://cdn.example.com/api/x')
    expect(fetchMock.mock.calls.map((c) => (c as unknown[])[0])).toEqual([
      '/assets/index.js',
      'https://cdn.example.com/api/x',
    ])
    expect((fetchMock.mock.calls[1] as unknown[])[1]).toBeUndefined()
  })

  it('rewrites the frontend /ws URL to the server with the key in the query', () => {
    withFakes()
    new WebSocket('ws://localhost/ws')
    expect(FakeWebSocket.last?.url).toBe('ws://100.64.0.5:8088/ws?access_key=k%2B1%2F2')
  })

  it('uses wss for an https server', () => {
    globalThis.WebSocket = FakeWebSocket as unknown as typeof WebSocket
    shim = installShim({ base: 'https://box.example.ts.net', key: 'k' }, PAGE)
    new WebSocket('wss://localhost/ws')
    expect(FakeWebSocket.last?.url).toBe('wss://box.example.ts.net/ws?access_key=k')
  })

  it('passes other WebSockets through and keeps the static constants', () => {
    withFakes()
    new WebSocket('wss://other.example.com/socket')
    expect(FakeWebSocket.last?.url).toBe('wss://other.example.com/socket')
    expect(WebSocket.OPEN).toBe(1)
    expect(new WebSocket('ws://localhost/ws')).toBeInstanceOf(WebSocket)
  })

  it('uninstall restores the originals', () => {
    const fetchMock = withFakes()
    shim!.uninstall()
    shim = null
    expect(globalThis.fetch).toBe(fetchMock)
    expect(globalThis.WebSocket).toBe(FakeWebSocket)
  })
})

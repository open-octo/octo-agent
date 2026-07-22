import type { Transport } from './transport'
import { decodeFrame, encodeFrame, type ShimFrame, type HttpRequestFrame } from './frames'

// The local shim makes the bundled frontend's same-origin traffic reach the
// remote server transparently. It monkeypatches globalThis.fetch and
// globalThis.WebSocket so a `fetch('/api/…')` or `new WebSocket('…/ws')` is
// terminated locally, framed (frames.ts), and carried over the active Transport;
// anything else falls through to the real implementation. The frontend never
// learns it is remote — the design's "the frontend stays same-origin" move.
//
// Only plaintext frames cross into the transport; encryption and keys live below
// it (native, for the managed relay), so nothing secret enters this layer.

export interface Shim {
  /** Restore the original fetch / WebSocket. */
  uninstall(): void
}

const CONNECTING = 0
const OPEN = 1
const CLOSING = 2
const CLOSED = 3

// TunnelWebSocket presents the subset of the WebSocket API the frontend uses
// (web/src/lib/ws.ts): the readyState constants, on{open,message,close,error},
// send, and close. Its traffic is carried as ws-* frames.
class TunnelWebSocket {
  static readonly CONNECTING = CONNECTING
  static readonly OPEN = OPEN
  static readonly CLOSING = CLOSING
  static readonly CLOSED = CLOSED
  readonly CONNECTING = CONNECTING
  readonly OPEN = OPEN
  readonly CLOSING = CLOSING
  readonly CLOSED = CLOSED

  readyState: number = CONNECTING
  onopen: ((ev: unknown) => void) | null = null
  onmessage: ((ev: { data: string }) => void) | null = null
  onclose: ((ev: { code?: number; reason?: string }) => void) | null = null
  onerror: ((ev: unknown) => void) | null = null

  constructor(
    readonly url: string,
    private readonly id: string,
    path: string,
    private readonly emit: (frame: ShimFrame) => void,
    private readonly dispose: () => void,
  ) {
    this.emit({ kind: 'ws-open', id, path })
    // Report open on a microtask so the caller can attach onopen first. The
    // frontend queues its first send in onopen; a host-side failure arrives as
    // a ws-error frame and surfaces through onerror/onclose.
    queueMicrotask(() => {
      if (this.readyState !== CONNECTING) return
      this.readyState = OPEN
      this.onopen?.({})
    })
  }

  send(data: string): void {
    if (this.readyState !== OPEN) return
    this.emit({ kind: 'ws-msg', id: this.id, data: typeof data === 'string' ? data : String(data) })
  }

  close(code?: number, reason?: string): void {
    if (this.readyState === CLOSED || this.readyState === CLOSING) return
    this.readyState = CLOSING
    this.emit({ kind: 'ws-close', id: this.id, code, reason })
    this.deliverClose(code, reason)
  }

  // ── host-driven events (called by the shim's frame router) ──
  deliverMessage(data: string): void {
    this.onmessage?.({ data })
  }
  deliverClose(code?: number, reason?: string): void {
    if (this.readyState === CLOSED) return
    this.readyState = CLOSED
    this.onclose?.({ code, reason })
    this.dispose()
  }
  deliverError(message: string): void {
    this.onerror?.({ message })
    this.deliverClose(1006, message)
  }
}

export function installShim(
  transport: Transport,
  origin: string = (globalThis as { location?: { origin?: string } }).location?.origin ?? 'capacitor://localhost',
): Shim {
  const pendingHttp = new Map<string, { resolve: (r: Response) => void; reject: (e: unknown) => void }>()
  const sockets = new Map<string, TunnelWebSocket>()
  let seq = 0
  const nextId = () => String(++seq)

  const url = (input: unknown): string => {
    if (typeof input === 'string') return input
    if (input instanceof URL) return input.href
    if (input && typeof input === 'object' && 'url' in input) return String((input as { url: unknown }).url)
    return String(input)
  }
  const resolved = (u: string): URL => new URL(u, origin)
  const isApi = (u: string): boolean => resolved(u).pathname.startsWith('/api')
  const isWs = (u: string): boolean => resolved(u).pathname === '/ws'

  transport.onMessage((raw) => {
    let f: ShimFrame
    try {
      f = decodeFrame(raw)
    } catch {
      return
    }
    switch (f.kind) {
      case 'http-resp': {
        const pending = pendingHttp.get(f.id)
        if (!pending) return
        pendingHttp.delete(f.id)
        pending.resolve(new Response(f.body, { status: f.status, headers: f.headers }))
        return
      }
      case 'ws-msg':
        sockets.get(f.id)?.deliverMessage(f.data)
        return
      case 'ws-close':
        sockets.get(f.id)?.deliverClose(f.code, f.reason)
        return
      case 'ws-error':
        sockets.get(f.id)?.deliverError(f.message)
        return
      default:
        return
    }
  })

  const origFetch = globalThis.fetch
  const OrigWebSocket = globalThis.WebSocket

  const toRequestFrame = async (id: string, input: unknown, init?: RequestInit): Promise<HttpRequestFrame> => {
    const raw = url(input)
    let method = 'GET'
    const headers: Record<string, string> = {}
    let body: string | null = null

    if (input && typeof input === 'object' && 'url' in input) {
      const req = input as Request
      method = req.method
      req.headers.forEach((v, k) => (headers[k] = v))
      const text = await req.clone().text()
      body = text || null
    }
    if (init) {
      if (init.method) method = init.method
      if (init.headers) new Headers(init.headers).forEach((v, k) => (headers[k] = v))
      if (init.body != null) body = typeof init.body === 'string' ? init.body : String(init.body)
    }
    method = method.toUpperCase()
    const u = resolved(raw)
    return {
      kind: 'http-req',
      id,
      method,
      path: u.pathname + u.search,
      headers,
      body: method === 'GET' || method === 'HEAD' ? null : body,
    }
  }

  const patchedFetch = (async (input: unknown, init?: RequestInit): Promise<Response> => {
    if (!isApi(url(input))) return origFetch(input as RequestInfo, init)
    const id = nextId()
    const frame = await toRequestFrame(id, input, init)
    const done = new Promise<Response>((resolve, reject) => pendingHttp.set(id, { resolve, reject }))
    try {
      await transport.send(encodeFrame(frame))
    } catch (e) {
      pendingHttp.delete(id)
      throw e
    }
    return done
  }) as typeof fetch

  // A constructor-compatible stand-in for WebSocket. /ws is tunneled; any other
  // target falls through to the real WebSocket.
  const patchedWebSocket = function (this: unknown, target: string | URL, protocols?: string | string[]) {
    const u = url(target)
    if (!isWs(u)) return new OrigWebSocket(target as string | URL, protocols)
    const id = nextId()
    const ws = new TunnelWebSocket(
      u,
      id,
      resolved(u).pathname,
      (frame) => {
        void transport.send(encodeFrame(frame))
      },
      () => sockets.delete(id),
    )
    sockets.set(id, ws)
    return ws
  } as unknown as typeof WebSocket
  // Preserve the static readyState constants the frontend reads.
  Object.assign(patchedWebSocket, {
    CONNECTING,
    OPEN,
    CLOSING,
    CLOSED,
  })

  globalThis.fetch = patchedFetch
  globalThis.WebSocket = patchedWebSocket

  return {
    uninstall() {
      globalThis.fetch = origFetch
      globalThis.WebSocket = OrigWebSocket
    },
  }
}

import type { Connection } from './connection'

// The local shim points the bundled frontend at the remote server. The
// frontend calls same-origin `/api/…` and `ws://<host>/ws`, as it does when
// octo serve hosts it; in the app its origin is the WebView's own
// (capacitor://localhost on iOS, https://localhost on Android), so the shim
// monkeypatches globalThis.fetch and globalThis.WebSocket to rewrite those
// calls to the connection's base URL and attach the access key. The server
// allows both WebView origins CORS (internal/server/mobile_connect.go).
// Anything else falls through untouched.

export interface Shim {
  /** Restore the original fetch / WebSocket. */
  uninstall(): void
}

export function installShim(
  conn: Connection,
  pageOrigin: string = (globalThis as { location?: { origin?: string } }).location?.origin ?? 'capacitor://localhost',
): Shim {
  const origFetch = globalThis.fetch
  const OrigWebSocket = globalThis.WebSocket
  const page = new URL(pageOrigin)
  const wsBase = conn.base.replace(/^http/i, 'ws')

  const href = (input: unknown): string => {
    if (typeof input === 'string') return input
    if (input instanceof URL) return input.href
    if (input && typeof input === 'object' && 'url' in input) return String((input as { url: unknown }).url)
    return String(input)
  }
  // Resolved against the page, a same-origin call keeps the page's host;
  // compare hosts rather than origins because the frontend builds its /ws URL
  // as ws(s)://<host>/ws.
  const local = (raw: string): URL | null => {
    let u: URL
    try {
      u = new URL(raw, pageOrigin)
    } catch {
      return null
    }
    return u.host === page.host ? u : null
  }

  const patchedFetch = (async (input: unknown, init?: RequestInit): Promise<Response> => {
    const u = local(href(input))
    if (!u || !u.pathname.startsWith('/api')) return origFetch(input as RequestInfo, init)

    let base: RequestInit = {}
    if (typeof Request !== 'undefined' && input instanceof Request) {
      const body = input.method === 'GET' || input.method === 'HEAD' ? undefined : await input.clone().arrayBuffer()
      base = { method: input.method, headers: input.headers, body, signal: input.signal }
    }
    const merged: RequestInit = { ...base, ...init }
    const headers = new Headers(merged.headers)
    headers.set('X-Access-Key', conn.key)
    return origFetch(conn.base + u.pathname + u.search, { ...merged, headers })
  }) as typeof fetch

  // The browser WebSocket API cannot set headers, so the key rides the query —
  // the one route where the server accepts it there.
  const patchedWebSocket = function (this: unknown, target: string | URL, protocols?: string | string[]) {
    const u = local(href(target))
    if (!u || u.pathname !== '/ws') return new OrigWebSocket(target, protocols)
    const dest = new URL(wsBase + u.pathname + u.search)
    dest.searchParams.set('access_key', conn.key)
    return new OrigWebSocket(dest.toString(), protocols)
  } as unknown as typeof WebSocket
  // Keep the static readyState constants and instanceof working.
  patchedWebSocket.prototype = OrigWebSocket.prototype
  Object.assign(patchedWebSocket, {
    CONNECTING: OrigWebSocket.CONNECTING,
    OPEN: OrigWebSocket.OPEN,
    CLOSING: OrigWebSocket.CLOSING,
    CLOSED: OrigWebSocket.CLOSED,
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

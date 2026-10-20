// A Connection is everything the app needs to reach one octo serve: its base
// URL and access key. Both come from the bootstrap link the server prints on
// startup and renders as a QR in Settings › Mobile —
// `http(s)://<host>[:port]/?access_key=<key>`.
export interface Connection {
  /** Server origin, e.g. http://100.64.0.5:8088 (no trailing slash). */
  base: string
  /** The server's access key. */
  key: string
}

/**
 * parseConnectLink parses a bootstrap link into a Connection. A bare address
 * without a scheme is taken as http://. Throws when the link is not http(s) or
 * carries no access_key.
 */
export function parseConnectLink(text: string): Connection {
  const raw = text.trim()
  const withScheme = /^[a-z][a-z0-9+.-]*:\/\//i.test(raw) ? raw : `http://${raw}`
  let u: URL
  try {
    u = new URL(withScheme)
  } catch {
    throw new Error('not a valid address')
  }
  if (u.protocol !== 'http:' && u.protocol !== 'https:') {
    throw new Error(`unsupported scheme ${u.protocol}`)
  }
  const key = u.searchParams.get('access_key')
  if (!key) throw new Error('missing access_key')
  return { base: u.origin, key }
}

const STORAGE_KEY = 'octo:connection'

export function loadConnection(): Connection | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return null
    const c = JSON.parse(raw) as Connection
    return c.base && c.key ? c : null
  } catch {
    return null
  }
}

export function saveConnection(c: Connection): void {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(c))
}

export function clearConnection(): void {
  try {
    localStorage.removeItem(STORAGE_KEY)
  } catch {
    // ignore
  }
}

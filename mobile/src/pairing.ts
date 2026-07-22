// Pairing material a phone obtains by scanning the host's QR. The host encodes
// it as an octo-pair://v1 URL (see cmd/octo/serve_tunnel.go's pairingURL): the
// four things the phone needs to reach and authenticate the host over the relay.
export interface PairingInfo {
  /** Relay endpoint, e.g. wss://relay.octo.dev. */
  relay: string
  /** The host's tunnel identity on the relay. */
  tunnelId: string
  /** The host's Noise static public key, base64 — used to authenticate the host. */
  hostKey: string
  /** One-time pairing token. */
  token: string
}

const SCHEME = 'octo-pair:'
const VERSION = 'v1'

/**
 * parsePairingURL parses an `octo-pair://v1?relay=&tid=&hk=&tok=` URL into typed
 * PairingInfo. It throws on a wrong scheme, an unsupported version, or a missing
 * field. The values were percent-encoded by the host (Go's url.Values.Encode),
 * and URL's searchParams decodes them back.
 */
export function parsePairingURL(raw: string): PairingInfo {
  let url: URL
  try {
    url = new URL(raw)
  } catch {
    throw new Error('pairing: not a valid URL')
  }
  if (url.protocol !== SCHEME) {
    throw new Error(`pairing: expected ${SCHEME} scheme, got ${url.protocol}`)
  }
  // The version rides in the URL authority slot: octo-pair://v1?...
  if (url.host !== VERSION) {
    throw new Error(`pairing: unsupported version ${url.host || '(none)'}`)
  }
  const info: PairingInfo = {
    relay: url.searchParams.get('relay') ?? '',
    tunnelId: url.searchParams.get('tid') ?? '',
    hostKey: url.searchParams.get('hk') ?? '',
    token: url.searchParams.get('tok') ?? '',
  }
  const required: Array<[string, string]> = [
    ['relay', info.relay],
    ['tid', info.tunnelId],
    ['hk', info.hostKey],
    ['tok', info.token],
  ]
  for (const [name, value] of required) {
    if (!value) throw new Error(`pairing: missing ${name}`)
  }
  return info
}

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

const SCHEME = 'octo-pair'
const VERSION = 'v1'

/**
 * parsePairingURL parses an `octo-pair://v1?relay=&tid=&hk=&tok=` URL into typed
 * PairingInfo. It throws on a wrong scheme, an unsupported version, or a missing
 * field. The values were percent-encoded by the host (Go's url.Values.Encode),
 * and URLSearchParams decodes them back.
 *
 * It parses the string directly rather than via the WHATWG `URL` constructor:
 * `octo-pair` is a non-special scheme, and engines disagree on whether the
 * authority (which carries our version, e.g. //v1) is populated — Node fills
 * `url.host`, but the Android System WebView leaves it empty, which silently
 * broke version detection on-device. Manual parsing behaves identically
 * everywhere, and it also tolerates the `//` being dropped when a custom-scheme
 * deep link is delivered as an opaque URI.
 */
export function parsePairingURL(raw: string): PairingInfo {
  const trimmed = raw.trim()

  const schemeMatch = /^([a-z][a-z0-9+.-]*):/i.exec(trimmed)
  if (!schemeMatch || schemeMatch[1].toLowerCase() !== SCHEME) {
    const got = schemeMatch ? `${schemeMatch[1]}:` : '(none)'
    throw new Error(`pairing: expected ${SCHEME}: scheme, got ${got}`)
  }

  // Everything after `octo-pair:`, with an optional `//` authority marker
  // stripped. The version rides in the authority slot: octo-pair://v1?...
  const afterScheme = trimmed.slice(schemeMatch[0].length).replace(/^\/\//, '')
  const queryStart = afterScheme.search(/[?#]/)
  const versionPart = (queryStart === -1 ? afterScheme : afterScheme.slice(0, queryStart)).replace(/\/+$/, '')
  const queryStr = queryStart === -1 ? '' : afterScheme.slice(queryStart + 1).replace(/#.*$/, '')

  if (versionPart !== VERSION) {
    throw new Error(`pairing: unsupported version ${versionPart || '(none)'}`)
  }

  const params = new URLSearchParams(queryStr)
  const info: PairingInfo = {
    relay: params.get('relay') ?? '',
    tunnelId: params.get('tid') ?? '',
    hostKey: params.get('hk') ?? '',
    token: params.get('tok') ?? '',
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

// Tunnel frames are the plaintext units multiplexed over the Noise-encrypted
// data channel between the phone's local shim and the host tunnel. One channel
// carries many concurrent /api requests and /ws sockets, each tagged with a
// stream id, so the frontend's same-origin traffic reaches the remote server
// transparently.
//
// This is the phone↔host contract. The host side that speaks it (loopback HTTP
// for http-* frames, a loopback /ws per ws-* stream) is a follow-up: the current
// internal/tunnel bridges a raw /ws only. Defining it here, tested, fixes the
// wire format both ends must agree on.

export type ShimFrame =
  | HttpRequestFrame
  | HttpResponseFrame
  | WsOpenFrame
  | WsMessageFrame
  | WsCloseFrame
  | WsErrorFrame
  | PushTokenFrame

/** A fetch() the shim intercepted, to be replayed as a loopback HTTP call. */
export interface HttpRequestFrame {
  kind: 'http-req'
  id: string
  method: string
  path: string
  headers: Record<string, string>
  body: string | null
}

/** The host's HTTP response for the http-req with the same id. */
export interface HttpResponseFrame {
  kind: 'http-resp'
  id: string
  status: number
  headers: Record<string, string>
  body: string | null
}

/** The frontend opened a WebSocket; the host dials the loopback /ws for it. */
export interface WsOpenFrame {
  kind: 'ws-open'
  id: string
  path: string
}

/** One WebSocket text message, either direction, for the ws stream `id`. */
export interface WsMessageFrame {
  kind: 'ws-msg'
  id: string
  data: string
}

/** The WebSocket closed, either direction. */
export interface WsCloseFrame {
  kind: 'ws-close'
  id: string
  code?: number
  reason?: string
}

/** The host could not open or sustain the ws stream `id`. */
export interface WsErrorFrame {
  kind: 'ws-error'
  id: string
  message: string
}

/**
 * Registers the phone's current push token with the host (phone→host only;
 * the host's bridge consumes it, never forwarding it to loopback). `data` is
 * JSON `{"token":"...","platform":"apns"|"fcm"}` — mirror of the host's
 * pushTokenData in internal/tunnel/frames.go. Send it after every connect so
 * the host always holds a fresh token; an empty token unregisters.
 */
export interface PushTokenFrame {
  kind: 'push-token'
  id: string
  data: string
}

const KINDS = new Set(['http-req', 'http-resp', 'ws-open', 'ws-msg', 'ws-close', 'ws-error', 'push-token'])

/** encodeFrame serializes a frame to the JSON text the transport carries. */
export function encodeFrame(frame: ShimFrame): string {
  return JSON.stringify(frame)
}

/** decodeFrame parses a frame, throwing on malformed input or an unknown kind. */
export function decodeFrame(raw: string): ShimFrame {
  let obj: unknown
  try {
    obj = JSON.parse(raw)
  } catch {
    throw new Error('frames: invalid JSON')
  }
  if (typeof obj !== 'object' || obj === null) {
    throw new Error('frames: not an object')
  }
  const rec = obj as Record<string, unknown>
  if (typeof rec.kind !== 'string' || !KINDS.has(rec.kind)) {
    throw new Error(`frames: unknown kind ${String(rec.kind)}`)
  }
  if (typeof rec.id !== 'string' || rec.id === '') {
    throw new Error('frames: missing id')
  }
  return obj as ShimFrame
}

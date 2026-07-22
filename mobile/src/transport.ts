import type { PairingInfo } from './pairing'

// The mobile client speaks the existing octo /ws + /api protocol through a
// single Transport with two implementations (design: "one protocol, swappable
// transport"). The local shim terminates the webview's same-origin /api + /ws
// and feeds their bytes through the active Transport; the UI and protocol layers
// never learn which one is in use.
//
// This file is the contract. Both implementations are stubbed until the native
// tunnel layer lands — the Noise session and device keys live in native code
// (see native-bridge.ts) that needs the iOS/Android toolchains to build.
export interface Transport {
  /** Establish the connection (relay handshake, or a direct dial). */
  connect(): Promise<void>
  /** Send one framed /api or /ws message to the backend. */
  send(frame: Uint8Array): Promise<void>
  /** Register for frames arriving from the backend. */
  onMessage(handler: (frame: Uint8Array) => void): void
  /** Tear the connection down. */
  disconnect(): Promise<void>
}

function notImplemented(what: string): never {
  throw new Error(`octo-mobile: ${what} not implemented yet (native tunnel pending)`)
}

/**
 * SelfTunnelTransport dials the user's own tunnel URL directly and presents the
 * access key — no relay, no Noise, because the user's own tunnel (Tailscale,
 * Cloudflare, frp, …) already provides the encrypted transport. This is the free
 * bring-your-own-tunnel path.
 */
export class SelfTunnelTransport implements Transport {
  constructor(
    readonly url: string,
    readonly accessKey: string,
  ) {}
  connect(): Promise<void> {
    return notImplemented('self-tunnel transport')
  }
  send(_frame: Uint8Array): Promise<void> {
    return notImplemented('self-tunnel transport')
  }
  onMessage(_handler: (frame: Uint8Array) => void): void {
    notImplemented('self-tunnel transport')
  }
  disconnect(): Promise<void> {
    return notImplemented('self-tunnel transport')
  }
}

/**
 * ManagedRelayTransport runs the Noise session (in the native layer) and speaks
 * to octo's relay, framing the same /ws JSON inside encrypted frames. It is
 * constructed from the PairingInfo a QR scan yields.
 */
export class ManagedRelayTransport implements Transport {
  constructor(readonly pairing: PairingInfo) {}
  connect(): Promise<void> {
    return notImplemented('managed-relay transport')
  }
  send(_frame: Uint8Array): Promise<void> {
    return notImplemented('managed-relay transport')
  }
  onMessage(_handler: (frame: Uint8Array) => void): void {
    notImplemented('managed-relay transport')
  }
  disconnect(): Promise<void> {
    return notImplemented('managed-relay transport')
  }
}

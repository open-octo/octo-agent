import type { PairingInfo } from './pairing'
import type { OctoTunnelPlugin } from './native-bridge'

// Captured at import time, before the local shim patches globalThis.WebSocket —
// the self-tunnel transport must dial with the real WebSocket, not the shim's
// interceptor (which would tunnel the tunnel).
const NativeWebSocket = globalThis.WebSocket

// The mobile client speaks the existing octo /ws + /api protocol through a
// single Transport with two implementations (design: "one protocol, swappable
// transport"). The local shim (shim.ts) terminates the webview's same-origin
// /api + /ws and feeds their bytes here as tunnel frames; the UI and protocol
// layers never learn which transport is in use.
export interface Transport {
  /** Establish the connection (relay pairing + handshake, or a direct dial). */
  connect(): Promise<void>
  /** Send one tunnel frame (see frames.ts) to the backend. */
  send(frame: string): Promise<void>
  /** Register for frames arriving from the backend. */
  onMessage(handler: (frame: string) => void): void
  /** Tear the connection down. */
  disconnect(): Promise<void>
}

/**
 * ManagedRelayTransport delegates the Noise session and the relay WebSocket to
 * the native OctoTunnel plugin. JavaScript only ever handles plaintext frames;
 * the native layer holds the keys, encrypts, and moves ciphertext over the
 * relay — so no key material crosses this boundary.
 */
export class ManagedRelayTransport implements Transport {
  private handler: ((frame: string) => void) | null = null

  constructor(
    private readonly plugin: OctoTunnelPlugin,
    private readonly pairing: PairingInfo,
  ) {}

  async connect(): Promise<void> {
    await this.plugin.loadDeviceIdentity()
    await this.plugin.pair(this.pairing)
    await this.plugin.addListener('frame', ({ frame }) => this.handler?.(frame))
  }

  async send(frame: string): Promise<void> {
    await this.plugin.send({ frame })
  }

  onMessage(handler: (frame: string) => void): void {
    this.handler = handler
  }

  async disconnect(): Promise<void> {
    await this.plugin.disconnect()
  }
}

/**
 * SelfTunnelTransport carries the same frames over a plain WebSocket to the
 * user's own tunnel URL (their reverse proxy / Tailscale / frp), presenting the
 * access key. No relay and no Noise — the user's tunnel provides transport
 * security. The host still terminates the frame protocol at the other end.
 */
export class SelfTunnelTransport implements Transport {
  private ws: WebSocket | null = null
  private handler: ((frame: string) => void) | null = null

  constructor(
    private readonly url: string,
    private readonly accessKey: string,
  ) {}

  connect(): Promise<void> {
    return new Promise((resolve, reject) => {
      const u = new URL('/tunnel', this.url)
      u.protocol = u.protocol === 'https:' ? 'wss:' : 'ws:'
      if (this.accessKey) u.searchParams.set('access_key', this.accessKey)
      const ws = new NativeWebSocket(u.toString())
      this.ws = ws
      ws.onopen = () => resolve()
      ws.onerror = () => reject(new Error('self-tunnel: connection failed'))
      ws.onmessage = (ev: MessageEvent) => {
        if (typeof ev.data === 'string') this.handler?.(ev.data)
      }
    })
  }

  async send(frame: string): Promise<void> {
    this.ws?.send(frame)
  }

  onMessage(handler: (frame: string) => void): void {
    this.handler = handler
  }

  async disconnect(): Promise<void> {
    this.ws?.close()
    this.ws = null
  }
}

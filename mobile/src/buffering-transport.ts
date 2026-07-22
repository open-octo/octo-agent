import type { Transport } from './transport'

// BufferingTransport is the transport the local shim (shim.ts) is installed
// against at boot, before a real transport exists. The bundled frontend starts
// firing /api + /ws traffic the instant its scripts run, but the managed-relay
// session isn't ready until the Noise handshake finishes (and, on first launch,
// until the user has paired at all). This transport absorbs that gap: it holds
// the shim's message handler and buffers every outbound frame, then replays the
// buffer once a connected inner transport is attached. From the shim's side it
// is an ordinary Transport; from boot's side, attach() hands over control.
export class BufferingTransport implements Transport {
  private inner: Transport | null = null
  private handler: ((frame: string) => void) | null = null
  private buffer: string[] = []

  // connect is a no-op: the real transport is connected by boot and handed in
  // via attach(). The shim never calls this.
  async connect(): Promise<void> {}

  onMessage(handler: (frame: string) => void): void {
    this.handler = handler
    if (this.inner) this.inner.onMessage(handler)
  }

  async send(frame: string): Promise<void> {
    if (this.inner) {
      await this.inner.send(frame)
      return
    }
    this.buffer.push(frame)
  }

  async disconnect(): Promise<void> {
    await this.inner?.disconnect()
  }

  // attach wires an already-connected transport in, flushing any frames the
  // frontend sent while we were still pairing/handshaking. The handler is routed
  // first so a response can't arrive before its listener is in place.
  async attach(inner: Transport): Promise<void> {
    if (this.handler) inner.onMessage(this.handler)
    this.inner = inner
    const pending = this.buffer
    this.buffer = []
    for (const frame of pending) await inner.send(frame)
  }

  // detach drops the inner transport (e.g. the session died) and re-enters
  // buffering mode, so a reconnect can flush again.
  detach(): void {
    this.inner = null
  }

  get attached(): boolean {
    return this.inner !== null
  }
}

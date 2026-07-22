import { describe, expect, it } from 'vitest'
import { BufferingTransport } from './buffering-transport'
import type { Transport } from './transport'

// A recording inner transport standing in for a connected ManagedRelayTransport.
class FakeInner implements Transport {
  sent: string[] = []
  handler: ((f: string) => void) | null = null
  async connect(): Promise<void> {}
  async send(frame: string): Promise<void> {
    this.sent.push(frame)
  }
  onMessage(handler: (f: string) => void): void {
    this.handler = handler
  }
  async disconnect(): Promise<void> {}
}

describe('BufferingTransport', () => {
  it('buffers sends before attach and flushes them in order on attach', async () => {
    const buf = new BufferingTransport()
    await buf.send('a')
    await buf.send('b')
    const inner = new FakeInner()
    expect(inner.sent).toEqual([])
    await buf.attach(inner)
    expect(inner.sent).toEqual(['a', 'b'])
  })

  it('forwards sends straight through once attached', async () => {
    const buf = new BufferingTransport()
    const inner = new FakeInner()
    await buf.attach(inner)
    await buf.send('c')
    expect(inner.sent).toEqual(['c'])
  })

  it('routes the shim handler to the inner transport, whenever it was set', async () => {
    const buf = new BufferingTransport()
    const received: string[] = []
    buf.onMessage((f) => received.push(f)) // shim registers before a transport exists
    const inner = new FakeInner()
    await buf.attach(inner)
    inner.handler?.('down')
    expect(received).toEqual(['down'])
  })

  it('re-buffers after detach so a reconnect can flush again', async () => {
    const buf = new BufferingTransport()
    const first = new FakeInner()
    await buf.attach(first)
    buf.detach()
    expect(buf.attached).toBe(false)
    await buf.send('queued')
    const second = new FakeInner()
    await buf.attach(second)
    expect(second.sent).toEqual(['queued'])
    expect(first.sent).toEqual([])
  })
})

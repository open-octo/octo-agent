import { describe, it, expect, beforeEach, vi } from 'vitest'
import { WsManager } from './ws'

// A controllable WebSocket stand-in. The manager only ever constructs it,
// assigns the four handlers, and calls send/close, so the fake is a property
// bag plus a send log. open()/drop() drive the lifecycle from the test.
class FakeWebSocket {
  static instances: FakeWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  readyState = FakeWebSocket.CONNECTING
  onopen: (() => void) | null = null
  onclose: ((ev: unknown) => void) | null = null
  onerror: (() => void) | null = null
  onmessage: ((ev: unknown) => void) | null = null
  sent: string[] = []
  constructor(public url: string) {
    FakeWebSocket.instances.push(this)
  }
  send(msg: string) {
    this.sent.push(msg)
  }
  close() {
    this.readyState = 3
  }
  open() {
    this.readyState = FakeWebSocket.OPEN
    this.onopen?.()
  }
  // A dropped connection the manager hasn't noticed yet: readyState moved,
  // no close event fired (the reconnect timer path is tested implicitly by
  // the manager calling connect() itself — here the test drives it).
  drop() {
    this.readyState = 3
  }
}

vi.stubGlobal('WebSocket', FakeWebSocket)

function lastSocket() {
  return FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
}

beforeEach(() => {
  FakeWebSocket.instances = []
})

describe('WsManager reconnect resync', () => {
  it('does not refetch the session list on the very first connect', () => {
    const mgr = new WsManager()
    const reloads: string[] = []
    mgr.on('session_list_reload', () => reloads.push('reload'))
    mgr.connect()
    lastSocket().open()
    expect(reloads).toEqual([])
  })

  it('refetches the session list on a reconnect — turn_ended broadcasts missed while down are unrecoverable otherwise', () => {
    const mgr = new WsManager()
    const reloads: string[] = []
    mgr.on('session_list_reload', () => reloads.push('reload'))
    mgr.connect()
    lastSocket().open()
    lastSocket().drop()
    mgr.connect()
    lastSocket().open()
    expect(reloads).toEqual(['reload'])
  })

  it('still resyncs subscribed session histories alongside the list reload', () => {
    const mgr = new WsManager()
    const historyReloads: unknown[] = []
    const listReloads: unknown[] = []
    mgr.on('history_reload', ev => historyReloads.push(ev))
    mgr.on('session_list_reload', ev => listReloads.push(ev))
    mgr.subscribe('s1')
    mgr.connect()
    lastSocket().open()
    lastSocket().drop()
    mgr.connect()
    lastSocket().open()
    expect(historyReloads).toEqual([{ type: 'history_reload', session_id: 's1' }])
    expect(listReloads).toEqual([{ type: 'session_list_reload' }])
  })
})

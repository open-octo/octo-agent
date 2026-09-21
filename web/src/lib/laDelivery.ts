// laDelivery.ts — carries a file from the agent into an open artifact.
//
// The opposite direction from laState, and the only one that reaches into a
// page. It cannot be done directly: the tool runs in the server process, the
// page runs in a frame whose own origin cannot fetch octo's API at all. So
// the server announces a claim ticket over the WebSocket, this module redeems
// it for the bytes, and hands them to the frame over postMessage.
//
// The bytes come over HTTP rather than riding the socket, because a generated
// image is large and the socket carries the conversation.

import { laFrameFor } from './laStorage'
import type { WsManager } from './ws'

export interface ArtifactDeliveryEvent {
  session?: unknown
  path?: unknown
  id?: unknown
  name?: unknown
  note?: unknown
}

// Exported for tests: fetch the bytes and hand them to the frame. Returns what
// happened, so a test can assert the frame was (or was not) posted to.
export async function deliverToFrame(ev: ArtifactDeliveryEvent): Promise<'delivered' | 'no-frame' | 'failed'> {
  const session = typeof ev.session === 'string' ? ev.session : ''
  const path = typeof ev.path === 'string' ? ev.path : ''
  const id = typeof ev.id === 'string' ? ev.id : ''
  if (!session || !path || !id) return 'failed'

  // The frame has to be open right now. A page the user closed between the
  // tool call and this event simply misses it — the ticket then expires on
  // its own, and the agent finds out through artifact_state like everything
  // else about the page.
  const ns = session + '\n' + path
  const target = laFrameFor(ns)
  if (!target || !target.origin) return 'no-frame'

  try {
    const res = await fetch(
      `/api/sessions/${encodeURIComponent(session)}/artifacts/delivery/${encodeURIComponent(id)}`,
    )
    if (!res.ok) return 'failed'
    const blob = await res.blob()
    target.win.postMessage(
      {
        __laBridge: 1,
        id: 0,
        ns,
        op: 'delivery',
        name: typeof ev.name === 'string' ? ev.name : 'image',
        note: typeof ev.note === 'string' ? ev.note : '',
        blob,
      },
      // Never '*': the bytes are the agent's, and the frame may have taken
      // itself somewhere else since it registered.
      target.origin,
    )
    return 'delivered'
  } catch {
    return 'failed'
  }
}

/** Subscribe to delivery announcements. Returns an unsubscribe function. */
export function installLaDeliveryBridge(ws: WsManager): () => void {
  return ws.on('artifact_delivery', (ev: unknown) => {
    void deliverToFrame((ev ?? {}) as ArtifactDeliveryEvent)
  })
}

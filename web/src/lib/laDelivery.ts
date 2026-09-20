// laDelivery.ts — carries a file from the agent into a running Light App.
//
// The opposite direction from laState, and the only one that reaches into an
// app. It cannot be done directly: the tool runs in the server process, the app
// runs in a frame whose own origin cannot fetch octo's API at all. So the
// server announces a claim ticket over the WebSocket, this module redeems it
// for the bytes, and hands them to the frame over postMessage.
//
// The bytes come over HTTP rather than riding the socket, because a generated
// image is large and the socket carries the conversation.

import { laFrameFor } from './laStorage'
import type { WsManager } from './ws'

export interface LaDeliveryEvent {
  slug?: unknown
  id?: unknown
  name?: unknown
  note?: unknown
}

// Exported for tests: fetch the bytes and hand them to the frame. Returns what
// happened, so a test can assert the frame was (or was not) posted to.
export async function deliverToFrame(ev: LaDeliveryEvent): Promise<'delivered' | 'no-frame' | 'failed'> {
  const slug = typeof ev.slug === 'string' ? ev.slug : ''
  const id = typeof ev.id === 'string' ? ev.id : ''
  if (!slug || !id) return 'failed'

  // The frame has to be open right now. An app the user closed between the
  // tool call and this event simply misses it — the ticket then expires on
  // its own, and the agent finds out through lightapp_state like everything
  // else about the app.
  const win = laFrameFor(slug)
  if (!win) return 'no-frame'

  try {
    const res = await fetch(
      `/api/light-apps/${encodeURIComponent(slug)}/delivery/${encodeURIComponent(id)}`,
    )
    if (!res.ok) return 'failed'
    const blob = await res.blob()
    win.postMessage(
      {
        __laBridge: 1,
        id: 0,
        ns: slug,
        op: 'delivery',
        name: typeof ev.name === 'string' ? ev.name : 'image',
        note: typeof ev.note === 'string' ? ev.note : '',
        blob,
      },
      '*',
    )
    return 'delivered'
  } catch {
    return 'failed'
  }
}

/** Subscribe to delivery announcements. Returns an unsubscribe function. */
export function installLaDeliveryBridge(ws: WsManager): () => void {
  return ws.on('lightapp_delivery', (ev: unknown) => {
    void deliverToFrame((ev ?? {}) as LaDeliveryEvent)
  })
}

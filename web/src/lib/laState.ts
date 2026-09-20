// laState.ts — relays a Light App's state snapshot to the server.
//
// The app cannot reach the API itself: it runs on its own origin behind a CSP
// that closes every network exit. It posts a snapshot over the `__laBridge`
// channel instead (laStorage.ts routes it here), and this module — running in
// the host page, which has the origin and the cookie — puts it on
// PUT /api/light-apps/{slug}/state. The mirror there is what the model's
// lightapp_state / view_lightapp tools read.
//
// The app gains no capability from this. It only gets to be seen.

// A canvas can fire on every stroke, so snapshots are coalesced per app: one
// request in flight, at most one queued, and never more than one per window.
// The queued snapshot is always the newest — an intermediate state nobody
// asked about is worth nothing.
const MIN_INTERVAL_MS = 1000

// Anything larger is dropped rather than sent: the server caps the body too,
// and a refused upload would cost the same round trip.
const MAX_IMAGE_BYTES = 12 * 1024 * 1024

export interface LaStatePush {
  digest?: unknown
  summary?: unknown
  image?: unknown
}

type Pending = { digest: string; summary: string; image: Blob | null }

const queued = new Map<string, Pending>()
const timers = new Map<string, ReturnType<typeof setTimeout>>()
const lastSent = new Map<string, number>()

// Exported for tests: the shape the server receives.
export function buildStateForm(p: Pending): FormData {
  const form = new FormData()
  form.set('digest', p.digest)
  if (p.summary) form.set('summary', p.summary)
  if (p.image) form.set('image', p.image, 'state.png')
  return form
}

async function flush(slug: string): Promise<void> {
  const p = queued.get(slug)
  queued.delete(slug)
  timers.delete(slug)
  if (!p) return
  lastSent.set(slug, Date.now())
  try {
    await fetch(`/api/light-apps/${encodeURIComponent(slug)}/state`, {
      method: 'PUT',
      body: buildStateForm(p),
    })
  } catch {
    // A dropped snapshot is not worth surfacing: the app will push again on
    // its next change, and the mirror's staleness marker already tells the
    // model that what it has may have moved on.
  }
}

function schedule(slug: string): void {
  if (timers.has(slug)) return
  const since = Date.now() - (lastSent.get(slug) ?? 0)
  const wait = Math.max(0, MIN_INTERVAL_MS - since)
  timers.set(slug, setTimeout(() => void flush(slug), wait))
}

/** Accept one `state` message from a Light App frame. */
export function acceptLaState(slug: string, msg: LaStatePush): void {
  const digest = typeof msg.digest === 'string' ? msg.digest : ''
  let summary = ''
  if (msg.summary !== undefined && msg.summary !== null) {
    try {
      summary = JSON.stringify(msg.summary)
    } catch {
      // Circular or otherwise unserialisable: the digest still goes.
    }
  }
  const image = msg.image instanceof Blob && msg.image.size <= MAX_IMAGE_BYTES ? msg.image : null
  if (!digest && !summary && !image) return

  queued.set(slug, { digest, summary, image })
  schedule(slug)
}

/** Forget an app server-side — its frame went away. */
export function dropLaState(slug: string): void {
  const t = timers.get(slug)
  if (t !== undefined) clearTimeout(t)
  timers.delete(slug)
  queued.delete(slug)
  lastSent.delete(slug)
  void fetch(`/api/light-apps/${encodeURIComponent(slug)}/state`, { method: 'DELETE' }).catch(() => {})
}

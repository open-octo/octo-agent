// laState.ts — relays a sandboxed page's state snapshot to the server.
//
// The page cannot reach the API itself: it runs on its own origin behind a
// CSP that closes every network exit. It posts a snapshot over the
// `__laBridge` channel instead (laStorage.ts routes it here), and this module
// — running in the host page, which has the origin and the cookie — puts it
// on the matching endpoint:
//
//   Light App          PUT /api/light-apps/{slug}/state
//   session artifact   PUT /api/sessions/{id}/artifacts/state?path=…
//
// The mirror there is what the model's state/view tools read. The page gains
// no capability from this. It only gets to be seen.

// A canvas can fire on every stroke, so snapshots are coalesced per identity:
// at most one queued, and never more than one request per window. The queued
// snapshot is always the newest — an intermediate state nobody asked about is
// worth nothing. Requests are not serialised beyond that; the server keeps
// whichever arrives last, and one window apart they do not overlap in
// practice.
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

// Keyed by identity: a slug for a Light App, `session\npath` for an artifact
// — the same string the bridge stamps into the message's ns.
const queued = new Map<string, Pending>()
const timers = new Map<string, ReturnType<typeof setTimeout>>()
const lastSent = new Map<string, number>()
// Bumped whenever a page goes away. A request already awaiting fetch cannot
// be cancelled, and if it lands after the DELETE the server is left
// describing a page nobody has open — so a flush whose generation moved on
// drops its result instead of racing it.
const generation = new Map<string, number>()

// Exported for tests: the shape the server receives.
export function buildStateForm(p: Pending): FormData {
  const form = new FormData()
  form.set('digest', p.digest)
  if (p.summary) form.set('summary', p.summary)
  if (p.image) form.set('image', p.image, 'state.png')
  return form
}

async function flush(key: string, endpoint: string): Promise<void> {
  const p = queued.get(key)
  queued.delete(key)
  timers.delete(key)
  if (!p) return
  const gen = generation.get(key) ?? 0
  lastSent.set(key, Date.now())
  try {
    await fetch(endpoint, {
      method: 'PUT',
      body: buildStateForm(p),
    })
  } catch {
    // A dropped snapshot is not worth surfacing: the page will push again on
    // its next change, and the mirror's staleness marker already tells the
    // model that what it has may have moved on.
  }
  // The page went away while this was in flight. Its DELETE and this PUT then
  // raced, and a PUT that lands second leaves the mirror describing a page
  // nobody has open — for the full eviction window. Aborting cannot help
  // (the request was already sent), so undo it instead.
  if ((generation.get(key) ?? 0) !== gen) {
    void fetch(endpoint, { method: 'DELETE' }).catch(() => {})
  }
}

function schedule(key: string, endpoint: string): void {
  if (timers.has(key)) return
  const since = Date.now() - (lastSent.get(key) ?? 0)
  const wait = Math.max(0, MIN_INTERVAL_MS - since)
  timers.set(key, setTimeout(() => void flush(key, endpoint), wait))
}

function accept(key: string, endpoint: string, msg: LaStatePush): void {
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

  queued.set(key, { digest, summary, image })
  schedule(key, endpoint)
}

function drop(key: string, endpoint: string): void {
  const t = timers.get(key)
  if (t !== undefined) clearTimeout(t)
  timers.delete(key)
  queued.delete(key)
  lastSent.delete(key)
  generation.set(key, (generation.get(key) ?? 0) + 1)
  void fetch(endpoint, { method: 'DELETE' }).catch(() => {})
}

const artifactEndpoint = (session: string, path: string) =>
  `/api/sessions/${encodeURIComponent(session)}/artifacts/state?path=${encodeURIComponent(path)}`
const artifactKey = (session: string, path: string) => session + '\n' + path

/** Accept one `state` message from a session artifact's frame. */
export function acceptArtifactState(session: string, path: string, msg: LaStatePush): void {
  accept(artifactKey(session, path), artifactEndpoint(session, path), msg)
}

/** Forget an artifact server-side — its frame went away. */
export function dropArtifactState(session: string, path: string): void {
  drop(artifactKey(session, path), artifactEndpoint(session, path))
}

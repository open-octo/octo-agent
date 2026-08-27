// Light App storage bridge — makes `localStorage` WORK inside Light Apps.
//
// Light Apps render in a sandboxed srcdoc iframe (`sandbox="allow-scripts"`,
// deliberately without `allow-same-origin`), so their origin is opaque and the
// browser refuses every persistent storage API: localStorage, sessionStorage,
// Cookie, IndexedDB all throw SecurityError in there. Persistent state
// (scores, collections, settings) was impossible for Light Apps.
//
// This module fixes that WITHOUT touching the sandbox. The host document owns
// an IndexedDB, and an injected bridge script replaces `window.localStorage`
// inside the iframe with a synchronous shim backed by an in-memory cache:
//
//   - getItem / length / key: served synchronously from the cache
//   - setItem / removeItem / clear: update the cache synchronously, then
//     persist through a background postMessage to the host
//   - on load the script prefetches the app's full key set (async) so the
//     cache starts warm; `window.__laStorageReady` resolves when that lands
//
// Light Apps keep using the plain `localStorage` API — no special interface,
// zero code changes. Where localStorage genuinely works (non-sandboxed
// contexts) the script does nothing at all.
//
// Only sessionStorage is deliberately left broken: a Light App that wants
// per-visit state can keep it in a plain variable.
//
// Security model:
//   - Only iframes the host registered (registerLaIframe) can read/write; the
//     message handler checks event.source against the registry and ignores
//     everything else (incl. the app's own artifact-preview iframes).
//   - Every request must carry the namespace its bridge script was built with,
//     and it must match the one the host registered for that window. The
//     iframe element is reused across app switches (same WindowProxy, fresh
//     srcdoc), so without this a departing document's in-flight write would
//     land in the incoming app's namespace.
//   - The host stores under the ns it registered, never the one the message
//     claims, so an app cannot read or write another app's keys.
//   - Keys are validated strings (<=512 chars, non-empty); values are strings
//     capped at MAX_VALUE. The shim enforces the same limits synchronously so
//     a rejected write can never leave the cache disagreeing with the store.

const DB_NAME = 'octo-la-storage'
const STORE = 'kv'
const MAX_KEY = 512
// Per-key and per-app ceilings, in UTF-16 code units. MAX_TOTAL mirrors the
// ~5MB per origin real localStorage gives an app; MAX_VALUE keeps one runaway
// write from eating the whole budget in a single message.
const MAX_VALUE = 1_000_000
const MAX_TOTAL = 5_000_000

const laFrames = new Map<Window, string>() // iframe window -> namespace
let bridgeInstalled = false

export function registerLaIframe(win: Window | null | undefined, ns: string): void {
  if (!win) return
  laFrames.set(win, ns)
}

export function unregisterLaIframe(win: Window | null | undefined): void {
  if (win) laFrames.delete(win)
}

// ── IndexedDB ───────────────────────────────────────────────────────────────

let dbPromise: Promise<IDBDatabase> | null = null

function openDb(): Promise<IDBDatabase> {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, 1)
      req.onupgradeneeded = () => {
        const db = req.result
        if (!db.objectStoreNames.contains(STORE)) db.createObjectStore(STORE)
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error ?? new Error('IndexedDB open failed'))
    })
  }
  return dbPromise
}

function run<T>(mode: IDBTransactionMode, fn: (store: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  return openDb().then(
    (db) =>
      new Promise<T>((resolve, reject) => {
        const t = db.transaction(STORE, mode)
        const req = fn(t.objectStore(STORE))
        req.onsuccess = () => resolve(req.result)
        req.onerror = () => reject(req.error ?? new Error('IndexedDB request failed'))
      }),
  )
}

function fullKey(ns: string, key: string): string {
  return `${ns}:${key}`
}

function laSet(ns: string, key: string, value: string): Promise<unknown> {
  return run('readwrite', (s) => s.put(value, fullKey(ns, key)))
}

function laRemove(ns: string, key: string): Promise<unknown> {
  return run('readwrite', (s) => s.delete(fullKey(ns, key)))
}

// dump: all key/values under the namespace (prefetch for the iframe shim).
function laDump(ns: string): Promise<Record<string, string>> {
  return openDb().then(
    (db) =>
      new Promise<Record<string, string>>((resolve, reject) => {
        const t = db.transaction(STORE, 'readonly')
        const store = t.objectStore(STORE)
        const prefix = ns + ':'
        const out: Record<string, string> = {}
        const cur = store.openCursor()
        cur.onsuccess = () => {
          const c = cur.result
          if (c) {
            if (typeof c.key === 'string' && c.key.startsWith(prefix)) {
              out[c.key.slice(prefix.length)] = String(c.value)
            }
            c.continue()
          } else {
            resolve(out)
          }
        }
        cur.onerror = () => reject(cur.error ?? new Error('IndexedDB cursor failed'))
      }),
  )
}

// clear: removes every key under the namespace, leaving other apps untouched.
async function laClear(ns: string): Promise<void> {
  const db = await openDb()
  await new Promise<void>((resolve, reject) => {
    const t = db.transaction(STORE, 'readwrite')
    const store = t.objectStore(STORE)
    const prefix = ns + ':'
    const keys: IDBValidKey[] = []
    const cur = store.openCursor()
    cur.onsuccess = () => {
      const c = cur.result
      if (c) {
        if (typeof c.key === 'string' && c.key.startsWith(prefix)) keys.push(c.key)
        c.continue()
      } else {
        keys.forEach((k) => store.delete(k))
        resolve()
      }
    }
    cur.onerror = () => reject(cur.error ?? new Error('IndexedDB cursor failed'))
  })
}

// ── Bridge protocol ─────────────────────────────────────────────────────────
//
// iframe -> parent:  { __laBridge: 1, id, ns, op: 'dump'|'set'|'remove'|'clear', key?, value? }
// parent -> iframe:  { __laBridge: 1, id, res: true, ok, value?, err? }
//
// `res: true` marks the direction, so a nested iframe's request reaching this
// window is never mistaken for a reply to one of our own calls.

function validKey(key: unknown): key is string {
  return typeof key === 'string' && key.length > 0 && key.length <= MAX_KEY
}

function validValue(value: unknown): value is string {
  return typeof value === 'string' && value.length <= MAX_VALUE
}

function onLaMessage(ev: MessageEvent): void {
  const ns = laFrames.get(ev.source as Window)
  if (!ns) return
  const d = ev.data as Record<string, unknown> | null
  if (!d || d.__laBridge !== 1) return
  if (d.ns !== ns) return // stale document from before an app switch
  const id = d.id
  const op = d.op
  if (typeof id !== 'number' || typeof op !== 'string') return
  const reply = (ok: boolean, value?: unknown, err?: string): void => {
    ;(ev.source as Window).postMessage({ __laBridge: 1, id, res: true, ok, value, err }, '*')
  }
  const fail = (e: unknown): void => reply(false, undefined, String(e))

  switch (op) {
    case 'dump':
      laDump(ns).then((v) => reply(true, v)).catch(fail)
      break
    case 'set':
      if (!validKey(d.key) || !validValue(d.value)) return reply(false, undefined, 'bad key/value')
      laSet(ns, d.key, d.value).then(() => reply(true)).catch(fail)
      break
    case 'remove':
      if (!validKey(d.key)) return reply(false, undefined, 'bad key')
      laRemove(ns, d.key).then(() => reply(true)).catch(fail)
      break
    case 'clear':
      laClear(ns).then(() => reply(true)).catch(fail)
      break
    default:
      reply(false, undefined, 'unknown op')
  }
}

export function installLaStorageBridge(): void {
  if (bridgeInstalled) return
  bridgeInstalled = true
  window.addEventListener('message', onLaMessage)
}

// ── iframe-side bridge script ───────────────────────────────────────────────
//
// Injected into the Light App's srcdoc just before </body>. If localStorage
// natively works (non-sandboxed context) it does nothing. Otherwise it swaps
// `window.localStorage` for a synchronous shim: reads come from an in-memory
// cache prefetched from the host (dump), writes update the cache synchronously
// and persist via background postMessage. `window.__laStorageReady` resolves
// once the prefetch lands, for apps that read storage at startup.
//
// Writes the app makes before the prefetch lands win: `dirty` keys and a
// post-clear `wiped` flag tell the dump handler what not to overwrite, so the
// cache never reverts to a value the app has already replaced.
//
// The limits the host enforces are re-checked here synchronously, and a
// violation throws QuotaExceededError like real localStorage does — a write
// the host would reject must not silently sit in the cache.

// Embed a string as a JS literal that is also inert inside <script> in HTML.
function jsLiteral(s: string): string {
  return JSON.stringify(s).replace(/</g, '\\u003c').replace(/>/g, '\\u003e')
}

export function buildLaBridgeScript(ns: string): string {
  return `(function(){
  var usable = false;
  try { localStorage.getItem('__la_probe'); usable = true; } catch (e) {}
  if (usable) return; // native localStorage works here — nothing to shim
  var NS = ${jsLiteral(ns)}, MAX_KEY = ${MAX_KEY}, MAX_VALUE = ${MAX_VALUE}, MAX_TOTAL = ${MAX_TOTAL};
  var mem = Object.create(null), pend = {}, seq = 0, used = 0;
  var dirty = Object.create(null), wiped = false;
  var readyResolve;
  window.__laStorageReady = new Promise(function(res){ readyResolve = res; });
  function has(k){ return Object.prototype.hasOwnProperty.call(mem, k); }
  function quota(){
    var e = new Error("Failed to execute 'setItem' on 'Storage': the quota has been exceeded.");
    e.name = 'QuotaExceededError';
    return e;
  }
  window.addEventListener('message', function(ev){
    var d = ev.data;
    if (!d || d.__laBridge !== 1 || !d.res) return;
    var p = pend[d.id]; if (!p) return;
    delete pend[d.id];
    if (d.ok) p.resolve(d.value); else p.reject(new Error(d.err || 'storage bridge failed'));
  });
  function call(op, key, value){
    return new Promise(function(resolve, reject){
      var id = ++seq;
      pend[id] = { resolve: resolve, reject: reject };
      try { window.parent.postMessage({ __laBridge: 1, id: id, ns: NS, op: op, key: key, value: value }, '*'); }
      catch (e) { delete pend[id]; reject(e); }
      setTimeout(function(){ if (pend[id]) { delete pend[id]; reject(new Error('storage bridge timeout')); } }, 3000);
    });
  }
  // Warm the cache with everything the app already persisted, without undoing
  // whatever it wrote while the prefetch was still in flight.
  call('dump').then(function(map){
    if (!map || wiped) return;
    for (var k in map) {
      if (dirty[k]) continue;
      mem[k] = map[k];
      used += k.length + map[k].length;
    }
  }).catch(function(){}).then(function(){ readyResolve(); });
  function emit(op, key, value){
    try { window.parent.postMessage({ __laBridge: 1, id: ++seq, ns: NS, op: op, key: key, value: value }, '*'); }
    catch (e) {}
  }
  var shim = {
    getItem: function(k){ k = String(k); return has(k) ? mem[k] : null; },
    setItem: function(k, v){
      k = String(k); v = String(v);
      if (k.length === 0 || k.length > MAX_KEY || v.length > MAX_VALUE) throw quota();
      var next = used + k.length + v.length - (has(k) ? k.length + mem[k].length : 0);
      if (next > MAX_TOTAL) throw quota();
      mem[k] = v; used = next; dirty[k] = true;
      emit('set', k, v);
    },
    removeItem: function(k){
      k = String(k);
      if (has(k)) used -= k.length + mem[k].length;
      delete mem[k]; dirty[k] = true;
      emit('remove', k);
    },
    clear: function(){
      mem = Object.create(null); dirty = Object.create(null);
      used = 0; wiped = true;
      emit('clear');
    },
    key: function(i){ var ks = Object.keys(mem); return i >= 0 && i < ks.length ? ks[i] : null; },
    get length(){ return Object.keys(mem).length; }
  };
  try { Object.defineProperty(window, 'localStorage', { value: shim, configurable: true, writable: true }); }
  catch (e) { try { window.localStorage = shim; } catch (e2) {} }
})();`
}

/** Inject the bridge script into a Light App document before </body>. */
export function withLaBridge(html: string, ns: string): string {
  const script = `<script>${buildLaBridgeScript(ns)}<\\/script>`
  if (/\<\/body\s*>/i.test(html)) return html.replace(/\<\/body\s*>/i, script + '</body>')
  return html + script
}

import { installShim } from './shim'
import { BufferingTransport } from './buffering-transport'
import { ManagedRelayTransport } from './transport'
import { OctoTunnel, registerPlugin } from './plugin'
import { parsePairingURL, type PairingInfo } from './pairing'
import { scanPairingURL } from './qr'

// The first-party App plugin, resolved through the same native bridge as
// OctoTunnel (see plugin.ts) rather than @capacitor/app's JS, so a scanned QR
// arriving as an octo-pair:// deep link reaches us via appUrlOpen.
interface AppPlugin {
  getLaunchUrl(): Promise<{ url: string } | null>
  addListener(event: 'appUrlOpen', cb: (data: { url: string }) => void): void
}
const App = registerPlugin<AppPlugin>('App')

// boot is octo-mobile's entry point. It is bundled to a classic script and
// injected as the first element of the bundled frontend's <head>, so it runs
// before any frontend module. It installs the local shim (shim.ts) against a
// BufferingTransport immediately — so the frontend's /api + /ws traffic is
// captured and held from its very first request — then pairs (scanning the
// host's QR on first launch, or reusing the stored pairing), stands up the
// native managed-relay transport, and hands it to the buffer to flush. The
// frontend never learns it started life offline.

const STORAGE_KEY = 'octo:pairing'

const buffering = new BufferingTransport()
installShim(buffering)

// Guards against pairing more than once. On a cold start into a deep link,
// Capacitor delivers the same octo-pair:// URL through BOTH getLaunchUrl() and
// appUrlOpen; without this, two pair() calls race on the one-time token — the
// first pairs, the second is rejected 403 and tears the shared session down.
let pairing = false

function loadStored(): PairingInfo | null {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    return raw ? (JSON.parse(raw) as PairingInfo) : null
  } catch {
    return null
  }
}

function saveStored(info: PairingInfo): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(info))
  } catch {
    // Best effort: a paired session still works this launch without persistence.
  }
}

async function connect(info: PairingInfo): Promise<void> {
  const transport = new ManagedRelayTransport(OctoTunnel, info)
  await transport.connect()
  await buffering.attach(transport)
}

// ── pairing overlay ─────────────────────────────────────────────────────────
// A minimal full-screen gate shown until a session is live. The frontend is
// rendering underneath (its requests buffered), so this must fully cover it.

let overlay: HTMLElement | null = null
let statusEl: HTMLElement | null = null

function ensureBody(): Promise<void> {
  if (document.body) return Promise.resolve()
  return new Promise((r) => document.addEventListener('DOMContentLoaded', () => r(), { once: true }))
}

async function showOverlay(): Promise<void> {
  await ensureBody()
  if (overlay) return
  overlay = document.createElement('div')
  overlay.style.cssText =
    'position:fixed;inset:0;z-index:2147483646;background:#0f1115;color:#fff;' +
    'display:flex;flex-direction:column;align-items:center;justify-content:center;' +
    'gap:20px;font-family:system-ui,-apple-system,sans-serif;padding:32px;text-align:center;'

  const title = document.createElement('div')
  title.textContent = 'octo'
  title.style.cssText = 'font-size:32px;font-weight:700;letter-spacing:-0.5px;'

  const sub = document.createElement('div')
  sub.textContent = '连接到你的 octo serve'
  sub.style.cssText = 'font-size:15px;opacity:0.7;'

  const button = document.createElement('button')
  button.textContent = '扫码配对'
  button.style.cssText =
    'padding:14px 40px;border:0;border-radius:26px;background:#3b82f6;color:#fff;' +
    'font-size:16px;font-weight:600;'
  button.onclick = () => void startScan()

  statusEl = document.createElement('div')
  statusEl.style.cssText = 'font-size:13px;opacity:0.6;min-height:18px;'

  overlay.append(title, sub, button, statusEl)
  document.body.appendChild(overlay)
}

function setStatus(text: string): void {
  if (statusEl) statusEl.textContent = text
}

function hideOverlay(): void {
  overlay?.remove()
  overlay = null
  statusEl = null
}

async function pairWith(url: string): Promise<void> {
  if (pairing || buffering.attached) return
  pairing = true
  try {
    const info = parsePairingURL(url)
    setStatus('正在建立加密隧道…')
    await connect(info)
    saveStored(info)
    hideOverlay()
  } catch (err) {
    pairing = false // allow a retry (scan again / re-open the link)
    throw err
  }
}

async function startScan(): Promise<void> {
  try {
    const url = await scanPairingURL()
    await pairWith(url)
  } catch (err) {
    if (err instanceof Error && err.message === 'scan cancelled') return
    setStatus(`配对失败：${err instanceof Error ? err.message : String(err)}`)
  }
}

// ── entry ─────────────────────────────────────────────────────────────────
async function boot(): Promise<void> {
  const stored = loadStored()
  if (stored) {
    try {
      await connect(stored)
      return
    } catch {
      // The stored pairing is stale (host rekeyed, token spent, relay moved).
      // Drop it and fall through to a fresh pairing.
      try {
        localStorage.removeItem(STORAGE_KEY)
      } catch {
        // ignore
      }
    }
  }

  await showOverlay()

  // A QR scanned by the system camera app (or an `adb … VIEW` in dev) arrives as
  // an octo-pair:// deep link, delivered through the App plugin.
  if (App) {
    App.getLaunchUrl()
      .then((launch) => {
        if (launch?.url?.startsWith('octo-pair://')) void pairWith(launch.url).catch(() => {})
      })
      .catch(() => {})
    App.addListener('appUrlOpen', (event) => {
      if (event.url?.startsWith('octo-pair://')) void pairWith(event.url).catch((err) => setStatus(String(err)))
    })
  }
}

void boot()

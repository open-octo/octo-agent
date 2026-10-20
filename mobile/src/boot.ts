import { installShim } from './shim'
import { loadConnection, parseConnectLink, saveConnection, type Connection } from './connection'
import { scanConnectLink } from './qr'

// boot is octo-mobile's entry point. It is bundled to a classic script and
// injected as the first element of the bundled frontend's <head>, so it runs
// before any frontend module. With a saved connection it installs the shim
// (shim.ts) right away, so the frontend's very first /api call already goes to
// the server. Without one it covers the page with a connect screen; once a link
// checks out it is saved and the page reloads into the first case.

const zh = (navigator.language || '').toLowerCase().startsWith('zh')
const text = zh
  ? {
      sub: '连接到你的 octo serve',
      scan: '扫码连接',
      paste: '或粘贴 octo serve 打印的链接',
      connect: '连接',
      checking: '正在连接…',
      badKey: 'access key 不对',
      unreachable: (base: string) => `连不上 ${base}，确认手机能访问这个地址`,
      failed: (msg: string) => `连接失败：${msg}`,
    }
  : {
      sub: 'Connect to your octo serve',
      scan: 'Scan QR code',
      paste: 'or paste the link octo serve prints',
      connect: 'Connect',
      checking: 'Connecting…',
      badKey: 'The access key was rejected',
      unreachable: (base: string) => `Can't reach ${base} — make sure this phone can reach that address`,
      failed: (msg: string) => `Connection failed: ${msg}`,
    }

// window.octoMobile tells the mobile UI (web/src/mobile) which server it is on,
// without importing anything from this project: page URLs must name it.
declare global {
  interface Window {
    octoMobile?: { server: string }
  }
}

// check probes a gated endpoint, so a wrong key shows as 401 rather than
// passing on an exempt route.
async function check(conn: Connection): Promise<void> {
  const ctl = new AbortController()
  const timer = setTimeout(() => ctl.abort(), 8000)
  let r: Response
  try {
    r = await fetch(`${conn.base}/api/sessions?limit=1`, {
      headers: { 'X-Access-Key': conn.key },
      signal: ctl.signal,
    })
  } catch {
    throw new Error(text.unreachable(conn.base))
  } finally {
    clearTimeout(timer)
  }
  if (r.status === 401) throw new Error(text.badKey)
  if (!r.ok) throw new Error(text.failed(`HTTP ${r.status}`))
}

// ── connect screen ──────────────────────────────────────────────────────────
// A full-screen gate over the frontend, which renders underneath unconnected.

let statusEl: HTMLElement | null = null
let busy = false

function ensureBody(): Promise<void> {
  if (document.body) return Promise.resolve()
  return new Promise((r) => document.addEventListener('DOMContentLoaded', () => r(), { once: true }))
}

function setStatus(s: string): void {
  if (statusEl) statusEl.textContent = s
}

async function connectWith(link: string): Promise<void> {
  if (busy) return
  busy = true
  try {
    const conn = parseConnectLink(link)
    setStatus(text.checking)
    await check(conn)
    saveConnection(conn)
    location.reload()
  } catch (err) {
    setStatus(err instanceof Error ? err.message : String(err))
  } finally {
    busy = false
  }
}

async function showConnectScreen(): Promise<void> {
  await ensureBody()
  const overlay = document.createElement('div')
  overlay.style.cssText =
    'position:fixed;inset:0;z-index:2147483646;background:#0f1115;color:#fff;' +
    'display:flex;flex-direction:column;align-items:center;justify-content:center;' +
    'gap:20px;font-family:system-ui,-apple-system,sans-serif;padding:32px;text-align:center;'

  const title = document.createElement('div')
  title.textContent = 'octo'
  title.style.cssText = 'font-size:32px;font-weight:700;letter-spacing:-0.5px;'

  const sub = document.createElement('div')
  sub.textContent = text.sub
  sub.style.cssText = 'font-size:15px;opacity:0.7;'

  const scan = document.createElement('button')
  scan.textContent = text.scan
  scan.style.cssText =
    'padding:14px 40px;border:0;border-radius:26px;background:#1677ff;color:#fff;' +
    'font-size:16px;font-weight:600;'
  scan.onclick = async () => {
    try {
      await connectWith(await scanConnectLink())
    } catch (err) {
      if (err instanceof Error && err.message === 'scan cancelled') return
      setStatus(text.failed(err instanceof Error ? err.message : String(err)))
    }
  }

  const form = document.createElement('form')
  form.style.cssText = 'display:flex;gap:8px;width:100%;max-width:360px;'
  const input = document.createElement('input')
  input.type = 'url'
  input.placeholder = text.paste
  input.autocapitalize = 'off'
  input.spellcheck = false
  input.style.cssText =
    'flex:1;min-width:0;padding:10px 12px;border-radius:10px;border:1px solid #333;' +
    'background:#1a1d24;color:#fff;font-size:14px;'
  const go = document.createElement('button')
  go.type = 'submit'
  go.textContent = text.connect
  go.style.cssText = 'padding:0 16px;border:0;border-radius:10px;background:#2a2f3a;color:#fff;font-size:14px;'
  form.append(input, go)
  form.onsubmit = (e) => {
    e.preventDefault()
    if (input.value.trim()) void connectWith(input.value)
  }

  statusEl = document.createElement('div')
  statusEl.style.cssText = 'font-size:13px;opacity:0.7;min-height:18px;max-width:360px;'

  overlay.append(title, sub, scan, form, statusEl)
  document.body.appendChild(overlay)
}

// iOS zooms the page in when an input under 16px takes focus (the connect
// field, the chat composer) and the WebView keeps that zoom, leaving the app
// wider than the screen. maximum-scale=1 stops the focus zoom; iOS still lets
// the user pinch-zoom. Applied here so the desktop web UI is unaffected.
function pinViewportScale(): void {
  const meta = document.querySelector('meta[name=viewport]')
  if (meta && !/maximum-scale/.test(meta.getAttribute('content') ?? '')) {
    meta.setAttribute('content', `${meta.getAttribute('content')}, maximum-scale=1`)
  }
}

// ── entry ─────────────────────────────────────────────────────────────────
if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', pinViewportScale, { once: true })
} else {
  pinViewportScale()
}

const saved = loadConnection()
if (saved) {
  installShim(saved)
  window.octoMobile = { server: saved.base }
} else {
  void showConnectScreen()
}

// Light App download bridge — makes `<a download>` WORK inside Light Apps.
//
// Light Apps render in a sandboxed srcdoc iframe without `allow-downloads`,
// so the browser drops every download the app starts (Chrome logs "Download
// is disallowed. The frame initiating or instantiating the download is
// sandboxed…"). The desktop webview is worse: the octo-served page has no
// download delegate at all, so even an unsandboxed download would be a silent
// no-op there (see internal/server/native_handlers.go, SaveFile).
//
// Same fix as the storage bridge (laStorage.ts): leave the sandbox alone and
// let the host do it. The injected script intercepts the standard download
// idiom — an anchor with a `download` attribute pointing at a blob:/data: URL
// — reads the bytes into a Blob and posts it to the host, which saves it the
// way the artifact panel does: the OS save dialog in the desktop shell, a
// top-level blob download in a browser.
//
// Light Apps keep writing the textbook pattern
//   const a = document.createElement('a')
//   a.href = URL.createObjectURL(blob); a.download = 'report.csv'; a.click()
// with no special API. Both shapes are covered: a real user click on an
// in-document anchor (document-level click listener) and the programmatic
// `.click()` on a detached anchor, whose event never reaches the document
// (HTMLAnchorElement.prototype.click patch). An app that already called
// preventDefault() on the click keeps its own handling.
//
// Not intercepted: window.open(blobUrl) and location.href = dataUrl. Neither is
// a download in an unsandboxed page either.

import { get } from 'svelte/store'
import { nativeShell, showToast } from './stores'
import { tr } from './i18n'
import * as api from './api'

// Ceiling for one file. Generous for anything a client-side tool produces
// (images, spreadsheets, zips) while keeping a runaway app from handing the
// host a multi-GB blob to base64.
export const MAX_DOWNLOAD_BYTES = 100 * 1024 * 1024

// The name is only a default for the save dialog / download prompt, but it
// still must not carry path separators or control characters into either.
export function sanitizeDownloadName(name: unknown): string {
  if (typeof name !== 'string') return 'download'
  // eslint-disable-next-line no-control-regex
  const cleaned = name.replace(/[/\\\u0000-\u001f\u007f]+/g, '_').trim()
  if (!cleaned || cleaned === '.' || cleaned === '..') return 'download'
  return cleaned.length > 255 ? cleaned.slice(0, 255) : cleaned
}

function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => {
      const url = String(reader.result)
      resolve(url.slice(url.indexOf(',') + 1))
    }
    reader.onerror = () => reject(reader.error ?? new Error('read failed'))
    reader.readAsDataURL(blob)
  })
}

// Saves a blob the Light App handed over. Resolves to whether a file was
// written (false on a cancelled or failed native save). The browser path
// mirrors artifact-actions.ts: an in-document anchor, since a detached
// anchor's click() has never been reliable in Firefox.
export async function deliverLaDownload(name: string, blob: Blob): Promise<boolean> {
  if (get(nativeShell)) {
    try {
      const r = await api.nativeSaveBinary(name, await blobToBase64(blob))
      if (!r.cancelled) showToast(tr('artifacts.saved'))
      return !r.cancelled
    } catch {
      showToast(tr('artifacts.save_failed'), 'error')
      return false
    }
  }
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  return true
}

// ── iframe-side script ──────────────────────────────────────────────────────
//
// Injected alongside the storage shim (withLaBridge). Fire-and-forget: the
// host never replies to a download (a save dialog can sit open for minutes,
// and the outcome is shown as a host toast), so the request carries id 0 and
// the storage shim's reply listener has no pending entry to confuse it with.

// Embed a string as a JS literal that is also inert inside <script> in HTML.
function jsLiteral(s: string): string {
  return JSON.stringify(s).replace(/</g, '\\u003c').replace(/>/g, '\\u003e')
}

export function buildLaDownloadScript(ns: string): string {
  return `(function(){
  if (window.parent === window) return; // not framed — native downloads work here
  var NS = ${jsLiteral(ns)};
  function send(name, blob){
    try { window.parent.postMessage({ __laBridge: 1, id: 0, ns: NS, op: 'download', name: name, blob: blob }, '*'); }
    catch (e) { console.warn('[octo] download bridge failed', e); }
  }
  // Resolve the anchor's target to bytes and hand them to the host. Returns
  // false when the anchor has nothing to download, so the caller leaves the
  // event alone.
  function save(a){
    if (!a.getAttribute('href')) return false;
    var name = a.getAttribute('download') || '';
    fetch(a.href).then(function(r){ return r.blob(); })
      .then(function(b){ send(name, b); })
      .catch(function(e){ console.warn('[octo] download failed', e); });
    return true;
  }
  document.addEventListener('click', function(ev){
    if (ev.defaultPrevented) return;
    var t = ev.target;
    var a = t && t.closest ? t.closest('a[download]') : null;
    if (a && save(a)) ev.preventDefault();
  });
  var origClick = HTMLAnchorElement.prototype.click;
  HTMLAnchorElement.prototype.click = function(){
    if (!this.isConnected && this.hasAttribute('download') && save(this)) return;
    return origClick.apply(this, arguments);
  };
})();`
}

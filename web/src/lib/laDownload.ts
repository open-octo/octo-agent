// Light App download bridge — the host half.
//
// A Light App renders as a real page (/_apps/<slug>/), so in a browser
// `<a download>` is a plain download and needs no help. The desktop webview is
// the exception: the octo-served page has no download delegate at all, so a
// download there is a silent no-op (see internal/server/native_handlers.go,
// SaveFile). For that case the script the server splices into the app
// (internal/server/page_shim.js) turns on a half that intercepts the standard idiom — an anchor with a `download`
// attribute pointing at a blob:/data: URL — reads the bytes into a Blob and
// posts it here, where it is saved the way the artifact panel's Download button
// saves: the OS save dialog through /api/native/save-file.
//
// Light Apps keep writing the textbook pattern
//   const a = document.createElement('a')
//   a.href = URL.createObjectURL(blob); a.download = 'report.csv'; a.click()
// with no special API. laStorage.ts owns the frame registry and routes the
// `download` message to deliverLaDownload below.

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

// One native save dialog at a time: the browser prompts before letting a page
// download several files in a row, the desktop shell has no such gate and
// would stack a sheet per file. Requests that arrive while one is open are
// dropped, not queued — a loop firing downloads is a bug in the app, not a
// batch the user wants to click through.
let nativeSaveInFlight = false

// Saves a blob the Light App handed over. Resolves to whether a file was
// written (false on a cancelled, failed or dropped native save). The browser
// path mirrors artifact-actions.ts: an in-document anchor, since a detached
// anchor's click() has never been reliable in Firefox. It is kept for a server
// that injects the bridge for every client — the desktop hub is also reachable
// from a plain browser on the same machine.
export async function deliverLaDownload(name: string, blob: Blob): Promise<boolean> {
  if (get(nativeShell)) {
    if (nativeSaveInFlight) return false
    nativeSaveInFlight = true
    try {
      const r = await api.nativeSaveBinary(name, await blobToBase64(blob))
      if (!r.cancelled) showToast(tr('artifacts.saved'))
      return !r.cancelled
    } catch {
      showToast(tr('artifacts.save_failed'), 'error')
      return false
    } finally {
      nativeSaveInFlight = false
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

import { get } from 'svelte/store'
import { nativeShell } from './stores'
import { tr } from './i18n'
import * as api from './api'
import type { Artifact } from './types'

// Shared by ArtifactsPanel and ArtifactModal — both render the same artifact
// actions (copy to clipboard, download file). Kept in one place so clipboard
// behavior (e.g. #1109's .catch fallback) only needs to be adjusted once.
export function copyArtifact(code: string, showToast: (msg: string, type?: string) => void) {
  navigator.clipboard.writeText(code ?? '')
    .then(() => showToast(tr('artifacts.copied')))
    .catch(() => showToast(tr('artifacts.copy_failed'), 'error'))
}

// Saves the artifact to disk. Text kinds carry their whole body in `code`; an
// image carries only its on-disk path there, so it takes the binary path below.
export async function downloadArtifact(
  artifact: Artifact | undefined,
  showToast: (msg: string, type?: string) => void,
) {
  if (!artifact) return
  if (artifact.src) {
    await downloadImage(artifact.name, artifact.src, showToast)
    return
  }
  const fname = artifact.name || 'artifact.txt'
  const content = artifact.code ?? ''
  if (get(nativeShell)) {
    try {
      const r = await api.nativeSaveFile(fname, content)
      if (!r.cancelled) showToast(tr('artifacts.saved'))
    } catch {
      showToast(tr('artifacts.save_failed'), 'error')
    }
    return
  }
  const blob = new Blob([content], { type: 'text/plain' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = fname
  a.click()
  URL.revokeObjectURL(url)
}

// Image artifacts are binary: saving `code` would write a text file holding a
// path. Fetch the bytes instead — and fetch rather than point <a download>
// straight at the endpoint, so a non-2xx surfaces as a toast instead of
// silently doing nothing (#1109). The desktop webview has no download
// delegate, so there the bytes go through the native save dialog
// base64-encoded, same shape as the skill zip export.
async function downloadImage(
  name: string,
  src: string,
  showToast: (msg: string, type?: string) => void,
) {
  const fname = name || 'artifact'
  try {
    const res = await fetch(src)
    if (!res.ok) {
      throw new Error(await api.readErrorMessage(res, `${res.status} ${res.statusText}`))
    }
    if (get(nativeShell)) {
      const bytes = new Uint8Array(await res.arrayBuffer())
      // Chunked so a multi-MB screenshot doesn't blow the spread argument limit.
      let binary = ''
      const chunk = 0x8000
      for (let i = 0; i < bytes.length; i += chunk) {
        binary += String.fromCharCode(...bytes.subarray(i, i + chunk))
      }
      const r = await api.nativeSaveBinary(fname, btoa(binary))
      if (!r.cancelled) showToast(tr('artifacts.saved'))
      return
    }
    const url = URL.createObjectURL(await res.blob())
    const a = document.createElement('a')
    a.href = url
    a.download = fname
    // In the document, not detached: a detached anchor's click() has never been
    // reliable in Firefox. Same shape as the skill zip export.
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (e: any) {
    // With the server's own message — the same story imagePreviewError tells
    // in the preview pane, for whoever reaches the download button first.
    const detail = e?.message ? `: ${e.message}` : ''
    showToast(`${tr('artifacts.save_failed')}${detail}`, 'error')
  }
}

// A failed <img> fires onerror with no reason attached, but the endpoint's
// error body has one — the 10 MB preview cap in particular (#1896). Re-request
// the src to read it; the handler rejects such requests from file metadata
// alone, before serving any bytes, so the retry is cheap exactly when it
// matters. Returns '' when the refetch has no better story to tell (network
// down, or the bytes arrived but failed to decode as an image).
export async function imagePreviewError(src: string): Promise<string> {
  try {
    const res = await fetch(src)
    if (res.ok) return ''
    return await api.readErrorMessage(res, `${res.status} ${res.statusText}`)
  } catch {
    return ''
  }
}

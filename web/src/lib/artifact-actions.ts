import { get } from 'svelte/store'
import { nativeShell } from './stores'
import { tr } from './i18n'
import * as api from './api'

// Shared by ArtifactsPanel and ArtifactModal — both render the same artifact
// actions (copy to clipboard, download file). Kept in one place so clipboard
// behavior (e.g. #1109's .catch fallback) only needs to be adjusted once.
export function copyArtifact(code: string, showToast: (msg: string, type?: string) => void) {
  navigator.clipboard.writeText(code ?? '')
    .then(() => showToast(tr('artifacts.copied')))
    .catch(() => showToast(tr('artifacts.copy_failed'), 'error'))
}

export async function downloadArtifact(
  name: string | undefined,
  code: string,
  showToast: (msg: string, type?: string) => void,
) {
  const fname = name || 'artifact.txt'
  const content = code ?? ''
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

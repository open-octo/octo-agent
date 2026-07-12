import { get } from 'svelte/store'
import { nativeShell } from './stores'
import * as api from './api'

// Open an external URL, choosing the path that actually works for the current
// client: the desktop shell has no place to open a new browser window, so it
// goes through the native bridge; a real browser (local or remote) just uses
// window.open. Use this for any programmatic "open this URL" — anchor clicks go
// through the interceptor below instead.
export function openUrl(url: string) {
  if (get(nativeShell)) {
    api.openExternal(url).catch(() => window.open(url, '_blank', 'noopener'))
  } else {
    window.open(url, '_blank', 'noopener')
  }
}

// In the desktop shell the page is served by octo's own server, not Wails'
// asset server, so the Wails runtime never loads and the webview has no
// place to open a `target="_blank"` link — clicking one does nothing. Route
// http(s) anchor clicks through the native bridge (the same /api/native/
// open-external path the update badge uses) so links reach the system
// browser. In a real browser this stays inert and `target="_blank"` works
// as usual; the check is per-click so it self-corrects once nativeShell is
// known.
export function installExternalLinkInterceptor(): () => void {
  function onClick(e: MouseEvent) {
    if (e.defaultPrevented || e.button !== 0) return
    if (!get(nativeShell)) return
    const anchor = (e.target as HTMLElement | null)?.closest<HTMLAnchorElement>('a[href]')
    if (!anchor) return
    const href = anchor.getAttribute('href') ?? ''
    // Only user-facing external schemes go through the bridge (the server
    // accepts exactly these); in-app hash routes keep their default behaviour.
    if (!/^(https?|mailto|tel):/i.test(href)) return
    e.preventDefault()
    openUrl(href)
  }
  document.addEventListener('click', onClick)
  return () => document.removeEventListener('click', onClick)
}

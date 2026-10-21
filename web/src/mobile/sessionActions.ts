// The session action sheet a long press on a card opens. The open target is a
// store so every list that renders cards (feed, project page, history) shares
// one sheet mounted by MobileApp.
import { get, writable } from 'svelte/store'
import { activeSessionId, pinnedSessions, sessions, showToast } from '../lib/stores'
import * as api from '../lib/api'
import { tr } from '../lib/i18n'
import { applyToolToggle, buildExportMarkdown, hasRenderableTurn } from '../lib/exportTranscript'

/** Id of the session whose action sheet is open, or null. */
export const sessionActionsFor = writable<string | null>(null)

// Pin or unpin a session. Optimistic, as in the desktop sidebar: the card moves
// at once, then the registry write follows; on failure, revert. Pinning
// appends to the end (most-recently pinned last), matching the desktop order.
export async function togglePin(sessionId: string, pin: boolean): Promise<void> {
  const before = get(pinnedSessions)
  pinnedSessions.set(pin
    ? [...before.filter(id => id !== sessionId), sessionId]
    : before.filter(id => id !== sessionId))
  try {
    await api.setSessionPinned(sessionId, pin)
  } catch {
    pinnedSessions.set(before)
    showToast(tr('sidebar.pin_failed'), 'error')
  }
}

// Rename a session; the card picks the new name up from the sessions store.
// A blank name is a no-op, as in the desktop sidebar.
export async function renameSession(sessionId: string, name: string): Promise<void> {
  const draft = name.trim()
  if (!draft) return
  try {
    await api.updateSession(sessionId, { name: draft })
    sessions.update(ss => ss.map(s => (s.id === sessionId ? { ...s, name: draft, title: draft } : s)))
  } catch (e: any) {
    showToast(e?.message ?? String(e), 'error')
  }
}

// The session as Markdown — the desktop export's format, tool calls left out
// as its default is. Rejects when there is nothing worth exporting.
async function sessionMarkdown(sessionId: string, title: string): Promise<string> {
  const data = await api.getSessionMessages(sessionId) as { events?: any[] }
  const { events } = applyToolToggle(data.events ?? [], false)
  if (!hasRenderableTurn(events)) throw new Error(tr('chat.nothing_to_export'))
  return buildExportMarkdown(events, title)
}

// Copy the session as Markdown: the phone's export. The app's WebView can't
// download a file, but it can fill the clipboard. The fetch runs inside the
// clipboard write (a ClipboardItem holding a promise) so iOS, which only
// allows a write during the tap, still accepts it once the text resolves.
// If that write is refused, writeText gets one more try: WebViews that don't
// tie the clipboard to the gesture (Android) can still take it then.
export async function copySessionMarkdown(sessionId: string, title: string): Promise<void> {
  const text = sessionMarkdown(sessionId, title)
  try {
    let written = false
    if (typeof ClipboardItem !== 'undefined' && navigator.clipboard?.write) {
      const blob = text.then(t => new Blob([t], { type: 'text/plain' }))
      // write() may refuse before reading the item; don't leave this unhandled.
      blob.catch(() => {})
      written = await navigator.clipboard.write([new ClipboardItem({ 'text/plain': blob })]).then(() => true, () => false)
    }
    if (!written) await navigator.clipboard.writeText(await text)
    showToast(tr('chat.copied'), 'success')
  } catch (e: any) {
    // A failed fetch or an empty session carries its own message; a refused
    // clipboard surfaces as a DOMException, reported plainly.
    const own = await text.then(() => null, (err: any) => err?.message ?? String(err))
    showToast(own ?? tr('tools.copy_failed'), 'error')
  }
}

// Delete a session for good (the caller has already confirmed).
export async function deleteSession(sessionId: string): Promise<void> {
  try {
    await api.deleteSession(sessionId)
    sessions.update(ss => ss.filter(s => s.id !== sessionId))
    if (get(activeSessionId) === sessionId) activeSessionId.set(null)
  } catch (e: any) {
    showToast(e?.message ?? String(e), 'error')
  }
}

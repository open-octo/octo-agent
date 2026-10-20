// The session action sheet a long press on a card opens. The open target is a
// store so every list that renders cards (feed, project page, history) shares
// one sheet mounted by MobileApp.
import { get, writable } from 'svelte/store'
import { pinnedSessions, showToast } from '../lib/stores'
import * as api from '../lib/api'
import { tr } from '../lib/i18n'

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

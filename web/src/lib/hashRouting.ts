// Hash-route helpers shared by App.svelte's routing. The desktop shell
// (cmd/octo-desktop) navigates the frontend via location.hash, and the app
// reflects its current view there so a refresh lands back where the user was —
// both directions must agree on the exact hash shape.
export function normalizeHash(view: string, sid: string | null): string {
  return view === 'chat' ? (sid ? `#/chat/${encodeURIComponent(sid)}` : '#/chat') : `#/${view}`
}

// Whether a boot-time hash already names what the chat pane should show, so
// the "open the most recent session" fallback must keep out of it. "#/chat" is
// the new-session landing page and "#/chat/{id}" names one session — both are
// the user's own choice, carried across a refresh. An absent or non-chat hash
// names nothing, and the fallback fills it.
export function hashPicksChatTarget(hash: string): boolean {
  const h = hash.replace(/^#\/?/, '')
  return h === 'chat' || h.startsWith('chat/')
}

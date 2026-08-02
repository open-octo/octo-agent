// Hash-route helpers shared by App.svelte's routing. The desktop shell
// (cmd/octo-desktop) navigates the frontend via location.hash, and the app
// reflects its current view there so a refresh lands back where the user was —
// both directions must agree on the exact hash shape.
export function normalizeHash(view: string, sid: string | null): string {
  return view === 'chat' ? (sid ? `#/chat/${encodeURIComponent(sid)}` : '#/chat') : `#/${view}`
}

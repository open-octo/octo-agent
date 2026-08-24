// Sidebar width bounds and their persistence. Lives in lib/ rather than inside
// Sidebar.svelte so the parsing rules can be tested directly (same reason
// sidebarSections.ts sits here).

// Matches the artifacts panel's own PANEL_MIN (ArtifactsPanel.svelte) — the
// two resizable columns share one minimum so neither reads as more
// squeezable than the other. 200 used to clip the top-bar search button in
// the desktop shell, where .side-header's fixed-width children (toolbar
// buttons, logo, brand name, traffic-light padding) already add up to more
// than 200px before the flexible spacer even collapses.
export const SIDEBAR_MIN = 320
export const SIDEBAR_MAX = 480
export const SIDEBAR_DEFAULT = 256

// The narrowest the conversation column may become. Both resizable columns
// squeeze it, so the number lives in one place: the sidebar's grip and the
// artifacts panel's grip each cap themselves against it, and App.svelte's
// own min-width falls back on it too. 640, not some smaller round number —
// below that the composer's bottom toolbar (model/reasoning/context chips)
// runs out of room and wraps to a second line.
export const CENTER_MIN = 640

const SIDEBAR_WIDTH_KEY = 'octo.sidebarWidth'

export function clampSidebarWidth(w: number): number {
  return Math.max(SIDEBAR_MIN, Math.min(SIDEBAR_MAX, w))
}

// A width stored under different bounds is clamped rather than discarded: the
// user asked for "as wide as possible", so give them the new maximum.
export function parseSidebarWidth(raw: string | null): number {
  const v = Number(raw)
  return raw !== null && raw !== '' && Number.isFinite(v) && v > 0 ? clampSidebarWidth(v) : SIDEBAR_DEFAULT
}

export function readSidebarWidth(): number {
  try {
    return parseSidebarWidth(localStorage.getItem(SIDEBAR_WIDTH_KEY))
  } catch {
    // Storage access itself can throw (privacy mode). The sidebar is always
    // mounted, so letting this escape would take the whole app tree down.
    return SIDEBAR_DEFAULT
  }
}

export function saveSidebarWidth(w: number): void {
  try {
    localStorage.setItem(SIDEBAR_WIDTH_KEY, String(Math.round(w)))
  } catch {
    // Same privacy-mode case: the width still applies to this session.
  }
}

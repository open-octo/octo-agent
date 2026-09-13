import { get, writable } from 'svelte/store'
import { nativeShell, macosMajor, isDesktopShell } from './stores'
import { nativeToggleMaximise, nativeWindowState } from './api'

// Desktop shell only. Maximise state has to be shared rather than owned by one
// component: the main header draws the □/❐ icon, but every draggable titlebar
// region — that header and the sidebar's own — accepts the double-click that
// flips it, so a toggle from either has to move the icon.
export const isMaximised = writable(false)

// Guards against a stale focus refresh landing after a fresh toggle and
// overwriting its result.
let stateSeq = 0

export async function refreshMaximised(): Promise<void> {
  const seq = ++stateSeq
  const m = await nativeWindowState()
  if (seq === stateSeq) isMaximised.set(m)
}

export async function flipMaximise(): Promise<void> {
  const next = !get(isMaximised)
  try {
    await nativeToggleMaximise()
    isMaximised.set(next)
    ++stateSeq
  } catch {
    // Toggle failed — fetch the real OS state to stay in sync rather than
    // gambling that the old value is still accurate.
    await refreshMaximised()
  }
}

// Double-clicking a draggable titlebar region zooms the window, the way a
// native title bar does. Wails' custom drag region doesn't wire this up, and
// the octo-served page can't call Wails directly, so it goes through the native
// bridge over HTTP. Ignore double-clicks that land on a control.
export function titlebarDblClick(e: MouseEvent): void {
  if (!get(nativeShell)) return
  if ((e.target as HTMLElement).closest('button, a, input, select, textarea')) return
  void flipMaximise()
}

// The mac titlebar rows pad their top or bottom edge to put their content
// axis on the traffic lights' centre (via the --titlebar-pad-* CSS variables
// the .native-lift/.native-inset rules read). The lights' position depends on
// the macOS version AND the window's SDK stamp (AppKit gates window chrome on
// linked-on-or-after), and the desktop build has carried the macOS 26 SDK in
// LC_BUILD_VERSION since v1.16.18. Measured on macOS 26 for this window style
// (hidden titlebar + wails' toolbar): the centre sits 26pt below the window's
// top edge; through macOS 15 it was 20px. The row is pinned at 44px
// border-box, so 4px of bottom padding lands the axis at (44 - 4) / 2 = 20,
// and 8px of top padding lands it at 8 + (44 - 8) / 2 = 26. Unknown host
// version keeps the legacy padding: the fetch that feeds macosMajor failing
// must not misalign every older mac.
export function titlebarPaddingPx(
  native: boolean,
  isMac: boolean,
  macosMajorVersion: number,
): { top: number; bottom: number } {
  if (!native || !isMac) return { top: 0, bottom: 0 }
  return macosMajorVersion >= 26 ? { top: 8, bottom: 0 } : { top: 0, bottom: 4 }
}

// Publishes the padding to every titlebar row at once: the CSS reads
// var(--titlebar-pad-top, 0px) and var(--titlebar-pad-bottom, 4px), whose
// defaults are also the pre-fetch legacy values. main.ts calls this before
// first paint (isDesktopShell and the URL-seeded macosMajor make every input
// synchronous — waiting for /api/version flashed the rows un-inset under the
// lights at startup), and VersionBadge re-runs it once /api/version lands.
// Runs against document rather than a component so the single value reaches
// Header, Sidebar's brand row and the ArtifactsPanel topbars without
// threading a store through each of them.
export function applyTitlebarLift(): void {
  const isMac = typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform)
  const pad = titlebarPaddingPx(isDesktopShell, isMac, get(macosMajor))
  document.documentElement.style.setProperty('--titlebar-pad-top', `${pad.top}px`)
  document.documentElement.style.setProperty('--titlebar-pad-bottom', `${pad.bottom}px`)
}

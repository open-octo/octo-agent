import { get, writable } from 'svelte/store'
import { nativeShell } from './stores'
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

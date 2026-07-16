// Frameless-window drag + edge-resize for the desktop shell, all platforms.
//
// Wails removes the native frame of a frameless window, which also removes
// the OS's own edge-resize handling; both title-bar dragging
// (--wails-draggable regions) and edge resizing are instead detected by the
// Wails JS runtime, which asks the Go side to start the gesture. But octo's
// page is served by octo's own server, not Wails' asset server, so that
// runtime never loads here — only a tiny stub (window._wails.invoke) is
// injected into the webview. Without this module the window can't be moved or
// resized with the mouse at all, on any platform — including macOS: a
// Frameless Wails v3 window has no free native drag there either (Frameless
// only toggles NSWindowStyleMaskFullSizeContentView; nothing reads
// --wails-draggable natively), a prior version of this comment claimed
// otherwise and that was never actually true.
//
// This module replicates the runtime's gesture detection (adapted from
// @wailsio/runtime drag.ts) and drives the window through the injected stub:
// Go handles "wails:drag" / "wails:resize:<edge>" messages natively, no
// runtime required.

import { isDesktopShell } from './stores'

const RESIZE_HANDLE = 5 // px from each window edge that arms a resize
const CORNER_EXTRA = 10 // extra px so corners are easier to hit

const cursorForEdge = {
  'se-resize': 'nwse-resize',
  'sw-resize': 'nesw-resize',
  'nw-resize': 'nwse-resize',
  'ne-resize': 'nesw-resize',
  'w-resize': 'ew-resize',
  'n-resize': 'ns-resize',
  's-resize': 'ns-resize',
  'e-resize': 'ew-resize',
} as const

type Edge = keyof typeof cursorForEdge

let canDrag = false
let dragging = false
let canResize = false
let resizing = false
let resizeEdge: Edge | '' = ''
let defaultCursor = 'auto'
let buttons = 0

const isWindows = /Win/.test(navigator.platform)

// The stub is injected by Wails after navigation completes, i.e. after this
// module initialises — look it up lazily at gesture time, never at init.
function invoke(msg: string): void {
  ;(window as any)._wails?.invoke?.(msg)
}

function setResize(edge?: Edge): void {
  if (edge) {
    if (!resizeEdge) defaultCursor = document.body.style.cursor
    document.body.style.cursor = cursorForEdge[edge]
  } else if (resizeEdge) {
    document.body.style.cursor = defaultCursor
  }
  resizeEdge = edge || ''
}

function suppressEvent(event: Event): void {
  // Suppress click/dblclick/contextmenu while a drag or resize is in progress.
  if (dragging || resizing) {
    event.stopImmediatePropagation()
    event.stopPropagation()
    event.preventDefault()
  }
}

function primaryDown(event: MouseEvent): void {
  canDrag = false
  canResize = false

  // Ignore repeated clicks (double-click must not arm a gesture) — Windows
  // reports detail=1 for the presses we care about anyway.
  if (!isWindows && event.type === 'mousedown' && event.button === 0 && event.detail !== 1) {
    return
  }

  if (resizeEdge) {
    // Only arm from a real mousedown: entering the window with the button
    // already held (release detected on move/up) must not start a resize.
    if (event.type !== 'mousedown') return
    canResize = true
    return // never drag from a resize edge
  }

  // --wails-draggable is an inherited custom property, so children of the
  // header (including shadow-DOM hosts like iconify-icon) resolve to their
  // region's value, and the no-drag opt-outs on controls win automatically.
  // Inline-SVG children are Elements whose clientWidth is 0, which would fail
  // the scrollbar check below — climb to the nearest HTML ancestor instead.
  const raw = event.target
  const target =
    raw instanceof HTMLElement ? raw : raw instanceof Element ? raw.parentElement ?? document.body : document.body
  const style = window.getComputedStyle(target)
  // Draggable region, excluding clicks that land on the element's scrollbar.
  canDrag =
    style.getPropertyValue('--wails-draggable').trim() === 'drag' &&
    event.offsetX - parseFloat(style.paddingLeft) < target.clientWidth &&
    event.offsetY - parseFloat(style.paddingTop) < target.clientHeight
}

function primaryUp(): void {
  canDrag = false
  dragging = false
  canResize = false
  resizing = false
}

function onMouseMove(event: MouseEvent): void {
  if (canResize && resizeEdge) {
    resizing = true
    invoke('wails:resize:' + resizeEdge)
  } else if (canDrag) {
    dragging = true
    invoke('wails:drag')
  }
  if (dragging || resizing) {
    canDrag = canResize = false
    return
  }

  // A document-level scrollbar at the window edge consumes mouse events in
  // that strip; shift the effective content edge inward so the resize zone
  // sits just before it. Scrollbars of inner containers that touch the window
  // edge still overlap the zone (their outermost pixels grab a resize) — the
  // upstream Wails runtime has the same limitation.
  const scrollbarWidth = Math.max(0, window.innerWidth - document.documentElement.clientWidth)
  const scrollbarHeight = Math.max(0, window.innerHeight - document.documentElement.clientHeight)
  const rightContentEdge = window.innerWidth - scrollbarWidth
  const bottomContentEdge = window.innerHeight - scrollbarHeight

  const rightBorder = event.clientX < rightContentEdge && rightContentEdge - event.clientX < RESIZE_HANDLE
  const leftBorder = event.clientX < RESIZE_HANDLE
  const topBorder = event.clientY < RESIZE_HANDLE
  const bottomBorder = event.clientY < bottomContentEdge && bottomContentEdge - event.clientY < RESIZE_HANDLE

  const rightCorner = event.clientX < rightContentEdge && rightContentEdge - event.clientX < RESIZE_HANDLE + CORNER_EXTRA
  const leftCorner = event.clientX < RESIZE_HANDLE + CORNER_EXTRA
  const topCorner = event.clientY < RESIZE_HANDLE + CORNER_EXTRA
  const bottomCorner = event.clientY < bottomContentEdge && bottomContentEdge - event.clientY < RESIZE_HANDLE + CORNER_EXTRA

  if (!leftCorner && !topCorner && !bottomCorner && !rightCorner) setResize()
  else if (rightCorner && bottomCorner) setResize('se-resize')
  else if (leftCorner && bottomCorner) setResize('sw-resize')
  else if (leftCorner && topCorner) setResize('nw-resize')
  else if (topCorner && rightCorner) setResize('ne-resize')
  else if (leftBorder) setResize('w-resize')
  else if (topBorder) setResize('n-resize')
  else if (bottomBorder) setResize('s-resize')
  else if (rightBorder) setResize('e-resize')
  else setResize()
}

function update(event: MouseEvent): void {
  // Once a native drag/resize starts, the OS swallows the matching mouseup, so
  // gesture end can't be observed directly. Diff event.buttons against the last
  // seen state on every event to detect the release instead.
  const eventButtons = event.buttons
  let released = buttons & ~eventButtons
  let pressed = eventButtons & ~buttons
  buttons = eventButtons

  // A press of a button we already believed down implies we missed its release.
  if (event.type === 'mousedown' && !(pressed & (1 << event.button))) {
    released |= 1 << event.button
    pressed |= 1 << event.button
  }

  // Suppress button events during an active gesture (a plain mousemove or the
  // primary-button mouseup that ends a drag passes through).
  if (
    (event.type !== 'mousemove' && resizing) ||
    (dragging && (event.type === 'mousedown' || event.button !== 0))
  ) {
    event.stopImmediatePropagation()
    event.stopPropagation()
    event.preventDefault()
  }

  if (released & 1) primaryUp()
  if (pressed & 1) primaryDown(event)
  if (event.type === 'mousemove') onMouseMove(event)
}

// Installs the handlers. Gated on the desktop-shell URL marker. Safe to call
// unconditionally.
export function initFramelessDrag(): void {
  if (!isDesktopShell) return

  window.addEventListener('mousedown', update, { capture: true })
  window.addEventListener('mousemove', update, { capture: true })
  window.addEventListener('mouseup', update, { capture: true })
  for (const ev of ['click', 'contextmenu', 'dblclick']) {
    window.addEventListener(ev, suppressEvent, { capture: true })
  }
}

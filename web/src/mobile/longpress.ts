// use:longpress={fn} — fire fn on a press held LONG_PRESS_MS without moving,
// or on contextmenu (Android's WebView fires it for a long press; iOS's fires
// nothing on touch, hence the timer). The same rule as SessionCard's.
const LONG_PRESS_MS = 500
const MOVE_TOLERANCE = 10

export function longpress(node: HTMLElement, fn: () => void) {
  let handler = fn
  let timer: ReturnType<typeof setTimeout> | undefined
  let startX = 0
  let startY = 0
  let fired = false

  const fire = () => {
    clearTimeout(timer)
    if (fired) return
    fired = true
    handler()
  }
  const down = (e: PointerEvent) => {
    fired = false
    startX = e.clientX
    startY = e.clientY
    clearTimeout(timer)
    timer = setTimeout(fire, LONG_PRESS_MS)
  }
  const move = (e: PointerEvent) => {
    if (Math.abs(e.clientX - startX) > MOVE_TOLERANCE || Math.abs(e.clientY - startY) > MOVE_TOLERANCE) clearTimeout(timer)
  }
  // The gesture ends: a later contextmenu (keyboard menu key) may fire anew.
  const end = () => {
    clearTimeout(timer)
    fired = false
  }
  const cancel = () => clearTimeout(timer)
  // Android fires contextmenu during the same long press the timer caught;
  // fire() ignores it then, so one press opens once.
  const menu = (e: MouseEvent) => {
    e.preventDefault()
    fire()
  }

  node.addEventListener('pointerdown', down)
  node.addEventListener('pointermove', move)
  node.addEventListener('pointerup', end)
  node.addEventListener('pointercancel', end)
  node.addEventListener('pointerleave', cancel)
  node.addEventListener('contextmenu', menu)
  return {
    update(next: () => void) { handler = next },
    destroy() {
      clearTimeout(timer)
      node.removeEventListener('pointerdown', down)
      node.removeEventListener('pointermove', move)
      node.removeEventListener('pointerup', end)
      node.removeEventListener('pointercancel', end)
      node.removeEventListener('pointerleave', cancel)
      node.removeEventListener('contextmenu', menu)
    },
  }
}

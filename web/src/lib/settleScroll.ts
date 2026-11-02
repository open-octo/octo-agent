// Transcript rows use `content-visibility: auto`, so a row that has never been
// on screen is laid out at a placeholder height. A jump to a far message is
// therefore computed against estimates and lands off target; only once the
// scroll has ended do the rows around the target take their real heights.
// settleScroll waits for that, then nudges the pane frame by frame until
// `offset()` (how far the target is from where it should be) is ~0.
//
// It stops at the first sign of the user scrolling themselves, so it never
// fights them, and gives up after a bounded number of frames — a target near
// the bottom may be unreachable, and keeping on would pin the pane there. It
// also stops once the target leaves the DOM: switching sessions reuses the
// same pane, and a detached target measures at 0, which would keep dragging
// the new transcript away from the bottom it was just pinned to.

const SETTLE_FRAMES = 30
const SCROLLEND_FALLBACK_MS = 1000

export function settleScroll(sc: HTMLElement, target: Element, offset: () => number): () => void {
  let done = false
  let raf = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  const userEvents = ['wheel', 'touchstart', 'pointerdown', 'keydown'] as const

  const stop = () => {
    if (done) return
    done = true
    if (raf) cancelAnimationFrame(raf)
    if (timer) clearTimeout(timer)
    sc.removeEventListener('scrollend', begin)
    for (const t of userEvents) sc.removeEventListener(t, stop)
  }

  let frames = 0
  const nudge = () => {
    raf = 0
    if (done) return
    if (!sc.isConnected || !target.isConnected) return stop()
    const d = offset()
    if (Math.abs(d) >= 2) sc.scrollTop += d
    if (++frames >= SETTLE_FRAMES) stop()
    else raf = requestAnimationFrame(nudge)
  }

  // Runs once: on scrollend, or after the fallback for browsers without it
  // (and for a jump that needed no scrolling, which fires no scrollend).
  function begin() {
    if (done || frames > 0 || raf) return
    sc.removeEventListener('scrollend', begin)
    if (timer) clearTimeout(timer)
    raf = requestAnimationFrame(nudge)
  }

  sc.addEventListener('scrollend', begin, { once: true })
  timer = setTimeout(begin, SCROLLEND_FALLBACK_MS)
  for (const t of userEvents) sc.addEventListener(t, stop, { passive: true })
  return stop
}

// Liveness heartbeat for the desktop-shell webview. The shell can't see into
// the page (octo-served, no Wails runtime), so the page reports its own health:
// the beat arriving proves JS is running, and the reported requestAnimationFrame
// age proves the render pipeline is producing frames — a webview whose
// compositor went black keeps running JS while its rAF stalls, and that split is
// what the shell's revive logic keys on (cmd/octo-desktop/bridge.go).
import { isDesktopShell } from './stores'
import { request } from './api'

// Must match heartbeatInterval in cmd/octo-desktop/bridge.go: the shell's
// post-show probe waits a few of these, so a page that came back is certain to
// have reported in before any verdict.
const BEAT_MS = 5000

// Frame age reported when the page is hidden, or has not yet observed a frame:
// it owes no frames then, and the shell must not read the absence as a black
// window. Negative by contract — see Heartbeat in bridge.go.
const NO_FRAMES = -1

// startNativeHeartbeat begins beating and returns a stop function. No-op (and
// zero traffic) outside the desktop shell — the endpoint only exists there.
export function startNativeHeartbeat(): () => void {
  if (!isDesktopShell) return () => {}

  // One rAF is requested per beat rather than running a continuous loop: the
  // signal is identical (did a frame land since we asked?) at 0.2 requests per
  // second instead of one per vsync, which matters for an app that stays open
  // all day. A hidden or black window never runs the callback, so lastFrame
  // simply stays behind — exactly the evidence the shell wants.
  // null rather than 0 for "no frame seen yet": performance.now() can legally
  // be 0, and conflating the two would report a real frame as no-frames.
  let lastFrame: number | null = null
  let pendingRaf = 0
  const sampleFrame = () => {
    if (pendingRaf) cancelAnimationFrame(pendingRaf)
    pendingRaf = requestAnimationFrame(() => {
      pendingRaf = 0
      lastFrame = performance.now()
    })
  }

  const beat = () => {
    // document.hidden and "no frame seen yet" both mean the same thing to the
    // shell: don't expect pixels from this beat.
    const frameAge = document.hidden || lastFrame === null
      ? NO_FRAMES
      : Math.max(0, Math.round(performance.now() - lastFrame))
    void request<{ ok: boolean }>('/api/native/heartbeat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ frame_age_ms: frameAge }),
    }).catch(() => {
      // Best-effort: a failed beat reads as silence, and silence after a show
      // is what the shell already treats as "needs a new window".
    })
    sampleFrame() // ask for the frame the NEXT beat will report on
  }

  // Focus and becoming-visible beat immediately: the shell shows the window and
  // wants prompt evidence, and this is also when a frame becomes possible again.
  const onVisible = () => {
    if (!document.hidden) beat()
  }
  const timer = setInterval(beat, BEAT_MS)
  window.addEventListener('focus', beat)
  document.addEventListener('visibilitychange', onVisible)
  beat()

  return () => {
    clearInterval(timer)
    if (pendingRaf) cancelAnimationFrame(pendingRaf)
    window.removeEventListener('focus', beat)
    document.removeEventListener('visibilitychange', onVisible)
  }
}

// Liveness heartbeat for the desktop-shell webview. The shell can't see into
// the page (octo-served, no Wails runtime), so the page reports its own health:
// the beat arriving proves JS is running, and the reported requestAnimationFrame
// age proves the render pipeline is producing frames — a webview whose
// compositor went black keeps running JS while its rAF stalls, and that split
// is exactly what the shell's revive logic keys on (cmd/octo-desktop/bridge.go).
import { isDesktopShell } from './stores'
import { request } from './api'

// Must match heartbeatInterval in cmd/octo-desktop/bridge.go: the shell's
// post-show probe waits longer than this so a healthy page is guaranteed a
// scheduled beat inside the probe window.
const BEAT_MS = 5000

// startNativeHeartbeat begins beating and returns a stop function. No-op (and
// zero traffic) outside the desktop shell — the endpoint only exists there.
export function startNativeHeartbeat(): () => void {
  if (!isDesktopShell) return () => {}

  // rAF only fires while the page is visible, so lastRaf freezing is the
  // expected state for a hidden window; the shell compares frame times to its
  // own Show timestamp, never to wall-clock staleness alone.
  let lastRaf = performance.now()
  let rafId = requestAnimationFrame(function tick(t: number) {
    lastRaf = t
    rafId = requestAnimationFrame(tick)
  })

  const beat = () => {
    void request<{ ok: boolean }>('/api/native/heartbeat', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ frame_age_ms: Math.max(0, Math.round(performance.now() - lastRaf)) }),
    }).catch(() => {
      // Best-effort: a failed beat reads as silence and at worst triggers a
      // revive — which is the right outcome if the hub is unreachable too.
    })
  }
  // Focus and becoming-visible beat immediately: the shell shows the window
  // and expects prompt evidence of life without waiting a full interval.
  const onVisible = () => {
    if (!document.hidden) beat()
  }
  const timer = setInterval(beat, BEAT_MS)
  window.addEventListener('focus', beat)
  document.addEventListener('visibilitychange', onVisible)
  beat()

  return () => {
    clearInterval(timer)
    cancelAnimationFrame(rafId)
    window.removeEventListener('focus', beat)
    document.removeEventListener('visibilitychange', onVisible)
  }
}

// Relative timestamps for session lists, in the coarse buckets a list needs:
// nobody scanning a sidebar cares that a session was last touched 3 minutes and
// 40 seconds ago, only that it was minutes rather than days.
//
// Takes the reactive `$t` rather than importing `tr`, so a locale flip re-renders
// the timestamps — config-driven setLocale lands after the first session_list,
// which would otherwise leave every row in the boot locale.

import { readable } from 'svelte/store'

const TICK_MS = 30_000

// Every one of these strings goes stale on its own: a sidebar left open all
// afternoon keeps rendering whatever the last list fetch produced, so a row
// that really is four hours old still reads "just now". `$clockTick` is the
// missing input — subscribing to it alongside `$t` is what re-runs `ago` as
// time passes. The interval lives inside the store's start function, so it
// only runs while something is actually rendering a timestamp.
export const clockTick = readable(Date.now(), set => {
  const bump = () => set(Date.now())
  // The initial value above is stamped at module load. Re-stamp on subscribe so
  // a list mounted much later — or remounted after the last subscriber left and
  // stopped the interval — starts from now rather than from page-load time.
  bump()
  const id = setInterval(bump, TICK_MS)
  // A hidden tab's timers are throttled to a minute or worse, so the string on
  // screen when a tab is restored can be well behind. Re-stamp on the way back
  // in rather than making the user wait out the next tick.
  const onVisibility = () => { if (!document.hidden) bump() }
  document.addEventListener('visibilitychange', onVisibility)
  return () => {
    clearInterval(id)
    document.removeEventListener('visibilitychange', onVisibility)
  }
})

export function ago(iso: string, tf: (k: string) => string, now: number = Date.now()): string {
  if (!iso) return ''
  const ms = now - new Date(iso).getTime()
  if (Number.isNaN(ms)) return ''
  const m = Math.floor(ms / 60000)
  if (m < 1) return tf('time.just_now')
  if (m < 60) return tf('time.min_ago').replace('{n}', String(m))
  const h = Math.floor(m / 60)
  if (h < 24) return tf('time.hr_ago').replace('{n}', String(h))
  return tf('time.day_ago').replace('{n}', String(Math.floor(h / 24)))
}

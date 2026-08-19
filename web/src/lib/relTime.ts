// Relative timestamps for session lists, in the coarse buckets a list needs:
// nobody scanning a sidebar cares that a session was last touched 3 minutes and
// 40 seconds ago, only that it was minutes rather than days.
//
// Takes the reactive `$t` rather than importing `tr`, so a locale flip re-renders
// the timestamps — config-driven setLocale lands after the first session_list,
// which would otherwise leave every row in the boot locale.

export function ago(iso: string, tf: (k: string) => string): string {
  if (!iso) return ''
  const ms = Date.now() - new Date(iso).getTime()
  if (Number.isNaN(ms)) return ''
  const m = Math.floor(ms / 60000)
  if (m < 1) return tf('time.just_now')
  if (m < 60) return tf('time.min_ago').replace('{n}', String(m))
  const h = Math.floor(m / 60)
  if (h < 24) return tf('time.hr_ago').replace('{n}', String(h))
  return tf('time.day_ago').replace('{n}', String(Math.floor(h / 24)))
}

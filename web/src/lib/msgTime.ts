// Timestamps for the transcript's message rows. A bare HH:MM is enough for a
// message sent today, but a session reopened days later needs the date too —
// otherwise "08:02" could be any morning.

function sameDay(a: Date, b: Date): boolean {
  return a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()
}

// `locale` is only there so tests can pin the output; callers leave it unset
// and get the browser's own format.
export function fmtMsgTime(ts: number, now: number = Date.now(), locale?: string): string {
  const d = new Date(ts)
  const n = new Date(now)
  const time: Intl.DateTimeFormatOptions = { hour: '2-digit', minute: '2-digit' }
  if (sameDay(d, n)) return d.toLocaleTimeString(locale, time)
  const date: Intl.DateTimeFormatOptions = { month: '2-digit', day: '2-digit' }
  if (d.getFullYear() !== n.getFullYear()) date.year = 'numeric'
  return d.toLocaleString(locale, { ...date, ...time })
}

// Full date and time, for the hover title.
export function fmtMsgTimeFull(ts: number, locale?: string): string {
  return new Date(ts).toLocaleString(locale)
}

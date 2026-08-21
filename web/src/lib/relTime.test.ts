import { describe, it, expect, afterEach, vi } from 'vitest'
import { ago, clockTick } from './relTime'

// The real i18n table is irrelevant here — echo the key with its {n} filled so
// a wrong bucket reads as a wrong assertion rather than a translated string.
const tf = (k: string) => k
const tfn = (k: string) => `${k}:{n}`

const T0 = Date.parse('2026-08-21T12:00:00Z')
const iso = (msBefore: number) => new Date(T0 - msBefore).toISOString()

afterEach(() => {
  vi.useRealTimers()
})

describe('ago', () => {
  it('buckets by the `now` it is given', () => {
    expect(ago(iso(30_000), tf, T0)).toBe('time.just_now')
    expect(ago(iso(5 * 60_000), tfn, T0)).toBe('time.min_ago:5')
    expect(ago(iso(4 * 3600_000), tfn, T0)).toBe('time.hr_ago:4')
    expect(ago(iso(50 * 3600_000), tfn, T0)).toBe('time.day_ago:2')
  })

  it('defaults `now` to the current clock', () => {
    vi.useFakeTimers()
    vi.setSystemTime(T0)
    expect(ago(iso(2 * 3600_000), tfn)).toBe('time.hr_ago:2')
  })

  it('returns nothing for a missing or unparseable stamp', () => {
    expect(ago('', tf, T0)).toBe('')
    expect(ago('not a date', tf, T0)).toBe('')
  })

  // The whole point of the tick: the same row, re-rendered later, moves buckets.
  it('advances the bucket as `now` moves', () => {
    const stamp = iso(0)
    expect(ago(stamp, tf, T0)).toBe('time.just_now')
    expect(ago(stamp, tfn, T0 + 90 * 60_000)).toBe('time.hr_ago:1')
  })
})

describe('clockTick', () => {
  it('re-stamps on an interval while subscribed, and stops after', () => {
    vi.useFakeTimers()
    vi.setSystemTime(T0)

    const seen: number[] = []
    const stop = clockTick.subscribe(v => seen.push(v))
    // The store's declared initial value is stamped at module load — before
    // the fake clock exists — so only the re-stamp on subscribe is fixed.
    expect(seen.at(-1)).toBe(T0)

    vi.advanceTimersByTime(30_000)
    expect(seen.at(-1)).toBe(T0 + 30_000)
    vi.advanceTimersByTime(30_000)
    expect(seen.at(-1)).toBe(T0 + 60_000)

    stop()
    const count = seen.length
    vi.advanceTimersByTime(120_000)
    expect(seen).toHaveLength(count)
  })

  it('re-stamps when a hidden tab comes back, without waiting out the tick', () => {
    vi.useFakeTimers()
    vi.setSystemTime(T0)

    const seen: number[] = []
    const stop = clockTick.subscribe(v => seen.push(v))

    // Stand in for the throttling a background tab gets: time moved on, no
    // interval fired.
    vi.setSystemTime(T0 + 10 * 60_000)
    document.dispatchEvent(new Event('visibilitychange'))
    expect(seen.at(-1)).toBe(T0 + 10 * 60_000)

    stop()
    // The listener goes with the subscription — a later event must not fire it.
    const count = seen.length
    document.dispatchEvent(new Event('visibilitychange'))
    expect(seen).toHaveLength(count)
  })
})

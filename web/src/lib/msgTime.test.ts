import { describe, it, expect } from 'vitest'
import { fmtMsgTime, fmtMsgTimeFull } from './msgTime'

// Local-time constructors keep these independent of the runner's timezone.
const NOW = new Date(2026, 10, 13, 18, 30).getTime()
const at = (y: number, mo: number, d: number, h: number, mi: number) => new Date(y, mo, d, h, mi).getTime()

describe('fmtMsgTime', () => {
  it('shows only the time for a message sent today', () => {
    expect(fmtMsgTime(at(2026, 10, 13, 8, 2), NOW, 'en-GB')).toBe('08:02')
  })

  it('adds the date for an earlier day, even less than 24h ago', () => {
    expect(fmtMsgTime(at(2026, 10, 12, 23, 50), NOW, 'en-GB')).toBe('12/11, 23:50')
  })

  it('adds the year once the message is from another year', () => {
    expect(fmtMsgTime(at(2025, 11, 31, 9, 15), NOW, 'en-GB')).toBe('31/12/2025, 09:15')
  })
})

describe('fmtMsgTimeFull', () => {
  it('carries the full date and time', () => {
    expect(fmtMsgTimeFull(at(2026, 10, 11, 14, 5), 'en-GB')).toBe('11/11/2026, 14:05:00')
  })
})

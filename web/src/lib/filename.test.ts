import { describe, it, expect } from 'vitest'
import { filenameStem } from './filename'

describe('filenameStem', () => {
  it('keeps a CJK title intact', () => {
    // The regression this function exists for: a \w filter collapsed the whole
    // title to a single underscore, so every Chinese session exported as _.json.
    expect(filenameStem('会话导出测试')).toBe('会话导出测试')
  })

  it('keeps other non-Latin scripts and accents', () => {
    expect(filenameStem('Отладка платежей')).toBe('Отладка платежей')
    expect(filenameStem('Rückläufer prüfen')).toBe('Rückläufer prüfen')
    expect(filenameStem('日本語のタイトル')).toBe('日本語のタイトル')
  })

  it('drops characters a filename cannot hold', () => {
    expect(filenameStem('a/b\\c:d*e?f"g<h>i|j')).toBe('abcdefghij')
  })

  it('strips control characters', () => {
    const withControl = 'order' + String.fromCharCode(1) + 'sync'
    expect(filenameStem(withControl)).toBe('ordersync')
  })

  it('collapses whitespace runs and newlines', () => {
    expect(filenameStem('order   sync\nlater')).toBe('order sync later')
  })

  it('drops leading dots so the file is not hidden', () => {
    expect(filenameStem('...hidden')).toBe('hidden')
  })

  it('drops trailing dots and spaces, which Windows discards silently', () => {
    expect(filenameStem('report...')).toBe('report')
    expect(filenameStem('report   ')).toBe('report')
  })

  it('falls back when nothing survives', () => {
    expect(filenameStem('')).toBe('session')
    expect(filenameStem('///')).toBe('session')
    expect(filenameStem('...')).toBe('session')
    expect(filenameStem('   ')).toBe('session')
  })

  it('honours a caller-supplied fallback', () => {
    expect(filenameStem('///', 'transcript')).toBe('transcript')
  })

  it('caps length by code point and leaves no trailing dot at the cut', () => {
    expect(Array.from(filenameStem('汉'.repeat(200)))).toHaveLength(80)
    // 80 chars then a dot: the cut must not leave the name ending in one.
    expect(filenameStem('a'.repeat(80) + '.' + 'b'.repeat(10))).toBe('a'.repeat(80))
  })

  it('never truncates an emoji into a lone surrogate', () => {
    // A rocket is two UTF-16 units, so a unit-based slice(0, 80) would cut the
    // 41st one in half and leave an unpaired surrogate in the filename.
    const rocket = String.fromCodePoint(0x1f680)
    const stem = filenameStem(rocket.repeat(60))
    expect(Array.from(stem)).toHaveLength(60)
    const highOnly = new RegExp('[\\uD800-\\uDBFF](?![\\uDC00-\\uDFFF])')
    const lowOnly = new RegExp('(?<![\\uD800-\\uDBFF])[\\uDC00-\\uDFFF]')
    expect(stem).not.toMatch(highOnly)
    expect(stem).not.toMatch(lowOnly)
  })
})

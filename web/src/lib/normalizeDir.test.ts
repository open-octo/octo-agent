import { describe, it, expect } from 'vitest'
import { normalizeDir } from './stores'

describe('normalizeDir', () => {
  it('leaves a plain path alone', () => {
    expect(normalizeDir('/work/app')).toBe('/work/app')
  })

  it('strips trailing separators', () => {
    expect(normalizeDir('/work/app/')).toBe('/work/app')
    expect(normalizeDir('/work/app///')).toBe('/work/app')
  })

  // Windows paths have no forward slash at all, and a trailing backslash is
  // just as common there.
  it('strips a trailing backslash too', () => {
    expect(normalizeDir('C:\\Users\\x\\app\\')).toBe('C:\\Users\\x\\app')
  })

  it('does not eat a bare root', () => {
    expect(normalizeDir('/')).toBe('')
  })
})

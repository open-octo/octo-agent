import { describe, it, expect } from 'vitest'
import { dirLeaf } from './stores'

// dirLeaf names a directory where a full path will not fit. It replaced three
// near-identical private copies (two of which were added a commit apart), so
// the edge cases they each had to get right independently are pinned here.
describe('dirLeaf', () => {
  it('takes the last segment', () => {
    expect(dirLeaf('/Users/roy/Projects/klook-ceg')).toBe('klook-ceg')
  })

  it('ignores a trailing separator', () => {
    // The registry stores what the picker returned, and both the OS dialog and
    // a hand-typed path can carry one.
    expect(dirLeaf('/Users/roy/Projects/klook-ceg/')).toBe('klook-ceg')
    expect(dirLeaf('/Users/roy/Projects/klook-ceg///')).toBe('klook-ceg')
  })

  it('handles Windows separators', () => {
    expect(dirLeaf('C:\\Users\\roy\\klook-ceg')).toBe('klook-ceg')
    expect(dirLeaf('C:\\Users\\roy\\klook-ceg\\')).toBe('klook-ceg')
  })

  it('passes a bare name through', () => {
    expect(dirLeaf('klook-ceg')).toBe('klook-ceg')
  })

  it('never returns empty for a root or an empty input', () => {
    // An empty label would render a nameless, unclickable-looking row; falling
    // back to the input keeps something on screen.
    expect(dirLeaf('/')).toBe('/')
    expect(dirLeaf('')).toBe('')
  })
})

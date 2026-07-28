import { describe, it, expect } from 'vitest'
import { submitIntent } from './composerKeys'

describe('submitIntent', () => {
  it('plain Enter sends', () => {
    expect(submitIntent({ key: 'Enter' })).toBe('send')
  })

  it('Cmd+Enter queues', () => {
    expect(submitIntent({ key: 'Enter', metaKey: true })).toBe('queue')
  })

  it('Ctrl+Enter queues', () => {
    expect(submitIntent({ key: 'Enter', ctrlKey: true })).toBe('queue')
  })

  // The regression this ordering guards: with the send branch checked first,
  // Cmd/Ctrl+Enter would submit as an ordinary send and never queue.
  it('queue wins over send when a modifier is held', () => {
    expect(submitIntent({ key: 'Enter', metaKey: true, shiftKey: false })).toBe('queue')
    expect(submitIntent({ key: 'Enter', ctrlKey: true, shiftKey: true })).toBe('queue')
  })

  it('Shift+Enter is not a submit (the textarea inserts a newline)', () => {
    expect(submitIntent({ key: 'Enter', shiftKey: true })).toBeNull()
  })

  it('other keys are not submits', () => {
    for (const key of ['a', 'Escape', 'Tab', 'ArrowUp', 'ArrowDown']) {
      expect(submitIntent({ key })).toBeNull()
      expect(submitIntent({ key, metaKey: true })).toBeNull()
    }
  })
})

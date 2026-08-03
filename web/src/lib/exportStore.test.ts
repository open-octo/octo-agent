import { describe, it, expect, beforeEach } from 'vitest'
import { exportModeStore, selectedMessagesStore } from './exportStore'
import { get } from 'svelte/store'

beforeEach(() => {
  exportModeStore.exit('s1')
  exportModeStore.exit('s2')
  selectedMessagesStore.clear('s1')
  selectedMessagesStore.clear('s2')
})

describe('exportModeStore', () => {
  it('starts with no session in export mode', () => {
    expect(get(exportModeStore)['s1'] ?? false).toBe(false)
    expect(get(exportModeStore)['s2'] ?? false).toBe(false)
  })

  it('enter() sets export mode for a session', () => {
    exportModeStore.enter('s1')
    expect(get(exportModeStore)['s1']).toBe(true)
  })

  it('exit() clears export mode for a session', () => {
    exportModeStore.enter('s1')
    exportModeStore.exit('s1')
    expect(get(exportModeStore)['s1']).toBe(false)
  })

  it('multiple sessions can be in export mode independently', () => {
    exportModeStore.enter('s1')
    exportModeStore.enter('s2')
    expect(get(exportModeStore)['s1']).toBe(true)
    expect(get(exportModeStore)['s2']).toBe(true)
    exportModeStore.exit('s1')
    expect(get(exportModeStore)['s1']).toBe(false)
    expect(get(exportModeStore)['s2']).toBe(true)
  })
})

describe('selectedMessagesStore', () => {
  it('starts with no selections', () => {
    expect(get(selectedMessagesStore)).toEqual({})
  })

  it('initForSession() populates a Set from an ID array', () => {
    selectedMessagesStore.initForSession('s1', ['a', 'b', 'c'])
    const sel = get(selectedMessagesStore)['s1']
    expect(sel.has('a')).toBe(true)
    expect(sel.has('b')).toBe(true)
    expect(sel.has('c')).toBe(true)
    expect(sel.size).toBe(3)
  })

  it('toggle() adds an unselected message', () => {
    selectedMessagesStore.initForSession('s1', ['a'])
    selectedMessagesStore.toggle('s1', 'b')
    const sel = get(selectedMessagesStore)['s1']
    expect(sel.has('a')).toBe(true)
    expect(sel.has('b')).toBe(true)
  })

  it('toggle() removes a selected message', () => {
    selectedMessagesStore.initForSession('s1', ['a', 'b'])
    selectedMessagesStore.toggle('s1', 'a')
    const sel = get(selectedMessagesStore)['s1']
    expect(sel.has('a')).toBe(false)
    expect(sel.has('b')).toBe(true)
  })

  it('getForSession() returns an empty Set for unknown sessions', () => {
    const sel = selectedMessagesStore.getForSession('unknown')
    expect(sel.size).toBe(0)
  })

  it('getForSession() returns the current selection', () => {
    selectedMessagesStore.initForSession('s1', ['x', 'y'])
    const sel = selectedMessagesStore.getForSession('s1')
    expect(sel.has('x')).toBe(true)
    expect(sel.has('y')).toBe(true)
    expect(sel.size).toBe(2)
  })

  it('clear() removes the session from the store', () => {
    selectedMessagesStore.initForSession('s1', ['a'])
    selectedMessagesStore.clear('s1')
    expect(get(selectedMessagesStore)['s1']).toBeUndefined()
  })
})

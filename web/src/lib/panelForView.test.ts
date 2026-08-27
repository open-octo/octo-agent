import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'
import { activeSessionId, lightappOpen, panelContent, panelForView, savePanelMode, togglePanel, view } from './stores'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts), and the
// panel-mode helpers persist through it.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

beforeEach(() => {
  localStorage.clear()
  view.set('chat')
  activeSessionId.set('s1')
  lightappOpen.set([])
  panelContent.set(null)
})

describe('panelForView', () => {
  it('gives the chat view its remembered mode', () => {
    expect(panelForView('chat', [], 's1')).toBe('session')
    savePanelMode('diff')
    expect(panelForView('chat', [], 's1')).toBe('diff')
  })

  it('offers nothing on the blank chat landing, which has no session to read', () => {
    expect(panelForView('chat', [], null)).toBe(null)
    savePanelMode('diff')
    expect(panelForView('chat', [], null)).toBe(null)
  })

  it('gives the Light Apps view its own mode, but only with a tab open', () => {
    expect(panelForView('lightapps', [], null)).toBe(null)
    expect(panelForView('lightapps', ['clock'], null)).toBe('lightapps')
  })

  it('offers nothing to views that own no panel content', () => {
    for (const v of ['tasks', 'skills', 'agents', 'workflows', 'browser', 'mcp', 'channels']) {
      expect(panelForView(v, ['clock'], 's1')).toBe(null)
    }
  })
})

describe('togglePanel', () => {
  it('opens the chat view on its remembered mode', () => {
    savePanelMode('diff')
    togglePanel()
    expect(get(panelContent)).toBe('diff')
  })

  it('leaves the blank chat landing alone', () => {
    activeSessionId.set(null)
    togglePanel()
    expect(get(panelContent)).toBe(null)
  })

  it('never parks the last session artifacts beside another view', () => {
    view.set('lightapps')
    togglePanel()
    expect(get(panelContent)).toBe(null)

    lightappOpen.set(['clock'])
    togglePanel()
    expect(get(panelContent)).toBe('lightapps')
  })

  it('still closes an open panel from any view', () => {
    panelContent.set('lightapps')
    view.set('tasks')
    togglePanel()
    expect(get(panelContent)).toBe(null)
  })
})

import { describe, it, expect } from 'vitest'
import { get } from 'svelte/store'
import { chatMessages, clearMsgs, addChatMsg, historyPrefixLen, setHistoryPrefixLen, getHistoryPos, setHistoryPos } from './stores'

// historyPrefixLen marks how many leading chatMessages entries belong to the
// rendered persisted history; the reconnect resync keeps that prefix on screen
// and only rebuilds the live tail (#2745). These pin the counter semantics the
// swap relies on.
describe('history prefix marker', () => {
  const SID = 'prefix-test'

  it('starts at zero and resets on clearMsgs', () => {
    clearMsgs(SID)
    expect(historyPrefixLen(SID)).toBe(0)
    setHistoryPrefixLen(SID, 3)
    expect(historyPrefixLen(SID)).toBe(3)
    clearMsgs(SID)
    expect(historyPrefixLen(SID)).toBe(0)
  })

  it('live events append after the prefix, so the prefix stays a prefix', () => {
    clearMsgs(SID)
    addChatMsg(SID, { id: 'h1', type: 'user', content: 'from history', streaming: false })
    addChatMsg(SID, { id: 'h2', type: 'assistant', content: 'from history', streaming: false })
    setHistoryPrefixLen(SID, 2)
    addChatMsg(SID, { id: 'l1', type: 'assistant', content: 'live', streaming: true })
    const msgs = get(chatMessages)[SID]
    expect(msgs.slice(0, historyPrefixLen(SID)).map(m => m.id)).toEqual(['h1', 'h2'])
    expect(msgs.slice(historyPrefixLen(SID)).map(m => m.id)).toEqual(['l1'])
  })
})

describe('history position cursor', () => {
  it('is unset until a load caches it', () => {
    expect(getHistoryPos('never-loaded')).toBeNull()
    setHistoryPos('s', { after: 5, epoch: 1 })
    expect(getHistoryPos('s')).toEqual({ after: 5, epoch: 1 })
  })
})

import { describe, it, expect, beforeEach, vi } from 'vitest'
import { get } from 'svelte/store'

const getSessionMessages = vi.fn()
const getSessionGoal = vi.fn()
const getAgentRuns = vi.fn()
vi.mock('../lib/api', () => ({
  getSessionMessages: (id: string, pos?: { after: number; epoch: number }) => getSessionMessages(id, pos),
  getSessionGoal: (id: string) => getSessionGoal(id),
  getAgentRuns: (id: string) => getAgentRuns(id),
}))

import { chatMessages, addChatMsg, clearMsgs, historyPrefixLen, getHistoryPos } from '../lib/stores'
import { loadMobileHistory } from './chatWiring'

// The reconnect resync must not blank the transcript the user is reading
// (#2745): the rendered history prefix stays on screen during the fetch, and
// an incremental (tail-only) response never re-renders it. These tests drive
// loadMobileHistory through both modes.
describe('loadMobileHistory resync', () => {
  const SID = 'resync-test'

  beforeEach(() => {
    getSessionMessages.mockReset()
    getSessionGoal.mockReset().mockRejectedValue(new Error('no goal'))
    getAgentRuns.mockReset().mockRejectedValue(new Error('no runs'))
    clearMsgs(SID)
  })

  // A full initial load, so the session has a rendered history prefix and a
  // cached cursor: two persisted messages.
  async function seedFullLoad() {
    getSessionMessages.mockResolvedValue({
      events: [
        { type: 'history_user_message', content: 'first question', created_at: 1000, message_index: 0 },
        { type: 'assistant_message', content: 'first answer', created_at: 2000, message_index: 1 },
      ],
      epoch: 0,
      history_len: 2,
      incremental: false,
    })
    await loadMobileHistory(SID)
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual(['first question', 'first answer'])
    expect(historyPrefixLen(SID)).toBe(2)
    expect(getHistoryPos(SID)).toEqual({ after: 2, epoch: 0 })
  }

  it('keeps the history prefix on screen during the resync fetch (no blanking)', async () => {
    await seedFullLoad()
    // A stale live tail from before the disconnect: a turn was streaming.
    addChatMsg(SID, { id: 'live', type: 'assistant', content: 'partial', streaming: true })

    let release!: (v: unknown) => void
    getSessionMessages.mockReturnValue(new Promise(r => (release = r)))
    const done = loadMobileHistory(SID, true)

    // While the fetch is in flight the prefix is untouched and the stale live
    // tail is already gone (the subscribe replay rebuilds it).
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual(['first question', 'first answer'])
    // The request carried the cached cursor.
    expect(getSessionMessages).toHaveBeenCalledWith(SID, { after: 2, epoch: 0 })

    release({ events: [], epoch: 0, history_len: 2, incremental: true })
    await done
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual(['first question', 'first answer'])
    expect(historyPrefixLen(SID)).toBe(2)
  })

  it('appends an incremental tail without re-rendering or duplicating the prefix', async () => {
    await seedFullLoad()
    getSessionMessages.mockResolvedValue({
      events: [
        { type: 'history_user_message', content: 'second question', created_at: 3000, message_index: 2 },
        { type: 'assistant_message', content: 'second answer', created_at: 4000, message_index: 3 },
      ],
      epoch: 0,
      history_len: 4,
      incremental: true,
    })
    await loadMobileHistory(SID, true)
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual([
      'first question', 'first answer', 'second question', 'second answer',
    ])
    expect(historyPrefixLen(SID)).toBe(4)
    expect(getHistoryPos(SID)).toEqual({ after: 4, epoch: 0 })
  })

  it('keeps live events that arrive during the resync fetch behind the tail', async () => {
    await seedFullLoad()
    let release!: (v: unknown) => void
    getSessionMessages.mockReturnValue(new Promise(r => (release = r)))
    const done = loadMobileHistory(SID, true)
    // The subscribe replay of a running turn lands while the fetch is out.
    addChatMsg(SID, { id: 'live', type: 'assistant', content: 'streaming reply', streaming: true })
    release({
      events: [{ type: 'assistant_message', content: 'completed while away', created_at: 3000, message_index: 2 }],
      epoch: 0,
      history_len: 3,
      incremental: true,
    })
    await done
    // Order: prefix, the persisted tail, then the still-live replay content.
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual([
      'first question', 'first answer', 'completed while away', 'streaming reply',
    ])
    // The persisted tail joins the prefix; the replayed bubble stays live.
    expect(historyPrefixLen(SID)).toBe(3)
  })

  it('falls back to a full replace when the server reports a moved epoch', async () => {
    await seedFullLoad()
    // While disconnected someone compacted the session: the server answers a
    // full response with the new epoch and the rewritten, shorter history.
    getSessionMessages.mockResolvedValue({
      events: [{ type: 'assistant_message', content: '[summary]', created_at: 5000, message_index: 0 }],
      epoch: 1,
      history_len: 1,
      incremental: false,
    })
    await loadMobileHistory(SID, true)
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual(['[summary]'])
    expect(historyPrefixLen(SID)).toBe(1)
    expect(getHistoryPos(SID)).toEqual({ after: 1, epoch: 1 })
  })

  it('answers an old server (no cursor fields) with a full render and no cached cursor', async () => {
    await seedFullLoad()
    // An old server ignores after/epoch and returns the full transcript
    // without the cursor fields.
    getSessionMessages.mockResolvedValue({
      events: [
        { type: 'history_user_message', content: 'first question', created_at: 1000, message_index: 0 },
        { type: 'assistant_message', content: 'first answer', created_at: 2000, message_index: 1 },
      ],
    })
    await loadMobileHistory(SID, true)
    expect(get(chatMessages)[SID].map(m => m.content)).toEqual(['first question', 'first answer'])
    // Cursor stays at the previously cached value — no new fields to update it.
    expect(getHistoryPos(SID)).toEqual({ after: 2, epoch: 0 })
  })
})

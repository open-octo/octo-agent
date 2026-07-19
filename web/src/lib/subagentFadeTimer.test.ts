import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import type { SubAgentState } from './stores'
import { createSubAgentFadeTimer, SUB_AGENT_DISMISS_MS } from './subagentFadeTimer'

// Mock the store helper so the test asserts the controller's *decision* (when it
// fires) without touching the real chatSubAgents store.
vi.mock('./stores', () => ({
  clearDoneSubAgents: vi.fn(),
}))

import { clearDoneSubAgents } from './stores'

function agent(id: string, status: SubAgentState['status']): SubAgentState {
  return {
    id,
    description: `agent ${id}`,
    agentType: 'general',
    status,
    lastTool: '',
    tools: [],
    startedAt: Date.now(),
  }
}

const SID = 'sess-1'

describe('createSubAgentFadeTimer', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.runOnlyPendingTimers()
    vi.useRealTimers()
  })

  it('fires clearDoneSubAgents once every agent is done', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'done'), agent('b', 'done')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS - 1)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()

    vi.advanceTimersByTime(1)
    expect(clearDoneSubAgents).toHaveBeenCalledOnce()
    expect(clearDoneSubAgents).toHaveBeenCalledWith(SID)
  })

  it('treats error / cancelled as "done" (not running)', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'error'), agent('b', 'cancelled')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS)
    expect(clearDoneSubAgents).toHaveBeenCalledOnce()
  })

  it('does NOT fire while any agent is still running', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'done'), agent('b', 'running')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS * 3)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()
  })

  it('cancels the pending timer when a new agent starts running again', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'done')])

    // A new sub-agent starts before the 2s elapse — the panel should stay.
    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS - 100)
    timer.update(SID, [agent('a', 'done'), agent('b', 'running')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS * 3)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()

    // Now the new one finishes too — a fresh 2s window opens.
    timer.update(SID, [agent('a', 'done'), agent('b', 'done')])
    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS)
    expect(clearDoneSubAgents).toHaveBeenCalledOnce()
  })

  it('does nothing when the agents list is empty', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS * 3)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()
  })

  it('does nothing when sid is empty', () => {
    const timer = createSubAgentFadeTimer()
    timer.update('', [agent('a', 'done')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS * 3)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()
  })

  it('destroy() cancels a pending timer', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'done')])

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS - 100)
    timer.destroy()

    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS * 3)
    expect(clearDoneSubAgents).not.toHaveBeenCalled()
  })

  it('update() after destroy() can schedule a fresh timer', () => {
    const timer = createSubAgentFadeTimer()
    timer.update(SID, [agent('a', 'done')])
    timer.destroy()

    timer.update(SID, [agent('a', 'done'), agent('b', 'done')])
    vi.advanceTimersByTime(SUB_AGENT_DISMISS_MS)
    expect(clearDoneSubAgents).toHaveBeenCalledOnce()
  })
})

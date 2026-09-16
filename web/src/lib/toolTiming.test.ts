import { describe, it, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { chatMessages, addToolCallToGroup, updateToolResult, setToolError, clearMsgs } from './stores'

// endedAt handling in updateToolResult / setToolError: the server-stamped
// event time must win when present (both ends then share the server clock),
// Date.now() is the fallback for older servers, and residual clock skew can
// never produce a negative elapsed.

const SID = 'tool-timing-test'

function lastTool() {
  const msgs = get(chatMessages)[SID] ?? []
  const grp = msgs.findLast((m: any) => m.type === 'tool_group')
  return grp?.tools?.[grp.tools.length - 1]
}

function addTool(startedAt?: number) {
  addToolCallToGroup(SID, {
    id: `t-${Math.random()}`, toolId: 'call_1', name: 'terminal', args: '',
    summary: '', startedAt, done: false, error: null, result: null, stdout: [], diff: null,
  })
}

beforeEach(() => clearMsgs(SID))

describe('updateToolResult endedAt', () => {
  it('derives elapsed from the server-stamped end time when present', () => {
    addTool(10_000)
    updateToolResult(SID, 'call_1', 'ok', null, 12_500)
    expect(lastTool().elapsed).toBe(2.5)
  })

  it('falls back to Date.now() when the event carries no timestamp', () => {
    addTool(Date.now())
    updateToolResult(SID, 'call_1', 'ok', null)
    const e = lastTool().elapsed
    expect(e).toBeGreaterThanOrEqual(0)
    expect(e).toBeLessThan(10)
  })

  it('clamps negative skew to zero instead of showing a negative duration', () => {
    addTool(10_000)
    updateToolResult(SID, 'call_1', 'ok', null, 9_000)
    expect(lastTool().elapsed).toBe(0)
  })

  it('leaves elapsed unset for a tool with no start time (old history)', () => {
    addTool(undefined)
    updateToolResult(SID, 'call_1', 'ok', null, 12_500)
    expect(lastTool().elapsed).toBeUndefined()
  })
})

describe('setToolError endedAt', () => {
  it('uses the server-stamped end time the same way', () => {
    addTool(10_000)
    setToolError(SID, 'call_1', 'boom', 11_250)
    expect(lastTool().elapsed).toBe(1.25)
    expect(lastTool().error).toBe('boom')
  })
})

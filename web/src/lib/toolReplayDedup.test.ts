import { describe, it, expect, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import { chatMessages, addToolCallToGroup, appendToLastAssistant, finishAllTools, clearMsgs } from './stores'

// addToolCallToGroup dedups replayed tool_call events by tool_id: a mid-turn
// (re)subscribe redelivers the turn's buffered events, and without the dedup
// every card would render twice until the next refresh.

const SID = 'tool-replay-dedup-test'

function toolCall(toolId: string, name = 'terminal') {
  return {
    id: `t-${toolId}`, toolId, name, args: '', summary: '',
    startedAt: Date.now(), done: false, error: null, result: null, stdout: [], diff: null,
  }
}

function toolCount(): number {
  const msgs = get(chatMessages)[SID] ?? []
  return msgs.flatMap((m: any) => (m.type === 'tool_group' ? m.tools : [])).length
}

beforeEach(() => clearMsgs(SID))

describe('addToolCallToGroup replay dedup', () => {
  it('skips a tool_call whose tool_id is already rendered', () => {
    addToolCallToGroup(SID, toolCall('call_1'))
    addToolCallToGroup(SID, toolCall('call_1')) // replayed event, verbatim
    expect(toolCount()).toBe(1)
  })

  it('still renders two genuinely different calls back to back', () => {
    addToolCallToGroup(SID, toolCall('call_1'))
    addToolCallToGroup(SID, toolCall('call_2'))
    expect(toolCount()).toBe(2)
    // Same group: consecutive tools group together.
    const msgs = get(chatMessages)[SID] ?? []
    expect(msgs.filter((m: any) => m.type === 'tool_group')).toHaveLength(1)
  })

  it('dedups even after the group was finalized (streaming stopped)', () => {
    addToolCallToGroup(SID, toolCall('call_1'))
    finishAllTools(SID) // turn ended / group closed before the replay arrived
    addToolCallToGroup(SID, toolCall('call_1'))
    expect(toolCount()).toBe(1)
  })

  it('finds the duplicate in an older group, not just the trailing one', () => {
    addToolCallToGroup(SID, toolCall('call_1'))
    // An assistant segment ends the group; the next tool starts a new one.
    appendToLastAssistant(SID, 'some text between rounds')
    addToolCallToGroup(SID, toolCall('call_2'))
    const groups = (get(chatMessages)[SID] ?? []).filter((m: any) => m.type === 'tool_group')
    expect(groups).toHaveLength(2) // sanity: the two calls really are in separate groups
    addToolCallToGroup(SID, toolCall('call_1')) // replay of the FIRST call
    expect(toolCount()).toBe(2)
  })

  it('appends calls with an empty tool_id rather than deduping on it', () => {
    // tool_id is the dedup key; an absent one can't be trusted, so keep the
    // pre-dedup append behaviour for it.
    addToolCallToGroup(SID, toolCall(''))
    addToolCallToGroup(SID, toolCall(''))
    expect(toolCount()).toBe(2)
  })
})

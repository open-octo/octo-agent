import { describe, it, expect } from 'vitest'
import { get } from 'svelte/store'
import {
  chatSubAgents,
  chatWorkflows,
  applySubAgentEvent,
  applyWorkflowEvent,
  resetSubAgents,
  agentRunTrails,
  recordAgentTrailEvent,
  recordWorkflowTrailEvent,
  hydrateAgentRuns,
} from './stores'

// Folding of the enriched sub_agent_event / workflow_event payloads into the
// live trail state the AgentTrail component renders.

describe('applySubAgentEvent trail folding', () => {
  it('folds tools with outputs, text blocks, and the final result', () => {
    const sid = 'trail-1'
    resetSubAgents(sid)
    applySubAgentEvent(sid, { agent_id: 'agent_1', description: 'explore x', agent_type: 'explore', kind: 'started' })
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'tool', tool_id: 't1', tool_name: 'read_file', tool_input: { path: 'go.mod' } })
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'text', text: 'interim thought' })
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'tool_done', tool_id: 't1', tool_name: 'read_file', tool_output: 'module x' })
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'done', stop_reason: 'end_turn', result: 'final answer' })

    const [a] = get(chatSubAgents)[sid]
    expect(a.status).toBe('done')
    expect(a.result).toBe('final answer')
    expect(a.lastTool).toBe('read_file')
    expect(a.steps).toHaveLength(2)
    expect(a.steps[0]).toMatchObject({ kind: 'tool', id: 't1', name: 'read_file', output: 'module x', done: true, error: false })
    expect(a.steps[1]).toMatchObject({ kind: 'text', text: 'interim thought' })
  })

  it('marks a tool_error step and appends an unmatched completion', () => {
    const sid = 'trail-2'
    resetSubAgents(sid)
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'tool', tool_id: 't1', tool_name: 'terminal' })
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'tool_error', tool_id: 't1', tool_name: 'terminal', tool_output: 'exit 1' })
    // A completion whose "tool" event was never seen (aged out of retention).
    applySubAgentEvent(sid, { agent_id: 'agent_1', kind: 'tool_done', tool_id: 't9', tool_name: 'grep', tool_output: '3 hits' })

    const [a] = get(chatSubAgents)[sid]
    expect(a.steps).toHaveLength(2)
    expect(a.steps[0]).toMatchObject({ kind: 'tool', done: true, error: true, output: 'exit 1' })
    expect(a.steps[1]).toMatchObject({ kind: 'tool', id: 't9', name: 'grep', done: true, output: '3 hits' })
  })
})

describe('applyWorkflowEvent agent folding', () => {
  it('builds per-agent trails from agent_* events', () => {
    const sid = 'wf-trail-1'
    applyWorkflowEvent(sid, { run_id: 'wf_1', description: 'audit', kind: 'started' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'progress', line: 'phase: scan' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'agent_started', agent_id: 'a1', agent_label: 'check auth' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'agent_tool', agent_id: 'a1', tool_id: 't1', tool_name: 'grep' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'agent_tool_done', agent_id: 'a1', tool_id: 't1', tool_name: 'grep', tool_output: '3 hits' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'agent_text', agent_id: 'a1', text: 'auth looks fine' })
    applyWorkflowEvent(sid, { run_id: 'wf_1', kind: 'agent_done', agent_id: 'a1', reply: 'auth ok' })

    const [r] = get(chatWorkflows)[sid]
    expect(r.progress).toEqual(['phase: scan'])
    expect(r.agents).toHaveLength(1)
    const a = r.agents[0]
    expect(a).toMatchObject({ id: 'a1', label: 'check auth', status: 'done', reply: 'auth ok' })
    expect(a.steps).toHaveLength(2)
    expect(a.steps[0]).toMatchObject({ kind: 'tool', name: 'grep', output: '3 hits', done: true })
    expect(a.steps[1]).toMatchObject({ kind: 'text', text: 'auth looks fine' })
  })

  it('records an agent_done error and still removes the run on done', () => {
    const sid = 'wf-trail-2'
    applyWorkflowEvent(sid, { run_id: 'wf_2', kind: 'agent_started', agent_id: 'a1', agent_label: 'flaky' })
    applyWorkflowEvent(sid, { run_id: 'wf_2', kind: 'agent_done', agent_id: 'a1', error: 'spawn failed' })
    let [r] = get(chatWorkflows)[sid].filter(x => x.id === 'wf_2')
    expect(r.agents[0]).toMatchObject({ status: 'error', error: 'spawn failed' })

    applyWorkflowEvent(sid, { run_id: 'wf_2', kind: 'done', status: 'error' })
    expect(get(chatWorkflows)[sid].some(x => x.id === 'wf_2')).toBe(false)
  })
})

describe('agentRunTrails (transcript review)', () => {
  it('accumulates across rounds and survives done', () => {
    const sid = 'persist-1'
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', description: 'do x', kind: 'started' })
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'tool', tool_id: 't1', tool_name: 'grep' })
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'done', stop_reason: 'end_turn', result: 'round 1' })
    // A Continue round: trail re-opens, steps accumulate.
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'started' })
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'tool', tool_id: 't2', tool_name: 'read_file' })
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'done', stop_reason: 'end_turn', result: 'round 2' })

    const a = get(agentRunTrails)[sid].subAgents['agent_1']
    expect(a.status).toBe('done')
    expect(a.result).toBe('round 2')
    expect(a.steps).toHaveLength(2)
  })

  it('keeps workflow trails after the run finishes', () => {
    const sid = 'persist-2'
    recordWorkflowTrailEvent(sid, { run_id: 'wf_1', description: 'audit', kind: 'started' })
    recordWorkflowTrailEvent(sid, { run_id: 'wf_1', kind: 'agent_started', agent_id: 'a1', agent_label: 'scan' })
    recordWorkflowTrailEvent(sid, { run_id: 'wf_1', kind: 'agent_done', agent_id: 'a1', reply: 'ok' })
    recordWorkflowTrailEvent(sid, { run_id: 'wf_1', kind: 'done', status: 'done' })

    const r = get(agentRunTrails)[sid].workflows['wf_1']
    expect(r.status).toBe('done')
    expect(r.agents[0]).toMatchObject({ id: 'a1', status: 'done', reply: 'ok' })
  })

  it('hydrates from the REST snapshot without clobbering live entries', () => {
    const sid = 'persist-3'
    recordAgentTrailEvent(sid, { agent_id: 'agent_1', kind: 'tool', tool_id: 't1', tool_name: 'live_tool' })
    hydrateAgentRuns(sid, {
      sub_agents: [
        { agent_id: 'agent_1', description: 'stale snapshot', status: 'done', steps: [] },
        { agent_id: 'agent_9', description: 'old run', agent_type: 'explore', status: 'done', steps: [{ kind: 'tool', id: 't1', name: 'grep', output: 'x', done: true }], result: 'archived' },
      ],
      workflows: [
        { run_id: 'wf_9', description: 'old wf', status: 'error', logs: ['phase: a'], agents: [{ agent_id: 'a1', label: 'l', status: 'error', steps: [], error: 'boom' }] },
      ],
    })

    const t = get(agentRunTrails)[sid]
    // Live entry untouched by the snapshot.
    expect(t.subAgents['agent_1'].steps[0]).toMatchObject({ name: 'live_tool' })
    // Snapshot-only entries hydrated.
    expect(t.subAgents['agent_9']).toMatchObject({ description: 'old run', status: 'done', result: 'archived' })
    expect(t.workflows['wf_9']).toMatchObject({ status: 'error', logs: ['phase: a'] })
    expect(t.workflows['wf_9'].agents[0]).toMatchObject({ id: 'a1', error: 'boom' })
  })
})

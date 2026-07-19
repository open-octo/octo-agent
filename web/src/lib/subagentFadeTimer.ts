import { clearDoneSubAgents, type SubAgentState } from './stores'

// Shared by the whole-panel "all done" fade (this module) and the single-agent
// idle dismiss (ChatView.scheduleSubAgentDismiss) so the two never drift.
export const SUB_AGENT_DISMISS_MS = 2000

/**
 * Watches a session's live sub-agents and clears them all once every agent has
 * finished ("all done", panel shows the check badge) — after a short beat so
 * the user can actually read the completion state before it fades out.
 *
 * The instance is pure and Svelte-agnostic: the component drives it by calling
 * `update()` from a `$effect` whenever the snapshot changes, and `destroy()` on
 * teardown. The whole "read reactive state + schedule/cancel timer" core lives
 * here, testable with fake timers and no component mount.
 */
export interface SubAgentFadeTimer {
  /** Re-read schedule from a fresh (sid, agents) snapshot. */
  update(sid: string | undefined | null, agents: SubAgentState[]): void
  /** Cancel any pending timer (call from the component's teardown/cleanup). */
  destroy(): void
}

export function createSubAgentFadeTimer(): SubAgentFadeTimer {
  let timer: ReturnType<typeof setTimeout> | null = null

  function cancel() {
    if (timer !== null) {
      clearTimeout(timer)
      timer = null
    }
  }

  return {
    update(sid, agents) {
      cancel()
      if (!sid) return
      if (agents.length === 0) return
      if (agents.some(a => a.status === 'running')) return
      timer = setTimeout(() => {
        timer = null
        clearDoneSubAgents(sid)
      }, SUB_AGENT_DISMISS_MS)
    },
    destroy: cancel,
  }
}

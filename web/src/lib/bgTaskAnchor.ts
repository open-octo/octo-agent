// Background-process elapsed time, anchored locally.
//
// The server stamps `elapsed` when it broadcasts background_tasks_update, and
// it only broadcasts on tool calls and process exits — so between them the
// number sits frozen and under-reports a process that outlives its turn.
// Anchoring each task to a local start timestamp lets the UI derive elapsed
// from its own one-second tick instead.

/** How far the server's elapsed may drift from a kept anchor before re-anchoring. */
export const BG_ANCHOR_TOLERANCE_MS = 2000

export interface BgTask {
  handle_id?: string
  command?: string
  elapsed?: number
  /** Local wall-clock ms the process is treated as having started at. */
  startedAt?: number
  [k: string]: any
}

/**
 * Stamps `startedAt` on each incoming task.
 *
 * A task already on screen keeps its original anchor, so re-broadcasts (whose
 * `elapsed` is truncated to whole seconds and arrives after some network
 * delay) can't make a running clock jump. The anchor is only recomputed when
 * the server disagrees by more than the tolerance — which is what a reused
 * handle id looks like after the server restarts and its ids fall back to 1.
 */
export function anchorBgTasks(prev: BgTask[], incoming: BgTask[], at: number): BgTask[] {
  return (incoming ?? []).map(t => {
    const elapsed = Math.max(0, Number(t.elapsed) || 0)
    const anchored = at - elapsed * 1000
    const seen = (prev ?? []).find(p => p.handle_id === t.handle_id)
    const keep = seen?.startedAt !== undefined && Math.abs(seen.startedAt - anchored) <= BG_ANCHOR_TOLERANCE_MS
    return { ...t, startedAt: keep ? seen!.startedAt! : anchored }
  })
}

// Group elapsed = wall-clock span from the first tool's start to the last
// tool's finish — "how long this group took" as the user watched it run,
// LLM round-trips between rounds included. Starts/ends come from server-
// stamped event timestamps (startedAt + per-tool elapsed), so a mid-turn
// reload or reconnect keeps the real span instead of collapsing to the tail
// tool, and parallel batches don't multiply-count the way a sum of per-tool
// durations did. Tools with no timing (pre-CreatedAt history) are skipped;
// a group with none stays empty.
export function groupElapsed(ts: any[]): string {
  let start = Infinity
  let end = 0
  for (const t of ts) {
    if (typeof t.startedAt !== 'number' || t.startedAt <= 0) continue
    start = Math.min(start, t.startedAt)
    // A tool finished without a result-derived elapsed (closed by
    // finishAllTools/finishToolsById) contributes a zero-width point.
    const e = typeof t.elapsed === 'number' ? t.startedAt + t.elapsed * 1000 : t.startedAt
    end = Math.max(end, e)
  }
  if (!isFinite(start) || end <= start) return ''
  // Round to whole seconds BEFORE splitting into m/s: rounding the minute
  // remainder on its own produces "1m 60s" for spans like 119.96s.
  const secs = Math.round((end - start) / 1000)
  if (secs >= 60) {
    const m = Math.floor(secs / 60)
    const s = secs % 60
    return s > 0 ? `${m}m ${s}s` : `${m}m`
  }
  const total = (end - start) / 1000
  // Sub-100ms would render as "0.0s" — noise on the header, omit instead.
  if (total < 0.1) return ''
  return `${total.toFixed(1)}s`
}

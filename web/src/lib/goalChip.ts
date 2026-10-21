// The session goal's one-line status, shared by the desktop composer chip and
// the phone's goal bar: usage while active (tokens against the budget, or time
// spent when unbudgeted), the status label otherwise; '' when there is no goal.
export function goalChip(goal: any): string {
  if (!goal) return ''
  const compact = (n: number) =>
    n >= 1_000_000 ? `${(n / 1_000_000).toFixed(1).replace(/\.0$/, '')}M`
    : n >= 1_000 ? `${(n / 1_000).toFixed(1).replace(/\.0$/, '')}K`
    : `${n}`
  if (goal.status === 'active') {
    if (goal.token_budget > 0) return `${compact(goal.tokens_used ?? 0)}/${compact(goal.token_budget)}`
    const s = goal.time_used_seconds ?? 0
    return s < 60 ? `${s}s` : s < 3600 ? `${Math.floor(s / 60)}m` : `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`
  }
  return String(goal.status ?? '').replace('_', ' ')
}

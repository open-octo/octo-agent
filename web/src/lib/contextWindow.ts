// Context-window values for endpoint models, in tokens. 0 means "not set":
// the server then falls back to the built-in model table.

// The server rejects 1–999 as a unit mistake (config.MinFallbackContextWindow).
export const MIN_CONTEXT_WINDOW = 1000

// parseContextWindow reads what a user typed: a plain token count or a k/M
// shorthand ("200k", "1M", "1.5m"). Empty means unset (0). Returns null for
// anything the server would reject.
export function parseContextWindow(input: string): number | null {
  const s = input.trim().replace(/[,_\s]/g, '').toLowerCase()
  if (s === '') return 0
  const m = /^(\d+(?:\.\d+)?)([km]?)$/.exec(s)
  if (!m) return null
  const n = Math.round(Number(m[1]) * (m[2] === 'm' ? 1_000_000 : m[2] === 'k' ? 1000 : 1))
  return n >= MIN_CONTEXT_WINDOW ? n : null
}

// formatContextWindow renders a token count in the same shorthand, exactly:
// a value that isn't a whole number of k or M (or tenths of M, so 1.5M reads
// the way it was typed) is shown as-is so editing it round-trips without loss.
export function formatContextWindow(n: number): string {
  if (n <= 0) return ''
  if (n >= 1_000_000 && n % 100_000 === 0) return `${n / 1_000_000}M`
  if (n % 1000 === 0) return `${n / 1000}K`
  return String(n)
}

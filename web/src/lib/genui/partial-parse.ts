// Streaming partial JSON parse for an in-progress ```octo-ui fence body. See
// dev-docs/genui-design.md, "Streaming partial parse". `buffer` is not valid
// JSON yet (the model is still emitting it), but a prefix of it very often
// already describes several complete, safe-to-render nodes — this recovers
// the longest such prefix.
//
// This is a fresh implementation of the algorithm the design doc describes
// (itself inspired by, not ported from, dsh-genui's parse-partial.ts — see
// the design doc's "Code reuse decision"), not a port of any other
// codebase's implementation.
//
// Deliberately guard-agnostic: the caller (fence-split.ts) is responsible
// for running the result through sanitizeSpec, exactly like a complete spec.

// The scan tracks currently-open brackets/braces as a persistent (immutable)
// linked stack rather than a mutable array. Push allocates one new node
// pointing at the previous top; pop just moves the "top" pointer back to
// the parent node it already had — no array copy, no mutation of a shared
// snapshot. This is what lets each candidate below be recorded in O(1): it
// only stores a *reference* to the stack node as of that moment, not a copy
// of the whole stack. Reconstructing the actual closing-bracket string from
// that reference is deferred to the (bounded, ≤32) replay pass after the
// scan, so the single forward scan itself never redoes O(depth) work per
// character — the exact "rescan every earlier candidate" trap this function
// must not fall into.
interface StackNode {
  char: '{' | '['
  parent: StackNode | null
}

function closerFor(node: StackNode | null): string {
  let s = ''
  let n = node
  while (n) {
    s += n.char === '{' ? '}' : ']'
    n = n.parent
  }
  return s
}

const MAX_CANDIDATES = 32

/**
 * Scans `buffer` once, left to right, and returns the parsed value of the
 * longest prefix that — once the currently-open brackets/braces are
 * appended — is syntactically valid JSON. Returns `null` if no such prefix
 * exists (nothing in the buffer is safe to render yet). Never throws.
 */
export function parsePartialGenuiJson(buffer: string): unknown | null {
  let top: StackNode | null = null
  let inString = false
  let escaped = false

  // Fixed-size ring buffer of the most recent candidate offsets (bounded
  // memory regardless of input size — the pathological-input guard).
  const ring: { offset: number; top: StackNode | null }[] = new Array(MAX_CANDIDATES)
  let candidateCount = 0

  for (let i = 0; i < buffer.length; i++) {
    const c = buffer[i]

    if (inString) {
      if (escaped) {
        escaped = false
      } else if (c === '\\') {
        escaped = true
      } else if (c === '"') {
        inString = false
      }
      continue
    }

    if (c === '"') {
      inString = true
      continue
    }

    if (c === '{' || c === '[') {
      top = { char: c, parent: top }
      continue
    }

    if (c === '}' || c === ']') {
      if (top === null) continue // stray closer with nothing open — ignore
      const expected = top.char === '{' ? '}' : ']'
      if (c !== expected) continue // mismatched closer — leave the stack alone
      top = top.parent
      ring[candidateCount % MAX_CANDIDATES] = { offset: i + 1, top }
      candidateCount++
      continue
    }
    // Any other character (letters, digits, ':', ',', whitespace, etc.) is
    // structurally inert at this level of analysis — skip it.
  }

  if (candidateCount === 0) return null

  // Try from the longest (most recently recorded) candidate down to the
  // shortest one still held in the ring.
  const oldestKept = Math.max(0, candidateCount - MAX_CANDIDATES)
  for (let idx = candidateCount - 1; idx >= oldestKept; idx--) {
    const candidate = ring[idx % MAX_CANDIDATES]
    const attempt = buffer.slice(0, candidate.offset) + closerFor(candidate.top)
    try {
      return JSON.parse(attempt)
    } catch {
      // Not valid JSON (e.g. a truncated string/number just before this
      // closer) — fall through to the next-shorter candidate.
    }
  }
  return null
}

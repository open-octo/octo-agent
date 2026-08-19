// Splits an assistant message's raw text into markdown / octo-ui segments.
// See dev-docs/genui-design.md, "Slice B: inline octo-ui fences" ->
// "Message segmentation". This is the one new insertion point in the
// markdown pipeline: ChatView.svelte calls this instead of handing the whole
// message straight to throttledMarkdown, so a ```octo-ui fence can render as
// a live component tree instead of an unstyled code block.
//
// Structurally similar to markdown.ts's renderMarkdown() <think> extraction
// (extract the special blocks, leave the rest as plain text) but scanning
// for a fenced-code delimiter instead of a fixed tag, and — unlike the
// <think> pass — an unclosed trailing fence is not deferred until it closes;
// it is handed to partial-parse.ts so it can render as soon as it has a
// syntactically valid, safe-to-render prefix.

import type { GenuiSpec } from './types'
import { sanitizeSpec, READ_ONLY_NODE_TYPES, INTERACTIVE_NODE_TYPES } from './guard'
import { parsePartialGenuiJson } from './partial-parse'

export type Segment =
  | { kind: 'markdown'; text: string }
  | { kind: 'octo-ui'; raw: string; complete: boolean; spec: GenuiSpec | null }

// The inline-fence path is the one place a spec may legitimately contain
// interactive nodes (button/input/select/…) — render_ui's tool-card path
// never does (see guard.ts). Union the two whitelists rather than reusing
// READ_ONLY_NODE_TYPES alone.
const ALLOWED_TYPES: ReadonlySet<string> = new Set([...READ_ONLY_NODE_TYPES, ...INTERACTIVE_NODE_TYPES])

const FENCE_OPEN = '```octo-ui'
const FENCE_CLOSE = '```'

/**
 * Splits `text` into a sequence of markdown / octo-ui segments, in order.
 * A message with no fence returns exactly one markdown segment equal to the
 * input, byte-identical — the explicit no-op path every pre-existing
 * message/session must take (per the design's compatibility test #11).
 */
export function splitOctoUiFences(text: string): Segment[] {
  const segments: Segment[] = []
  let cursor = 0

  while (cursor < text.length) {
    const openIdx = findFenceOpen(text, cursor)
    if (openIdx === -1) {
      // No more fences: everything remaining is markdown.
      if (cursor < text.length) segments.push({ kind: 'markdown', text: text.slice(cursor) })
      break
    }

    if (openIdx > cursor) {
      segments.push({ kind: 'markdown', text: text.slice(cursor, openIdx) })
    }

    // Body starts right after "```octo-ui" and its trailing newline.
    const bodyStart = skipToNextLine(text, openIdx + FENCE_OPEN.length)
    if (bodyStart === -1) {
      // "```octo-ui" is the literal end of the text with no newline after it
      // yet — the model is still typing the opening line itself. Treat the
      // (empty) body as an in-progress fence.
      segments.push({ kind: 'octo-ui', raw: '', complete: false, spec: null })
      cursor = text.length
      break
    }

    const closeIdx = findFenceClose(text, bodyStart)
    if (closeIdx === -1) {
      // Unclosed trailing fence — only possible as the last thing in the
      // text (the model is mid-way through emitting it). Hand the body to
      // the streaming partial parser instead of waiting for the close.
      const raw = text.slice(bodyStart)
      const partial = parsePartialGenuiJson(raw)
      const { spec } = partial !== null ? sanitizeSpec(partial, ALLOWED_TYPES) : { spec: null }
      segments.push({ kind: 'octo-ui', raw, complete: false, spec })
      cursor = text.length
      break
    }

    const raw = text.slice(bodyStart, closeIdx)
    let spec: GenuiSpec | null = null
    try {
      const parsed = JSON.parse(raw)
      spec = sanitizeSpec(parsed, ALLOWED_TYPES).spec
    } catch {
      spec = null
    }
    segments.push({ kind: 'octo-ui', raw, complete: true, spec })

    // Advance past the closing fence line ("```" + optional trailing
    // newline).
    let next = closeIdx + FENCE_CLOSE.length
    if (text[next] === '\n') next++
    cursor = next
  }

  return segments
}

// Finds the next "```octo-ui" that starts at the beginning of a line (either
// the start of the text or right after a '\n'), matching how markdown.ts's
// renderer.code recognizes a fenced code block's language tag. Returns -1
// if none is found from `from` onward.
function findFenceOpen(text: string, from: number): number {
  let idx = text.indexOf(FENCE_OPEN, from)
  while (idx !== -1) {
    if (idx === 0 || text[idx - 1] === '\n') {
      // Must be followed by end-of-string, a newline, or whitespace before a
      // newline — not e.g. "```octo-uism" (a different, longer tag).
      const after = text[idx + FENCE_OPEN.length]
      if (after === undefined || after === '\n' || after === '\r') return idx
    }
    idx = text.indexOf(FENCE_OPEN, idx + 1)
  }
  return -1
}

// Returns the offset right after the next '\n' starting at/after `from`, or
// -1 if there is no newline yet (the opening fence line itself is still
// streaming in).
function skipToNextLine(text: string, from: number): number {
  const nl = text.indexOf('\n', from)
  return nl === -1 ? -1 : nl + 1
}

// Finds a closing "```" fence: a line consisting of exactly "```" (optionally
// followed by trailing whitespace) starting at/after `from`. Returns the
// offset of the "```" itself, or -1 if no closing fence is found.
function findFenceClose(text: string, from: number): number {
  let searchFrom = from
  while (searchFrom <= text.length) {
    const idx = text.indexOf(FENCE_CLOSE, searchFrom)
    if (idx === -1) return -1
    const atLineStart = idx === 0 || text[idx - 1] === '\n'
    // What follows must be end-of-string or a newline (a bare ``` line),
    // once trailing whitespace is stripped.
    let after = idx + FENCE_CLOSE.length
    while (text[after] === ' ' || text[after] === '\t' || text[after] === '\r') after++
    const lineEnds = after === text.length || text[after] === '\n'
    if (atLineStart && lineEnds) return idx
    searchFrom = idx + 1
  }
  return -1
}

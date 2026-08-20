// Classifies the two messages of a silent turn — the panel action the user
// fired and the model's reply that updates that panel — so neither renders as
// a chat bubble. See dev-docs/genui-interactive-panels-design.md, "Silent
// turns".
//
// This is entirely a rendering decision. Both messages are sent normally,
// enter history normally, and the model sees them normally; the frontend just
// draws the reply into the panel instead of into the transcript. No new WS
// frame, no session schema change, no backend awareness of the concept.

import type { ChatMessage } from '../types'
import { splitOctoUiFences, FENCE_OPEN } from './fence-split'

export const OCTO_UI_ACTION_PREFIX = '[octo-ui-action] '

export interface GenuiActionEnvelope {
  /** Present only when the acting panel had an id — its presence is what
   * marks the turn silent. An action from an anonymous panel omits it and
   * keeps the original visible-chip behaviour. */
  panel?: string
  action: string
  fields?: Record<string, unknown>
  payload?: Record<string, unknown>
}

/** Parses an `[octo-ui-action] {...}` message body. Returns null when the
 * prefix matches but the remainder isn't a valid envelope — never show a
 * broken chip, and never hide a message we failed to understand. */
export function parseActionEnvelope(content: string): GenuiActionEnvelope | null {
  if (!content.startsWith(OCTO_UI_ACTION_PREFIX)) return null
  try {
    const parsed = JSON.parse(content.slice(OCTO_UI_ACTION_PREFIX.length))
    if (typeof parsed !== 'object' || parsed === null || typeof parsed.action !== 'string') return null
    return parsed as GenuiActionEnvelope
  } catch {
    return null
  }
}

/** The panel id a user message silently addresses, or null if it is an
 * ordinary message (or an anonymous panel's action, which stays visible). */
export function silentActionPanel(msg: ChatMessage | undefined): string | null {
  if (!msg || msg.type !== 'user') return null
  const env = parseActionEnvelope(msg.content)
  return env && typeof env.panel === 'string' && env.panel !== '' ? env.panel : null
}

/**
 * Whether a reply's text still has the shape of a silent update.
 *
 * Called on every streaming delta, so it must be monotone: once false it must
 * stay false, or a bubble would appear and then vanish.
 *
 * The subtle part is the opening fence arriving one character at a time.
 * "```octo-ui" is only recognized as a fence once it is complete, so the
 * intermediate states ("`", "``", "```", "```oct") split as ordinary markdown
 * — and rejecting those would make this flicker false on the way in for
 * *every* silent turn, which is the opposite of what it is for. A trailing
 * run that is still a prefix of the opening marker is therefore treated as
 * "not prose yet".
 *
 * That tolerance is safe precisely because it is anchored to the end of the
 * text: any prefix that later turns out to be something else (a code fence in
 * another language, prose starting with a backtick) grows into a trailing
 * segment that no longer prefixes the marker, and the answer goes false and
 * stays false.
 */
export function couldBeSilentReply(content: string): boolean {
  const segments = splitOctoUiFences(content)
  let fences = 0
  for (let i = 0; i < segments.length; i++) {
    const seg = segments[i]
    if (seg.kind === 'markdown') {
      const text = seg.text.trim()
      if (text === '') continue
      if (i === segments.length - 1 && isPartialFenceOpen(text)) continue
      return false
    }
    fences++
    if (fences > 1) return false
  }
  return true
}

/** True when `text` is a strict prefix of the opening fence marker — i.e. it
 * could still grow into one rather than being prose that merely starts with a
 * backtick. */
function isPartialFenceOpen(text: string): boolean {
  return text.length < FENCE_OPEN.length && FENCE_OPEN.startsWith(text)
}

/**
 * The finished-message decision: a reply is a silent panel update only when
 * it follows a silent action, contains exactly one octo-ui fence and no other
 * text, and that fence's guarded spec carries the id the action addressed.
 *
 * Failing any of those, the caller renders an ordinary bubble. That single
 * degradation path covers every way this can go wrong — the model wanted to
 * explain something, addressed a different panel, produced no fence, or the
 * guard rejected the spec — and nothing is ever lost.
 */
export function isSilentReply(msg: ChatMessage, expectedPanelId: string): boolean {
  if (msg.type !== 'assistant') return false
  let fences = 0
  let matched = false
  for (const seg of splitOctoUiFences(msg.content)) {
    if (seg.kind === 'markdown') {
      if (seg.text.trim() !== '') return false
      continue
    }
    fences++
    if (fences > 1) return false
    if (seg.complete && seg.spec?.id === expectedPanelId) matched = true
  }
  return fences === 1 && matched
}

/**
 * The conversational message preceding `index`, skipping the rows that are
 * turn scaffolding rather than something said: tool groups and progress
 * lines.
 *
 * This matters because the archetypal silent turn — "the panel needs data the
 * model doesn't have" — is exactly the case where the model calls a tool
 * first. Looking only at index-1 would find that tool group, conclude the
 * reply answers nothing, and fall back to a visible bubble in precisely the
 * situation the feature exists for.
 */
export function precedingSaid(msgs: ChatMessage[], index: number): ChatMessage | undefined {
  for (let i = index - 1; i >= 0; i--) {
    const t = msgs[i].type
    if (t === 'user' || t === 'assistant') return msgs[i]
  }
  return undefined
}

/** True when the message at `index` is the silent half of a pair. */
export function isSilentPairAt(msgs: ChatMessage[], index: number): boolean {
  const msg = msgs[index]
  if (!msg) return false
  const panel = silentActionPanel(precedingSaid(msgs, index))
  return panel !== null && isSilentReply(msg, panel)
}

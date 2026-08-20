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
import { splitOctoUiFences } from './fence-split'

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
 * Monotone by construction, which is what makes it safe to call on every
 * streaming delta: it is true while the content is empty or is exactly one
 * octo-ui fence, and goes false the moment any non-whitespace text appears
 * outside a fence or a second fence starts. Since message text only grows,
 * it never flips back — so a reply that starts rendering as an ordinary
 * bubble mid-stream stays one.
 */
export function couldBeSilentReply(content: string): boolean {
  let fences = 0
  for (const seg of splitOctoUiFences(content)) {
    if (seg.kind === 'markdown') {
      if (seg.text.trim() !== '') return false
    } else {
      fences++
      if (fences > 1) return false
    }
  }
  return true
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

/** True when this assistant message is the silent half of a pair with the
 * message before it. `prev` is the immediately preceding message. */
export function isSilentPair(prev: ChatMessage | undefined, msg: ChatMessage): boolean {
  const panel = silentActionPanel(prev)
  return panel !== null && isSilentReply(msg, panel)
}

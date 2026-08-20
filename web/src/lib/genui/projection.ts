// Derives, from a session's message list, which version of each addressable
// panel is live and where it renders. See
// dev-docs/genui-interactive-panels-design.md, "Projection".
//
// The load-bearing property: this is a pure function of the messages. Nothing
// is stored server-side, no message is ever rewritten, and a page reload
// re-derives every panel's identity and content by replaying it over the same
// history.

import type { ChatMessage } from '../types'
import type { GenuiSpec } from './types'
import { splitOctoUiFences } from './fence-split'
import { isSilentPairAt } from './silent-turn'

export interface PanelProjection {
  /** The newest version's spec — whatever the model most recently said this
   * panel contains. */
  spec: GenuiSpec
  /** Message whose fence renders the live panel. */
  anchorMsgId: string
  /** Segment index within that message. */
  anchorSegIdx: number
  /** How many versions have been seen, for a "updated N times" affordance. */
  versions: number
}

/**
 * Two fields update on different rules, and the distinction is the design:
 *
 * - `spec` always takes the newest version.
 * - `anchor` moves only when the fence appears in a message that renders as a
 *   bubble. A silent-turn reply updates content in place; a fence inside an
 *   ordinary reply — one where the model is also talking to the user — is a
 *   new presentation of the panel and belongs at that point in the
 *   conversation.
 */
export function projectPanels(messages: ChatMessage[]): Map<string, PanelProjection> {
  const out = new Map<string, PanelProjection>()

  for (let i = 0; i < messages.length; i++) {
    const msg = messages[i]
    if (msg.type !== 'assistant' || !msg.content) continue
    const silent = isSilentPairAt(messages, i)

    const segments = splitOctoUiFences(msg.content)
    for (let segIdx = 0; segIdx < segments.length; segIdx++) {
      const seg = segments[segIdx]
      if (seg.kind !== 'octo-ui') continue
      const id = seg.spec?.id
      if (!id) continue

      const prev = out.get(id)
      out.set(id, {
        spec: seg.spec as GenuiSpec,
        // A silent reply keeps the existing anchor. The `?? msg.id` fallback
        // covers a silent-shaped reply with no earlier version to anchor to,
        // which shouldn't arise (a silent turn needs a prior action that
        // referenced the panel) but must not produce a panel with nowhere to
        // render.
        anchorMsgId: silent ? (prev?.anchorMsgId ?? msg.id) : msg.id,
        anchorSegIdx: silent ? (prev?.anchorSegIdx ?? segIdx) : segIdx,
        versions: (prev?.versions ?? 0) + 1,
      })
    }
  }

  return out
}

/**
 * Whether this exact segment position should render the live panel.
 *
 * An anonymous spec (no id) always renders where it sits — the pre-existing
 * behaviour every old session relies on. An id-bearing segment renders only
 * at its anchor; elsewhere it belongs to a silent turn whose message isn't
 * drawn at all, so this is a guard against the model re-emitting a panel
 * inside a message that also carries prose.
 */
export function isAnchor(
  panels: Map<string, PanelProjection>,
  spec: GenuiSpec | null,
  msgId: string,
  segIdx: number
): boolean {
  if (!spec) return false
  if (!spec.id) return true
  const p = panels.get(spec.id)
  return !!p && p.anchorMsgId === msgId && p.anchorSegIdx === segIdx
}

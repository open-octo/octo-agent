// Which transcript rows the mobile chat draws, given GenUI's silent turns: an
// action on an addressable panel and the reply that updates it render nothing
// (the panel changes in place). The desktop ChatView's rules, on the shared
// silent-turn helpers.
import type { ChatMessage } from '../lib/types'
import { silentActionPanel, isSilentPairAt, precedingSaid, couldBeSilentReply } from '../lib/genui/silent-turn'

// The reply half of a silent pair. While it streams it stays hidden for as
// long as it could still be one — the check is monotone, so prose, once it
// appears, stays shown.
export function isHiddenReplyAt(msgs: ChatMessage[], index: number): boolean {
  const m = msgs[index]
  if (!m || m.type !== 'assistant') return false
  if (m.streaming) {
    const p = silentActionPanel(precedingSaid(msgs, index))
    return p !== null && couldBeSilentReply(m.content)
  }
  return isSilentPairAt(msgs, index)
}

export function isHiddenAt(msgs: ChatMessage[], index: number): boolean {
  const m = msgs[index]
  return (m?.type === 'user' && silentActionPanel(m) !== null) || isHiddenReplyAt(msgs, index)
}

// How many rows draw something. The chat pins to the bottom when this grows,
// not when the message count does: a silent turn adds two hidden rows, and
// pinning on those would carry the view away from the panel being used.
export function visibleCount(msgs: ChatMessage[]): number {
  let n = 0
  for (let i = 0; i < msgs.length; i++) if (!isHiddenAt(msgs, i)) n++
  return n
}

// A compaction folds the first `folded` persisted messages into one summary
// message, so every later persisted index moves down by folded-1, and the
// folded messages no longer exist on disk — their bubbles stay on screen but
// can no longer be edited or branched from. Returns the input unchanged when
// no bubble carries an index.
export function shiftMessageIndices<T extends { messageIndex?: number }>(msgs: T[], folded: number): T[] {
  if (folded <= 0) return msgs
  let changed = false
  const out = msgs.map(m => {
    if (typeof m.messageIndex !== 'number') return m
    changed = true
    if (m.messageIndex < folded) {
      const { messageIndex: _, ...rest } = m
      return rest as T
    }
    return { ...m, messageIndex: m.messageIndex - folded + 1 }
  })
  return changed ? out : msgs
}

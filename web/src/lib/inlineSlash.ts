// Slash commands the server applies inline in wsUserMessage — they mutate the
// session and return before any turn starts, so the text never enters history
// and no history_user_message is ever broadcast for it.
//
// That absence is what the caller cares about: send()'s optimistic user bubble
// is retired by that echo, and its pendingSends entry is retired by the same
// event. Left in place for an inline command, the bubble spins forever and the
// stale queue entry is shifted out by the NEXT message's confirmation, so that
// one is mis-classified (steer vs fresh) and its own bubble is never de-duped.
//
// The matching mirrors internal/server/ws_handlers.go exactly: /clear, /compact
// and /reload are case-insensitive exact matches; /goal is case-sensitive and
// takes arguments. Attachments take the message off the inline path server-side.
export type InlineSlashCommand = 'clear' | 'compact' | 'reload' | 'goal'

export function inlineSlashCommand(text: string, hasFiles = false): InlineSlashCommand | null {
  if (hasFiles) return null
  const trimmed = text.trim()
  switch (trimmed.toLowerCase()) {
    case '/clear':
      return 'clear'
    case '/compact':
      return 'compact'
    case '/reload':
      return 'reload'
  }
  if (trimmed === '/goal' || trimmed.startsWith('/goal ')) return 'goal'
  return null
}

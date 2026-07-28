// Which submit the composer's Enter key means.
//
// Three-way, and the order matters: Cmd/Ctrl+Enter has to be recognised before
// plain Enter, or the send branch swallows it (both are `key === 'Enter'` with
// shift up). Shift+Enter is neither — it falls through to the textarea, which
// inserts the newline itself.

export interface SubmitKeyEvent {
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  shiftKey?: boolean
}

// 'queue' runs the message as its own turn after the one in flight; 'send'
// submits normally (mid-turn that means steering the running turn); null means
// this key is not a submit and the caller should keep handling it.
export function submitIntent(e: SubmitKeyEvent): 'queue' | 'send' | null {
  if (e.key !== 'Enter') return null
  if (e.metaKey || e.ctrlKey) return 'queue'
  if (e.shiftKey) return null
  return 'send'
}

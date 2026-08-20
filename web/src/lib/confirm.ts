import { writable } from 'svelte/store'

// Promise-based confirmation that works in any host, unlike window.confirm():
// native webviews (Wails/WKWebView) don't implement the JS confirm panel, so
// window.confirm() silently returns false there and every confirm-gated action
// no-ops in the desktop shell. confirmDialog() renders an in-app modal instead.
//
//   if (!(await confirmDialog(tr('...')))) return
//
// The message may contain newlines; the dialog preserves them.
//
// A destructive confirmation takes a title and `danger`: what is about to happen
// belongs in the heading rather than buried in a paragraph, and the button that
// does it should not look like the one that cancels.
export interface ConfirmOptions {
  title?: string
  /** Style the confirm button as destructive, and label it accordingly. */
  danger?: boolean
  confirmLabel?: string
}

export interface ConfirmRequest extends ConfirmOptions {
  message: string
  resolve: (ok: boolean) => void
}

export const confirmRequest = writable<ConfirmRequest | null>(null)

export function confirmDialog(message: string, opts: ConfirmOptions = {}): Promise<boolean> {
  return new Promise((resolve) => {
    confirmRequest.set({ ...opts, message, resolve })
  })
}

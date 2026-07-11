import { writable } from 'svelte/store'

// Promise-based confirmation that works in any host, unlike window.confirm():
// native webviews (Wails/WKWebView) don't implement the JS confirm panel, so
// window.confirm() silently returns false there and every confirm-gated action
// no-ops in the desktop shell. confirmDialog() renders an in-app modal instead.
//
//   if (!(await confirmDialog(tr('...')))) return
//
// The message may contain newlines; the dialog preserves them.
export interface ConfirmRequest {
  message: string
  resolve: (ok: boolean) => void
}

export const confirmRequest = writable<ConfirmRequest | null>(null)

export function confirmDialog(message: string): Promise<boolean> {
  return new Promise((resolve) => {
    confirmRequest.set({ message, resolve })
  })
}

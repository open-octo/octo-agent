// Desktop-notification preference — an app-level on/off switch that is
// independent of the browser permission grant. The browser won't let us
// programmatically revoke a granted permission, so "off" is expressed here:
// notifyForSessionActivity() consults this store before firing. Both the
// header bell and the Settings toggle flip it through setNotificationsEnabled.
import { get, writable } from 'svelte/store'
import { showToast, nativeShell } from './stores'
import { tr } from './i18n'

const KEY = 'octo.notifications'

// Default on, so users who granted the bell keep getting notifications after
// this switch was introduced (absence of the key === legacy "enabled").
function initial(): boolean {
  const v = localStorage.getItem(KEY)
  return v === null ? true : v === 'true'
}

export const notificationsEnabled = writable<boolean>(initial())
notificationsEnabled.subscribe(v => {
  try { localStorage.setItem(KEY, String(v)) } catch { /* private mode: keep in-memory only */ }
})

// Flip the preference. Turning it on requires (and requests) browser
// permission; if notifications are unsupported or blocked we surface a toast
// and leave the switch off. Returns the resulting enabled state so callers
// binding a control can reconcile it (e.g. snap a Switch back to false).
export async function setNotificationsEnabled(on: boolean): Promise<boolean> {
  if (!on) {
    notificationsEnabled.set(false)
    showToast(tr('header.notif_disabled'))
    return false
  }
  // Desktop shell: the OS owns notification permission (prompted natively on
  // first send), and the browser Notification API doesn't work in the webview.
  // Skip the browser permission dance and just enable.
  if (get(nativeShell)) {
    notificationsEnabled.set(true)
    showToast(tr('header.notif_enabled'))
    return true
  }
  if (!('Notification' in window)) {
    showToast(tr('header.notif_unsupported'), 'error')
    notificationsEnabled.set(false)
    return false
  }
  if (Notification.permission === 'denied') {
    showToast(tr('header.notif_blocked'), 'error')
    notificationsEnabled.set(false)
    return false
  }
  if (Notification.permission !== 'granted') {
    const perm = await Notification.requestPermission()
    if (perm !== 'granted') {
      showToast(tr('header.notif_not_enabled'), 'error')
      notificationsEnabled.set(false)
      return false
    }
  }
  notificationsEnabled.set(true)
  showToast(tr('header.notif_enabled'))
  return true
}

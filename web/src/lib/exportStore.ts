import { writable, get } from 'svelte/store'

// Each session can independently be in export mode, so the store maps
// session id → boolean. A null session id is always false.
function createExportModeStore() {
  const { subscribe, set, update } = writable<Record<string, boolean>>({})

  function enter(sid: string) {
    update(m => ({ ...m, [sid]: true }))
  }

  function exit(sid: string) {
    update(m => ({ ...m, [sid]: false }))
  }

  function isActive(sid: string | null): boolean {
    if (!sid) return false
    return get({ subscribe })[sid] === true
  }

  return { subscribe, enter, exit, isActive }
}

// Selected message IDs per session. Only user/assistant messages are
// selectable; tool messages are excluded from the selection set.
function createSelectedMessagesStore() {
  const { subscribe, set, update } = writable<Record<string, Set<string>>>({})

  function initForSession(sid: string, ids: string[]) {
    update(m => ({ ...m, [sid]: new Set(ids) }))
  }

  function toggle(sid: string, msgId: string) {
    update(m => {
      const cur = new Set(m[sid] ?? [])
      if (cur.has(msgId)) cur.delete(msgId)
      else cur.add(msgId)
      return { ...m, [sid]: cur }
    })
  }

  function getForSession(sid: string): Set<string> {
    return get({ subscribe })[sid] ?? new Set()
  }

  function clear(sid: string) {
    update(m => {
      const next = { ...m }
      delete next[sid]
      return next
    })
  }

  return { subscribe, initForSession, toggle, getForSession, clear }
}

export const exportModeStore = createExportModeStore()
export const selectedMessagesStore = createSelectedMessagesStore()

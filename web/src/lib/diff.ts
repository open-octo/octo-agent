// Git Diff review panel state (dev-docs/git-diff-panel-design.md).
//
// The panel answers "what did the agent just change in here". Two things
// follow from that. There is no polling — a diff only moves when a turn ends,
// and that already arrives as a WS event. And nothing is cached across
// sessions: the data is stale within seconds, so every open refetches.
import { writable, get } from 'svelte/store'
import type { GitDiffFile, GitDiffResponse } from './types'
import * as api from './api'
import { activeSessionId, panelContent } from './stores'

/** The current session's diff, or null before the first load. */
export const diffData = writable<GitDiffResponse | null>(null)
export const diffLoading = writable(false)
export const diffError = writable('')

/**
 * Changed-file count per session id. Keyed by session so switching chats can't
 * show one session's badge over another's diff.
 */
export const diffBadge = writable<Record<string, number>>({})

/** The session diffData currently holds, so a late response can be discarded. */
let loadedFor = ''

function countFiles(resp: { repos: { files: unknown[] }[] }): number {
  return resp.repos.reduce((n, r) => n + r.files.length, 0)
}

/**
 * Fetch the full diff for a session and put it on screen. Concurrent or
 * superseded loads are dropped rather than allowed to overwrite a newer one.
 */
export async function loadDiff(sessionId: string): Promise<void> {
  if (!sessionId) {
    diffData.set(null)
    return
  }
  loadedFor = sessionId
  diffLoading.set(true)
  diffError.set('')
  try {
    const resp = await api.getSessionDiff(sessionId)
    if (loadedFor !== sessionId) return
    diffData.set(resp)
    diffBadge.update(m => ({ ...m, [sessionId]: countFiles(resp) }))
  } catch (e: any) {
    if (loadedFor !== sessionId) return
    diffData.set(null)
    diffError.set(e?.message ?? 'failed to load diff')
  } finally {
    if (loadedFor === sessionId) diffLoading.set(false)
  }
}

/**
 * Refresh only the badge count. Runs the cheap summary query — status without
 * diff — because the panel is closed and nothing is being rendered.
 */
export async function refreshDiffBadge(sessionId: string): Promise<void> {
  if (!sessionId) return
  try {
    const resp = await api.getSessionDiffSummary(sessionId)
    diffBadge.update(m => ({ ...m, [sessionId]: countFiles(resp) }))
  } catch {
    // A machine without git, or a session with no repository: leave the badge
    // as it was rather than flashing a zero.
  }
}

/** Replace one file with its complete diff, for the "show full file" action. */
export function replaceDiffFile(root: string, file: GitDiffFile): void {
  diffData.update(d => {
    if (!d) return d
    return {
      ...d,
      repos: d.repos.map(r => r.root !== root ? r : {
        ...r,
        files: r.files.map(f => f.path === file.path ? file : f),
      }),
    }
  })
}

/** Clear on session switch: the panel must never show the previous chat's diff. */
export function resetDiff(): void {
  loadedFor = ''
  diffData.set(null)
  diffError.set('')
  diffLoading.set(false)
}

/**
 * React to a finished turn. The panel being open decides which of the two
 * requests is worth making: the full diff to re-render, or the summary to move
 * the badge that calls the user back.
 */
export function onTurnEnded(sessionId: string): void {
  if (!sessionId || sessionId !== get(activeSessionId)) return
  if (get(panelContent) === 'diff') void loadDiff(sessionId)
  else void refreshDiffBadge(sessionId)
}

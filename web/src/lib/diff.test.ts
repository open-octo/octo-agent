import { describe, it, expect, beforeEach, vi, afterEach } from 'vitest'
import { get } from 'svelte/store'
import { activeSessionId, panelContent, readPanelMode, savePanelMode } from './stores'
import { diffBadge, diffData, diffError, loadDiff, onTurnEnded, refreshDiffBadge, replaceDiffFile, resetDiff } from './diff'
import * as api from './api'
import type { GitDiffFile, GitDiffResponse } from './types'

// jsdom exposes no localStorage under Node 26 (see unread.test.ts), and the
// panel-mode helpers persist through it.
const backing = new Map<string, string>()
vi.stubGlobal('localStorage', {
  getItem: (k: string) => (backing.has(k) ? backing.get(k)! : null),
  setItem: (k: string, v: string) => { backing.set(k, String(v)) },
  removeItem: (k: string) => { backing.delete(k) },
  clear: () => backing.clear(),
})

function file(path: string, over: Partial<GitDiffFile> = {}): GitDiffFile {
  return {
    path, status: 'M', staged: false, adds: 1, dels: 0, binary: false,
    truncated: false, omitted: false, total_lines: 1,
    patch: { old_path: path, new_path: path, hunks: [{ header: '@@ -1 +1 @@', lines: [{ kind: 'add', content: 'x' }] }] },
    ...over,
  }
}

function resp(...repos: { root: string; files: GitDiffFile[] }[]): GitDiffResponse {
  return {
    repos: repos.map(r => ({ root: r.root, name: r.root, branch: 'main', files: r.files })),
    truncated_files: 0,
    omitted_files: 0,
  }
}

beforeEach(() => {
  localStorage.clear()
  resetDiff()
  diffBadge.set({})
  activeSessionId.set(null)
  panelContent.set(null)
})

afterEach(() => {
  vi.restoreAllMocks()
})

describe('loadDiff', () => {
  it('puts the response on screen and counts every repository into the badge', async () => {
    vi.spyOn(api, 'getSessionDiff').mockResolvedValue(
      resp({ root: '/a', files: [file('one.ts'), file('two.ts')] }, { root: '/b', files: [file('three.ts')] }),
    )

    await loadDiff('s1')

    expect(get(diffData)?.repos).toHaveLength(2)
    // Multi-repo counts are summed: the badge answers "how much is waiting",
    // not "how much is waiting in one of them".
    expect(get(diffBadge).s1).toBe(3)
    expect(get(diffError)).toBe('')
  })

  it('surfaces a failure instead of leaving a stale diff on screen', async () => {
    vi.spyOn(api, 'getSessionDiff').mockResolvedValue(resp({ root: '/a', files: [file('one.ts')] }))
    await loadDiff('s1')
    expect(get(diffData)).not.toBeNull()

    vi.spyOn(api, 'getSessionDiff').mockRejectedValue(new Error('git not available'))
    await loadDiff('s1')

    expect(get(diffData)).toBeNull()
    expect(get(diffError)).toBe('git not available')
  })

  it('drops a superseded response so a slow load cannot overwrite a newer one', async () => {
    let releaseSlow: (r: GitDiffResponse) => void = () => {}
    const slow = new Promise<GitDiffResponse>(r => { releaseSlow = r })
    vi.spyOn(api, 'getSessionDiff')
      .mockImplementationOnce(() => slow)
      .mockImplementationOnce(() => Promise.resolve(resp({ root: '/b', files: [file('new.ts')] })))

    const first = loadDiff('s1')
    await loadDiff('s2')
    releaseSlow(resp({ root: '/a', files: [file('old.ts'), file('older.ts')] }))
    await first

    expect(get(diffData)?.repos[0].root).toBe('/b')
    expect(get(diffBadge).s1).toBeUndefined()
    expect(get(diffBadge).s2).toBe(1)
  })

  it('clears the diff for an empty session id rather than requesting one', async () => {
    const spy = vi.spyOn(api, 'getSessionDiff')
    await loadDiff('')
    expect(get(diffData)).toBeNull()
    expect(spy).not.toHaveBeenCalled()
  })
})

describe('refreshDiffBadge', () => {
  it('sums the summary counts without touching the rendered diff', async () => {
    vi.spyOn(api, 'getSessionDiffSummary').mockResolvedValue({
      repos: [
        { root: '/a', name: 'a', files: [{ path: 'x', status: 'M', staged: false }, { path: 'y', status: '?', staged: false }] },
        { root: '/b', name: 'b', files: [{ path: 'z', status: 'A', staged: true }] },
      ],
    })

    await refreshDiffBadge('s1')

    expect(get(diffBadge).s1).toBe(3)
    expect(get(diffData)).toBeNull()
  })

  it('leaves the previous count alone when the request fails', async () => {
    diffBadge.set({ s1: 4 })
    vi.spyOn(api, 'getSessionDiffSummary').mockRejectedValue(new Error('nope'))

    await refreshDiffBadge('s1')

    // Flashing a zero would read as "the agent undid its changes".
    expect(get(diffBadge).s1).toBe(4)
  })
})

describe('onTurnEnded', () => {
  it('reloads the full diff when the panel is open on that session', async () => {
    const full = vi.spyOn(api, 'getSessionDiff').mockResolvedValue(resp({ root: '/a', files: [file('one.ts')] }))
    const summary = vi.spyOn(api, 'getSessionDiffSummary')
    activeSessionId.set('s1')
    panelContent.set('diff')

    onTurnEnded('s1')
    await vi.waitFor(() => expect(full).toHaveBeenCalledWith('s1'))
    expect(summary).not.toHaveBeenCalled()
  })

  it('only refreshes the badge when the panel is closed', async () => {
    const full = vi.spyOn(api, 'getSessionDiff')
    const summary = vi.spyOn(api, 'getSessionDiffSummary').mockResolvedValue({ repos: [] })
    activeSessionId.set('s1')
    panelContent.set(null)

    onTurnEnded('s1')
    await vi.waitFor(() => expect(summary).toHaveBeenCalledWith('s1'))
    expect(full).not.toHaveBeenCalled()
  })

  it('ignores a turn in another session', () => {
    const full = vi.spyOn(api, 'getSessionDiff')
    const summary = vi.spyOn(api, 'getSessionDiffSummary')
    activeSessionId.set('s1')
    panelContent.set('diff')

    onTurnEnded('s2')

    expect(full).not.toHaveBeenCalled()
    expect(summary).not.toHaveBeenCalled()
  })
})

describe('replaceDiffFile', () => {
  it('swaps one file in one repository and leaves the rest untouched', async () => {
    vi.spyOn(api, 'getSessionDiff').mockResolvedValue(
      resp(
        { root: '/a', files: [file('one.ts', { truncated: true }), file('two.ts')] },
        { root: '/b', files: [file('one.ts')] },
      ),
    )
    await loadDiff('s1')

    replaceDiffFile('/a', file('one.ts', { total_lines: 99 }))

    const d = get(diffData)!
    expect(d.repos[0].files[0].total_lines).toBe(99)
    expect(d.repos[0].files[0].truncated).toBe(false)
    expect(d.repos[0].files[1].total_lines).toBe(1)
    // Same relative path in another repository must not be swapped too.
    expect(d.repos[1].files[0].total_lines).toBe(1)
  })
})

describe('panel mode persistence', () => {
  it('defaults to artifacts and remembers a switch to diff', () => {
    expect(readPanelMode()).toBe('session')
    savePanelMode('diff')
    expect(readPanelMode()).toBe('diff')
    savePanelMode('session')
    expect(readPanelMode()).toBe('session')
  })
})

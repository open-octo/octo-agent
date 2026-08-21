// Render-level coverage for the Git Diff panel body. Possible because
// vitest.config.ts sets resolve.conditions: ['browser'] — see
// src/lib/genui/components.test.ts.
import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest'
import { mount, unmount, flushSync } from 'svelte'
import DiffView from './DiffView.svelte'
import { activeSessionId } from '../../lib/stores'
import { diffBadge, diffData, diffError, resetDiff } from '../../lib/diff'
import * as api from '../../lib/api'
import type { GitDiffFile, GitDiffResponse } from '../../lib/types'

let target: HTMLElement
let app: Record<string, unknown> | null = null

beforeEach(() => {
  resetDiff()
  diffBadge.set({})
  activeSessionId.set('s1')
  target = document.createElement('div')
  document.body.appendChild(target)
})

afterEach(() => {
  if (app) unmount(app)
  app = null
  target.remove()
  vi.restoreAllMocks()
})

function file(path: string, over: Partial<GitDiffFile> = {}): GitDiffFile {
  return {
    path, status: 'M', staged: false, adds: 1, dels: 1, binary: false,
    truncated: false, omitted: false, total_lines: 2,
    patch: {
      old_path: path, new_path: path,
      hunks: [{ header: '@@ -1 +1 @@', lines: [{ kind: 'del', content: 'old' }, { kind: 'add', content: 'new' }] }],
    },
    ...over,
  }
}

function show(resp: GitDiffResponse | null, err = '') {
  diffData.set(resp)
  diffError.set(err)
  app = mount(DiffView, { target })
  flushSync()
  return target
}

function oneRepo(files: GitDiffFile[], over: Record<string, unknown> = {}): GitDiffResponse {
  return {
    repos: [{ root: '/repo', name: 'repo', branch: 'main', files, ...over }],
    truncated_files: 0,
    omitted_files: 0,
  }
}

describe('empty states', () => {
  it('says there is no repository when the response has none', () => {
    const el = show({ repos: [], truncated_files: 0, omitted_files: 0 })
    expect(el.textContent).toContain('no git repository')
  })

  it('says the tree is clean when a repository came back with no files', () => {
    const el = show(oneRepo([]))
    expect(el.textContent).toContain('clean')
  })

  it('shows the error instead of an empty state when the load failed', () => {
    const el = show(null, 'git not available')
    expect(el.textContent).toContain('git not available')
  })
})

describe('repository grouping', () => {
  it('omits the group header for a single repository', () => {
    const el = show(oneRepo([file('a.ts')]))
    expect(el.querySelector('.repo-head')).toBeNull()
  })

  it('adds a group header per repository once there are several', () => {
    const el = show({
      repos: [
        { root: '/one', name: 'one', branch: 'main', files: [file('a.ts')] },
        { root: '/two', name: 'two', branch: 'side', files: [file('b.ts')] },
      ],
      truncated_files: 0,
      omitted_files: 0,
    })
    expect(el.querySelectorAll('.repo-head')).toHaveLength(2)
  })

  it('surfaces a repository that failed without dropping it', () => {
    const el = show(oneRepo([], { error: 'git status timed out' }))
    expect(el.textContent).toContain('git status timed out')
  })
})

describe('hunk rendering', () => {
  it('renders added and removed lines with their own classes', () => {
    const el = show(oneRepo([file('a.ts')]))
    expect(el.querySelectorAll('.ln-add')).toHaveLength(1)
    expect(el.querySelectorAll('.ln-del')).toHaveLength(1)
    expect(el.querySelector('.hunk-head')?.textContent).toBe('@@ -1 +1 @@')
  })

  it('renders no hunk area for a file with no patch', () => {
    const el = show(oneRepo([file('logo.png', { binary: true, patch: null })]))
    expect(el.querySelectorAll('.ln')).toHaveLength(0)
    expect(el.textContent).toContain('Binary file')
  })

  it('marks a staged file', () => {
    const el = show(oneRepo([file('a.ts', { staged: true })]))
    expect(el.querySelector('.tag.staged')).not.toBeNull()
  })

  it('collapses a file when its header is clicked', () => {
    const el = show(oneRepo([file('a.ts')]))
    expect(el.querySelectorAll('.ln')).toHaveLength(2)

    el.querySelector<HTMLButtonElement>('.file-head')!.click()
    flushSync()

    expect(el.querySelectorAll('.ln')).toHaveLength(0)
  })
})

describe('truncated files', () => {
  it('offers the full file and swaps in what the single-file endpoint returns', async () => {
    const spy = vi.spyOn(api, 'getSessionFileDiff').mockResolvedValue(
      file('a.ts', {
        total_lines: 4,
        patch: {
          old_path: 'a.ts', new_path: 'a.ts',
          hunks: [{
            header: '@@ -1,4 +1,4 @@',
            lines: [
              { kind: 'add', content: '1' }, { kind: 'add', content: '2' },
              { kind: 'add', content: '3' }, { kind: 'add', content: '4' },
            ],
          }],
        },
      }),
    )
    const el = show(oneRepo([file('a.ts', { truncated: true, total_lines: 2500 })]))
    expect(el.textContent).toContain('2500')

    el.querySelector<HTMLButtonElement>('.note .link')!.click()
    await vi.waitFor(() => {
      // The store write happens a microtask after the request resolves, so the
      // flush has to live inside the retry rather than run once after it.
      flushSync()
      expect(el.querySelectorAll('.ln-add')).toHaveLength(4)
    })

    expect(spy).toHaveBeenCalledWith('s1', '/repo', 'a.ts')
    expect(el.querySelector('.note')).toBeNull()
  })

  it('offers the full file for an omitted one, which renders no hunks of its own', () => {
    const el = show(oneRepo([file('a.ts', { omitted: true, patch: null })]))
    expect(el.querySelectorAll('.ln')).toHaveLength(0)
    expect(el.querySelector('.note .link')).not.toBeNull()
  })
})

describe('jump bar', () => {
  it('stays out of the way for a single file and appears for several', () => {
    app = mount(DiffView, { target })
    diffData.set(oneRepo([file('a.ts')]))
    flushSync()
    expect(target.querySelector('.jump')).toBeNull()

    diffData.set(oneRepo([file('a.ts'), file('b.ts')]))
    flushSync()
    expect(target.querySelectorAll('.jump-chip')).toHaveLength(2)
  })
})

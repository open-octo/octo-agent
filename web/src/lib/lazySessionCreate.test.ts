import { describe, it, expect, vi, beforeEach } from 'vitest'
import { get } from 'svelte/store'
import type { SessionGroup } from './types'

// The whole point of these entry points is that they touch the network exactly
// never, so the api module is mocked and every call to it is a failure.
vi.mock('./api', () => ({
  createSession: vi.fn(() => {
    throw new Error('createSession must not be called before the first message')
  }),
  createSessionGroup: vi.fn(async (name: string, project?: { working_dir?: string }) => ({
    id: 'g-new',
    name,
    session_ids: [],
    ...(project?.working_dir ? { working_dir: project.working_dir } : {}),
  })),
}))

import * as api from './api'
import {
  createNewSession,
  createSessionInGroup,
  clearPendingSessionOpts,
  resolveProjectForDir,
  activeSessionId,
  view,
  sessionGroups,
  pendingAgent,
  pendingGroupId,
  pendingWorkingDir,
  pendingModel,
} from './stores'

const group = (id: string, working_dir?: string): SessionGroup =>
  ({ id, name: id, session_ids: [], ...(working_dir ? { working_dir } : {}) }) as SessionGroup

beforeEach(() => {
  vi.clearAllMocks()
  activeSessionId.set('some-old-session')
  view.set('agents')
  sessionGroups.set([])
  pendingAgent.set('')
  pendingGroupId.set('')
  pendingWorkingDir.set('')
  pendingModel.set('')
})

describe('createNewSession', () => {
  // The regression this guards: it used to POST on click, leaving an empty
  // session on disk and a dead row in the sidebar for every stray press of the
  // button, ⌘N, or the desktop tray item.
  it('creates nothing and opens the landing page', () => {
    createNewSession()
    expect(api.createSession).not.toHaveBeenCalled()
    expect(api.createSessionGroup).not.toHaveBeenCalled()
    expect(get(activeSessionId)).toBeNull()
    expect(get(view)).toBe('chat')
  })

  it('parks an agent pick for the session that does not exist yet', () => {
    createNewSession('expert-7')
    expect(get(pendingAgent)).toBe('expert-7')
    expect(api.createSession).not.toHaveBeenCalled()
  })

  // Abandoning one landing page and opening another must not inherit the first
  // one's picks — they would silently apply to an unrelated session.
  it('clears the previous picks on every fresh start', () => {
    createNewSession('expert-7')
    pendingWorkingDir.set('/work/app')
    pendingModel.set('ep::gpt')

    createNewSession()
    expect(get(pendingAgent)).toBe('')
    expect(get(pendingWorkingDir)).toBe('')
    expect(get(pendingModel)).toBe('')
    expect(get(pendingGroupId)).toBe('')
  })
})

describe('createSessionInGroup', () => {
  it('creates nothing and docks the group instead', () => {
    createSessionInGroup('g-42')
    expect(api.createSession).not.toHaveBeenCalled()
    expect(get(pendingGroupId)).toBe('g-42')
    expect(get(activeSessionId)).toBeNull()
    expect(get(view)).toBe('chat')
  })

  it('does not inherit a directory picked on an earlier landing page', () => {
    createNewSession()
    pendingWorkingDir.set('/work/app')
    createSessionInGroup('g-42')
    expect(get(pendingWorkingDir)).toBe('')
  })
})

describe('resolveProjectForDir', () => {
  it('reuses the project that already owns the directory', async () => {
    sessionGroups.set([group('g-1', '/work/app'), group('g-2', '/work/other')])
    expect(await resolveProjectForDir('/work/app')).toBe('g-1')
    expect(api.createSessionGroup).not.toHaveBeenCalled()
  })

  // Both pickers hand back absolute paths, but a trailing slash is the one
  // shape difference they can produce, and it must not spawn a second project
  // for a directory that already has one.
  it('matches across a trailing slash', async () => {
    sessionGroups.set([group('g-1', '/work/app')])
    expect(await resolveProjectForDir('/work/app/')).toBe('g-1')
    expect(api.createSessionGroup).not.toHaveBeenCalled()
  })

  it('ignores a plain group that happens to have no directory', async () => {
    sessionGroups.set([group('g-plain')])
    const id = await resolveProjectForDir('/work/app')
    expect(id).toBe('g-new')
    expect(api.createSessionGroup).toHaveBeenCalledWith('app', { source_dirs: ['/work/app'] })
  })

  it('creates a project named after the directory and records it locally', async () => {
    const id = await resolveProjectForDir('/work/app')
    expect(id).toBe('g-new')
    expect(api.createSessionGroup).toHaveBeenCalledWith('app', { source_dirs: ['/work/app'] })
    // Recorded without a refetch, so the sidebar shows the project immediately.
    expect(get(sessionGroups).map(g => g.id)).toContain('g-new')
  })

  it('propagates a creation failure rather than filing the session loose', async () => {
    vi.mocked(api.createSessionGroup).mockRejectedValueOnce(new Error('nope'))
    await expect(resolveProjectForDir('/work/app')).rejects.toThrow('nope')
    expect(get(sessionGroups)).toEqual([])
  })
})

describe('clearPendingSessionOpts', () => {
  // A docked group leaves no mark on the landing page, so a pick that survives
  // into an unrelated session files that session somewhere the user never saw
  // named. Every route back to the landing page — deleting the open session, a
  // session_deleted broadcast — calls this for that reason.
  it('drops every parked pick', () => {
    createSessionInGroup('g-42')
    pendingWorkingDir.set('/work/app')
    pendingAgent.set('expert-7')
    pendingModel.set('ep::gpt')

    clearPendingSessionOpts()

    expect(get(pendingGroupId)).toBe('')
    expect(get(pendingWorkingDir)).toBe('')
    expect(get(pendingAgent)).toBe('')
    expect(get(pendingModel)).toBe('')
  })
})

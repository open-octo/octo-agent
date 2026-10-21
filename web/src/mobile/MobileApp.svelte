<script lang="ts">
  // octo-mobile root view tree. Rendered instead of the desktop App layout when
  // `mobileShell` is true (see App.svelte). Batch 0 is the shell only: bottom
  // tab bar + neutral tokens. The data-bound feed, typed detail views, and the
  // other tabs are filled in by later batches (see
  // dev-docs/mobile-ui-implementation.md).
  import './theme.css'
  import Feed from './Feed.svelte'
  import SessionHistory from './SessionHistory.svelte'
  import ChatDetail from './ChatDetail.svelte'
  import ApprovalDetail from './ApprovalDetail.svelte'
  import TasksTab from './TasksTab.svelte'
  import ConfigTab from './ConfigTab.svelte'
  import NewTask from './NewTask.svelte'
  import ProjectSessions from './ProjectSessions.svelte'
  import SessionActions from './SessionActions.svelte'
  import QuestionOverlay from './QuestionOverlay.svelte'
  import type { FeedKind } from './feedGroups'
  import type { Attachment } from '../lib/composerDrafts'
  import { onMount } from 'svelte'
  import { setActiveSession, sessionGroups, pinnedSessions, collapsedSessions } from '../lib/stores'
  import * as api from '../lib/api'
  import { t } from '../lib/i18n'

  type Tab = 'chat' | 'tasks' | 'me'
  let tab = $state<Tab>('chat')
  // The session opened into a detail view (null = feed), plus which typed detail
  // to show, taken from the tapped card's kind.
  let openId = $state<string | null>(null)
  let openKind = $state<FeedKind | null>(null)
  // New-task sheet (FAB) + the first message it queued, bound to the session
  // it was queued for — opening any other session must never inherit it.
  let newTask = $state(false)
  // Project preselected in the new-task sheet ('' = none).
  let newTaskGroup = $state('')
  // Open project page (null = closed). Like history, it stays open under a
  // session opened from it.
  let project = $state<string | null>(null)
  // History / search page (null = closed). It stays open under a session
  // opened from it, so backing out of the session returns to the list.
  let history = $state<{ focus: boolean; query: string } | null>(null)
  let initial = $state<{ id: string; prompt: string; files: Attachment[] } | null>(null)

  function openSession(id: string, kind: FeedKind) {
    newTask = false
    if (initial && initial.id !== id) initial = null
    setActiveSession(id)
    openId = id
    openKind = kind
  }
  function startNew(groupId = '') {
    newTaskGroup = groupId
    newTask = true
  }

  // The desktop fetches the group registry when its sidebar mounts; the phone
  // has no sidebar, so fetch it here. App.svelte's WS handlers keep it current.
  onMount(() => {
    api.listSessionGroups().then(org => {
      sessionGroups.set(org.groups)
      pinnedSessions.set(org.pinned)
      collapsedSessions.set(org.collapsed)
    }).catch(() => { /* non-critical: the feed just shows no projects */ })
  })

  function closeDetail() {
    openId = null
    openKind = null
    initial = null
  }
</script>

<div class="m-root">
  <main class="m-view">
    {#if tab === 'chat'}
      {#if newTask}
        <NewTask
          groupId={newTaskGroup}
          onCancel={() => (newTask = false)}
          onCreated={(id, prompt, files) => { const send = !!prompt || files.length > 0; newTask = false; openSession(id, send ? 'running' : 'done'); if (send) initial = { id, prompt, files } }}
        />
      {:else if openId}
        {#if openKind === 'approval'}
          <ApprovalDetail onBack={closeDetail} />
        {:else}
          <ChatDetail
            onBack={closeDetail}
            onViewApproval={() => (openKind = 'approval')}
            {initial}
            onInitialSent={() => (initial = null)}
          />
        {/if}
      {:else if history}
        <SessionHistory
          focus={history.focus}
          bind:query={history.query}
          onOpen={(id, kind) => { history!.focus = false; openSession(id, kind) }}
          onBack={() => (history = null)}
        />
      {:else if project}
        <ProjectSessions
          groupId={project}
          onOpen={openSession}
          onNew={startNew}
          onBack={() => (project = null)}
        />
      {:else}
        <Feed
          onOpen={openSession}
          onNew={() => startNew()}
          onHistory={(focus) => (history = { focus, query: '' })}
          onProject={(id) => (project = id)}
        />
      {/if}
    {:else if tab === 'tasks'}
      <TasksTab
        onOpenSession={(id) => { tab = 'chat'; openSession(id, 'running') }}
        onAgentSession={(id, prompt) => { tab = 'chat'; openSession(id, 'running'); initial = { id, prompt, files: [] } }}
      />
    {:else}
      <ConfigTab onAgentSession={(id, prompt) => { tab = 'chat'; openSession(id, prompt ? 'running' : 'done'); if (prompt) initial = { id, prompt, files: [] } }} />
    {/if}
  </main>

  <nav class="m-tabbar">
    <button class="m-tab" class:on={tab === 'chat'} onclick={() => { tab = 'chat'; closeDetail(); history = null; project = null }}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 11.5a8.5 8.5 0 0 1-12.3 7.6L3 21l1.9-5.7A8.5 8.5 0 1 1 21 11.5z"/></svg>
      <span>{$t('m.tab_sessions')}</span>
    </button>
    <button class="m-tab" class:on={tab === 'tasks'} onclick={() => (tab = 'tasks')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/></svg>
      <span>{$t('m.tab_tasks')}</span>
    </button>
    <button class="m-tab" class:on={tab === 'me'} onclick={() => (tab = 'me')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0 1 16 0"/></svg>
      <span>{$t('m.tab_me')}</span>
    </button>
  </nav>

  <!-- Cross-client ask: with global broadcast, a question from any session
       (desktop or mobile) reaches every connected client. -->
  <QuestionOverlay />
  <SessionActions />
</div>

<style>
  .m-root {
    position: fixed;
    inset: 0;
    display: flex;
    flex-direction: column;
    background: var(--m-bg);
    color: var(--m-text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .m-view {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding-top: env(safe-area-inset-top);
    overflow: hidden;
    position: relative;
  }
  .m-tabbar {
    flex: none;
    display: flex;
    padding: 8px 4px calc(8px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border-top: 1px solid var(--m-border-2);
  }
  .m-tab {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding: 4px 0;
    font-size: 10px;
    color: var(--m-text-3);
    background: none;
    border: none;
    font-family: inherit;
    cursor: pointer;
  }
  .m-tab.on {
    color: var(--m-accent);
  }
</style>

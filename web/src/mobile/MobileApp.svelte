<script lang="ts">
  // octo-mobile root view tree. Rendered instead of the desktop App layout when
  // `mobileShell` is true (see App.svelte). Batch 0 is the shell only: bottom
  // tab bar + neutral tokens. The data-bound feed, typed detail views, and the
  // other tabs are filled in by later batches (see
  // dev-docs/mobile-ui-implementation.md).
  import './theme.css'
  import Feed from './Feed.svelte'
  import ChatDetail from './ChatDetail.svelte'
  import ApprovalDetail from './ApprovalDetail.svelte'
  import SettingsTab from './SettingsTab.svelte'
  import TasksTab from './TasksTab.svelte'
  import ConfigTab from './ConfigTab.svelte'
  import NewTask from './NewTask.svelte'
  import type { FeedKind } from './feedGroups'
  import { setActiveSession } from '../lib/stores'

  type Tab = 'chat' | 'tasks' | 'config' | 'settings'
  let tab = $state<Tab>('chat')
  // The session opened into a detail view (null = feed), plus which typed detail
  // to show, taken from the tapped card's kind.
  let openId = $state<string | null>(null)
  let openKind = $state<FeedKind | null>(null)
  // New-task sheet (FAB) + the first message it queued, bound to the session
  // it was queued for — opening any other session must never inherit it.
  let newTask = $state(false)
  let initial = $state<{ id: string; prompt: string } | null>(null)

  function openSession(id: string, kind: FeedKind) {
    newTask = false
    if (initial && initial.id !== id) initial = null
    setActiveSession(id)
    openId = id
    openKind = kind
  }
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
          onCancel={() => (newTask = false)}
          onCreated={(id, prompt) => { newTask = false; openSession(id, prompt ? 'running' : 'done'); if (prompt) initial = { id, prompt } }}
        />
      {:else if openId}
        {#if openKind === 'approval'}
          <ApprovalDetail onBack={closeDetail} />
        {:else}
          <ChatDetail
            onBack={closeDetail}
            onViewApproval={() => (openKind = 'approval')}
            initialPrompt={initial?.id === openId ? initial.prompt : ''}
            onInitialSent={() => (initial = null)}
          />
        {/if}
      {:else}
        <Feed onOpen={openSession} onNew={() => (newTask = true)} />
      {/if}
    {:else if tab === 'tasks'}
      <TasksTab onOpenSession={(id) => { tab = 'chat'; openSession(id, 'running') }} />
    {:else if tab === 'config'}
      <ConfigTab />
    {:else}
      <SettingsTab />
    {/if}
  </main>

  <nav class="m-tabbar">
    <button class="m-tab" class:on={tab === 'chat'} onclick={() => { tab = 'chat'; closeDetail() }}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 11.5a8.5 8.5 0 0 1-12.3 7.6L3 21l1.9-5.7A8.5 8.5 0 1 1 21 11.5z"/></svg>
      <span>会话</span>
    </button>
    <button class="m-tab" class:on={tab === 'tasks'} onclick={() => (tab = 'tasks')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/></svg>
      <span>任务</span>
    </button>
    <button class="m-tab" class:on={tab === 'config'} onclick={() => (tab = 'config')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h6M14 18h6"/><circle cx="16" cy="6" r="2"/><circle cx="8" cy="12" r="2"/><circle cx="12" cy="18" r="2"/></svg>
      <span>配置</span>
    </button>
    <button class="m-tab" class:on={tab === 'settings'} onclick={() => (tab = 'settings')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-2.7 1.1V21a2 2 0 1 1-4 0v-.1A1.6 1.6 0 0 0 7 19.4a1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0-1.1-2.7H3a2 2 0 1 1 0-4h.1A1.6 1.6 0 0 0 4.6 7"/></svg>
      <span>设置</span>
    </button>
  </nav>
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

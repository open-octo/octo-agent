<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sessions, activeSessionId, showToast } from './lib/stores'
  import { ws, wsState } from './lib/ws'
  import * as api from './lib/api'
  import Header from './components/layout/Header.svelte'
  import Sidebar from './components/layout/Sidebar.svelte'
  import ChatView from './views/ChatView.svelte'
  import SkillsView from './views/SkillsView.svelte'
  import TasksView from './views/TasksView.svelte'
  import McpView from './views/McpView.svelte'
  import ChannelsView from './views/ChannelsView.svelte'
  import SettingsView from './views/SettingsView.svelte'
  import ProfileView from './views/ProfileView.svelte'
  import FileRecallView from './views/FileRecallView.svelte'
  import CommandPalette from './components/overlays/CommandPalette.svelte'
  import McpModal from './components/overlays/McpModal.svelte'
  import ConfirmModal from './components/overlays/ConfirmModal.svelte'
  import QuestionModal from './components/overlays/QuestionModal.svelte'
  import FeedbackModal from './components/overlays/FeedbackModal.svelte'
  import Toast from './components/overlays/Toast.svelte'

  onMount(async () => {
    // Connect WebSocket
    ws.connect()

    // session_list event populates the sidebar
    ws.on('session_list', (ev: any) => {
      sessions.set(ev.sessions ?? [])
      // Auto-select first session
      if (!$activeSessionId && ev.sessions?.length > 0) {
        activeSessionId.set(ev.sessions[0].id)
      }
    })

    // session_update: patch the session in the list
    ws.on('session_update', (ev: any) => {
      sessions.update(list =>
        list.map(s => s.id === ev.session_id
          ? { ...s, status: ev.status ?? s.status, context_usage: ev.context_usage ?? s.context_usage }
          : s
        )
      )
    })

    // session_deleted: remove from list
    ws.on('session_deleted', (ev: any) => {
      sessions.update(list => list.filter(s => s.id !== ev.session_id))
      if ($activeSessionId === ev.session_id) {
        activeSessionId.set(null)
      }
    })

    // Also load sessions from REST as immediate fallback (WS session_list may be delayed)
    try {
      const data = await api.listSessions() as any
      if (data.sessions?.length > 0) {
        sessions.set(data.sessions)
        if (!$activeSessionId) {
          activeSessionId.set(data.sessions[0].id)
        }
      }
    } catch (e) {
      // non-critical: WS session_list will arrive shortly
    }

    return () => ws.disconnect()
  })
</script>

<div class="app">
  <Header />
  <div class="content">
    <Sidebar />
    <main class="main">
      {#if $view === 'chat'}
        <ChatView />
      {:else if $view === 'skills'}
        <SkillsView />
      {:else if $view === 'tasks'}
        <TasksView />
      {:else if $view === 'mcp'}
        <McpView />
      {:else if $view === 'channels'}
        <ChannelsView />
      {:else if $view === 'settings'}
        <SettingsView />
      {:else if $view === 'profile'}
        <ProfileView />
      {:else if $view === 'files'}
        <FileRecallView />
      {/if}
    </main>
  </div>
</div>

<CommandPalette />
<McpModal />
<ConfirmModal />
<QuestionModal />
<FeedbackModal />
<Toast />

<!-- WS reconnect banner lives in Header, driven by wsState store -->

<style>
.app {
  height: 100vh;
  display: flex;
  flex-direction: column;
  background: #F5F5F5;
  overflow: hidden;
}
.content {
  flex: 1;
  display: flex;
  min-height: 0;
}
.main {
  flex: 1;
  min-width: 0;
  display: flex;
  flex-direction: column;
  min-height: 0;
}
</style>

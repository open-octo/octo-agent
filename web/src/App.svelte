<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sessions, activeSessionId, showToast, onboardPhase, openAgentSession } from './lib/stores'
  import { ws, wsState } from './lib/ws'
  import { locale } from './lib/i18n'
  import { get } from 'svelte/store'
  import * as api from './lib/api'
  import FirstRunSetup from './components/overlays/FirstRunSetup.svelte'
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

  let booted = false

  // ── URL routing ─────────────────────────────────────────────────────────────
  // Reflect the current view (and active chat session) in the hash so a refresh
  // lands back where the user was instead of the default chat view.
  let routeReady = false
  const VALID_VIEWS = ['chat', 'skills', 'tasks', 'mcp', 'channels', 'settings', 'profile', 'files']

  function applyHash() {
    const h = location.hash.replace(/^#\/?/, '')
    if (!h) return
    const [v, ...rest] = h.split('/')
    if (!VALID_VIEWS.includes(v)) return
    if (get(view) !== v) view.set(v)
    if (v === 'chat' && rest[0]) {
      const sid = decodeURIComponent(rest[0])
      if (get(activeSessionId) !== sid) activeSessionId.set(sid)
    }
  }

  onMount(async () => {
    // First-run gate: decide the onboard phase BEFORE booting the main UI so it
    // never flashes behind the setup panel. Default to '' on error so a status
    // hiccup doesn't trap a configured user behind a blank splash.
    try {
      const status = await api.getOnboardStatus()
      onboardPhase.set(status.phase ?? '')
    } catch {
      onboardPhase.set('')
    }
    return () => ws.disconnect()
  })

  // Boot the normal UI once onboarding doesn't block it. 'key_setup' holds here
  // until FirstRunSetup completes and flips the phase to ''.
  $effect(() => {
    const phase = $onboardPhase
    if (booted || phase === 'unknown' || phase === 'key_setup') return
    booted = true
    bootMain()
    if (phase === 'soul_setup') maybeLaunchOnboard()
  })

  // Write the current view/session to the hash on navigation (once the initial
  // hash has been restored, and only while the main UI is showing).
  $effect(() => {
    const v = $view, sid = $activeSessionId, phase = $onboardPhase
    if (!routeReady || phase === 'unknown' || phase === 'key_setup') return
    const hash = v === 'chat' ? (sid ? `#/chat/${encodeURIComponent(sid)}` : '#/chat') : `#/${v}`
    if (location.hash !== hash) location.hash = hash
  })

  function bootMain() {
    ws.connect()

    ws.on('session_list', (ev: any) => {
      sessions.set(ev.sessions ?? [])
      if (!get(activeSessionId) && ev.sessions?.length > 0) {
        activeSessionId.set(ev.sessions[0].id)
      }
    })

    ws.on('session_update', (ev: any) => {
      sessions.update(list =>
        list.map(s => s.id === ev.session_id
          ? { ...s, status: ev.status ?? s.status, context_usage: ev.context_usage ?? s.context_usage }
          : s
        )
      )
    })

    ws.on('session_deleted', (ev: any) => {
      sessions.update(list => list.filter(s => s.id !== ev.session_id))
      if (get(activeSessionId) === ev.session_id) {
        activeSessionId.set(null)
      }
    })

    // REST fallback (WS session_list may be delayed)
    api.listSessions().then((data: any) => {
      if (data.sessions?.length > 0) {
        sessions.set(data.sessions)
        if (!get(activeSessionId)) activeSessionId.set(data.sessions[0].id)
      }
    }).catch(() => { /* non-critical: WS session_list will arrive shortly */ })

    // Restore the view/session from the URL now — synchronously, before the
    // WS/REST auto-select above resolves (both guard on activeSessionId being
    // unset, so this wins). Then start tracking forward/back + manual edits.
    applyHash()
    routeReady = true
    window.addEventListener('hashchange', applyHash)
  }

  // soul_setup: key present, soul.md missing → auto-launch one /onboard chat.
  // Guarded by sessionStorage so a refresh doesn't spawn a second session.
  function maybeLaunchOnboard() {
    if (sessionStorage.getItem('octo-onboard-launched')) return
    sessionStorage.setItem('octo-onboard-launched', '1')
    const lang = get(locale).startsWith('zh') ? 'zh' : 'en'
    openAgentSession(`/onboard lang:${lang}`, '✨ Onboard').catch(() => {})
  }
</script>

{#if $onboardPhase === 'unknown'}
  <div class="splash"><div class="spinner"></div></div>
{:else if $onboardPhase === 'key_setup'}
  <FirstRunSetup />
{:else}
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
{/if}

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
  background: var(--bg-layout);
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
.splash {
  height: 100vh; display: flex; align-items: center; justify-content: center;
  background: var(--bg-layout);
}
.splash .spinner {
  width: 28px; height: 28px; border: 3px solid rgba(22,119,255,0.2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: octo-spin 0.7s linear infinite;
}
@keyframes octo-spin { to { transform: rotate(360deg); } }
</style>

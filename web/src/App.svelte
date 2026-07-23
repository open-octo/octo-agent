<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sessions, sessionGroups, activeSessionId, showToast, onboardPhase, openAgentSession, chatShowReasoning, globalPermissionMode, nativeShell, mobileShell } from './lib/stores'
  import MobileApp from './mobile/MobileApp.svelte'
  import { ws, wsState } from './lib/ws'
  import { notificationsEnabled } from './lib/notifications'
  import { locale, t, tr, setLocale } from './lib/i18n'
  import { checkAuth } from './lib/auth'
  import { get } from 'svelte/store'
  import * as api from './lib/api'
  import { installExternalLinkInterceptor } from './lib/externalLinks'
  import AuthGate from './components/overlays/AuthGate.svelte'
  import FirstRunSetup from './components/overlays/FirstRunSetup.svelte'
  import Header from './components/layout/Header.svelte'
  import Sidebar from './components/layout/Sidebar.svelte'
  import ChatView from './views/ChatView.svelte'
  import SkillsView from './views/SkillsView.svelte'
  import WorkflowsView from './views/WorkflowsView.svelte'
  import BrowserView from './views/BrowserView.svelte'
  import TasksView from './views/TasksView.svelte'
  import McpView from './views/McpView.svelte'
  import ChannelsView from './views/ChannelsView.svelte'
  import SettingsView from './views/SettingsView.svelte'
  import ProfileView from './views/ProfileView.svelte'
  import FileRecallView from './views/FileRecallView.svelte'
  import CommandPalette from './components/overlays/CommandPalette.svelte'
  import McpModal from './components/overlays/McpModal.svelte'
  import ConfirmModal from './components/overlays/ConfirmModal.svelte'
  import ConfirmDialog from './components/overlays/ConfirmDialog.svelte'
  import ArtifactModal from './components/ArtifactModal.svelte'
  import FeedbackModal from './components/overlays/FeedbackModal.svelte'
  import Toast from './components/overlays/Toast.svelte'

  let booted = false
  // Set when the server requires an access key the user couldn't provide; the
  // app shows a denied splash instead of booting.
  let authDenied = $state(false)

  // ── URL routing ─────────────────────────────────────────────────────────────
  // Reflect the current view (and active chat session) in the hash so a refresh
  // lands back where the user was instead of the default chat view.
  let routeReady = false
  const VALID_VIEWS = ['chat', 'skills', 'workflows', 'browser', 'tasks', 'mcp', 'channels', 'settings', 'profile', 'files']

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

  onMount(() => {
    // Access-key gate, BEFORE any gated call. Loopback visits pass instantly
    // (the server exempts them); a networked server without a valid key prompts
    // via the AuthGate overlay. A denied result blocks boot with a message.
    let cancelled = false
    // Desktop shell: send http(s) link clicks to the system browser (the
    // webview can't open target="_blank" itself). Inert in a real browser.
    const uninstallLinks = installExternalLinkInterceptor()
    const cleanup = () => { cancelled = true; uninstallLinks(); ws.disconnect() }
    checkAuth().then(async ok => {
      if (cancelled) return
      if (!ok) {
        authDenied = true
        return
      }
      // First-run gate: decide the onboard phase BEFORE booting the main UI so it
      // never flashes behind the setup panel. Default to '' on error so a status
      // hiccup doesn't trap a configured user behind a blank splash.
      try {
        const status = await api.getOnboardStatus()
        onboardPhase.set(status.phase ?? '')
      } catch {
        onboardPhase.set('')
      }
    })
    return cleanup
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

    // Restore the persisted UI language from server config so a refresh
    // keeps the user's locale choice. Also seed globalPermissionMode from the
    // default model entry, so the Composer's no-active-session fallback
    // shows the real configured default instead of a hardcoded guess.
    api.getConfig().then(cfg => {
      if (cfg.language) setLocale(cfg.language)
      // PR5: permission_mode is global (was per-default-entry before). The
      // Composer reads this to seed its no-active-session fallback.
      if (cfg.permission_mode) globalPermissionMode.set(cfg.permission_mode)
    }).catch(() => { /* non-critical */ })

    ws.on('session_list', (ev: any) => {
      const list = ev.sessions ?? []
      sessions.set(list)
      chatShowReasoning.update(m => {
        const next = { ...m }
        for (const s of list) {
          if (typeof s.show_reasoning === 'boolean') next[s.id] = s.show_reasoning
        }
        return next
      })
      if (!get(activeSessionId) && list.length > 0) {
        activeSessionId.set(list[0].id)
      }
    })

    ws.on('session_update', (ev: any) => {
      // permission_mode is per-session (each session has its own, only
      // inheriting the global default at creation) — a mode change only
      // ever broadcasts to the one session it was changed on, so this stays
      // a plain per-session merge like every other field here.
      sessions.update(list =>
        list.map(s => s.id === ev.session_id
          ? {
              ...s,
              status: ev.status ?? s.status,
              context_usage: ev.context_usage ?? s.context_usage,
              show_reasoning: typeof ev.show_reasoning === 'boolean' ? ev.show_reasoning : s.show_reasoning,
              permission_mode: typeof ev.permission_mode === 'string' ? ev.permission_mode : s.permission_mode,
              reasoning_effort: typeof ev.reasoning_effort === 'string' ? ev.reasoning_effort : s.reasoning_effort,
            }
          : s
        )
      )
      if (typeof ev.show_reasoning === 'boolean') {
        chatShowReasoning.update(m => ({ ...m, [ev.session_id]: ev.show_reasoning }))
      }
    })

    ws.on('session_deleted', (ev: any) => {
      sessions.update(list => list.filter(s => s.id !== ev.session_id))
      if (get(activeSessionId) === ev.session_id) {
        activeSessionId.set(null)
        // A session deleted by another entry (e.g. another tab or the CLI)
        // should not leave the chat view stuck on a bound-to-another-entry
        // banner. Reset to the default chat landing.
        view.set('chat')
      }
    })

    // Pull the authoritative session list from the server into the stores.
    // Shared by the reconciliation paths below; never touches activeSessionId.
    const refreshSessionsFromServer = () => {
      api.listSessions().then((data: any) => {
        const list = data.sessions ?? []
        sessions.set(list)
        chatShowReasoning.update(m => {
          const next = { ...m }
          for (const s of list) {
            if (typeof s.show_reasoning === 'boolean') next[s.id] = s.show_reasoning
          }
          return next
        })
      }).catch(() => { /* non-critical: WS fast paths already ran */ })
    }

    // Auto-title: a global broadcast carrying the freshly generated name, so
    // the sidebar reflects the rename live instead of showing the stale title
    // until a reload.
    ws.on('session_renamed', (ev: any) => {
      if (!ev.name) return
      sessions.update(list =>
        list.map(s => s.id === ev.session_id
          ? { ...s, title: ev.name, name: ev.name }
          : s
        )
      )
      // Double-check against the server: the store mutation above is the fast
      // path, but if a slow-consumer drop or a UI reactivity gap hides the
      // rename, the next REST list will reconcile the sidebar.
      refreshSessionsFromServer()
    })

    // A session created outside this tab's own actions — a scheduled cron
    // fire filing a fresh session into its task's group, or a branch/fork
    // made in another tab. Every other broadcast about that session is
    // per-session and dropped for tabs that never subscribed to it, so this
    // is the one signal an open sidebar gets: refetch both the session list
    // AND the groups snapshot (which is otherwise only ever fetched once, at
    // sidebar mount) so the session appears already inside its group (#1699).
    ws.on('session_created', () => {
      refreshSessionsFromServer()
      api.listSessionGroups().then(org => sessionGroups.set(org.groups)).catch(() => { /* non-critical */ })
    })

    // session_activity is a lightweight global signal (unlike
    // request_user_question/session_update/complete, which only reach tabs
    // subscribed to that exact session) — it's how a tab looking at session B
    // learns that session A got a question or finished replying. Drives both
    // the sidebar's pending-question badge and the desktop notification.
    ws.on('session_activity', (ev: any) => {
      const sid = ev.session_id
      if (!sid) return
      if (ev.kind === 'question_pending' || ev.kind === 'question_resolved') {
        sessions.update(list => list.map(s =>
          s.id === sid ? { ...s, pending_question: ev.kind === 'question_pending' } : s
        ))
      }
      // Approval analogue — drives the mobile feed's needs-approval card for a
      // session the client isn't subscribed to.
      if (ev.kind === 'confirm_pending' || ev.kind === 'confirm_resolved') {
        sessions.update(list => list.map(s =>
          s.id === sid ? { ...s, pending_confirmation: ev.kind === 'confirm_pending' } : s
        ))
      }
      if (ev.kind === 'question_pending' || ev.kind === 'confirm_pending' || ev.kind === 'turn_complete') {
        notifyForSessionActivity(sid, ev.kind)
      }
    })

    // REST fallback (WS session_list may be delayed)
    api.listSessions().then((data: any) => {
      const list = data.sessions ?? []
      if (list.length > 0) {
        sessions.set(list)
        chatShowReasoning.update(m => {
          const next = { ...m }
          for (const s of list) {
            if (typeof s.show_reasoning === 'boolean') next[s.id] = s.show_reasoning
          }
          return next
        })
        if (!get(activeSessionId)) activeSessionId.set(list[0].id)
      }
    }).catch(() => { /* non-critical: WS session_list will arrive shortly */ })

    // Restore the view/session from the URL now — synchronously, before the
    // WS/REST auto-select above resolves (both guard on activeSessionId being
    // unset, so this wins). Then start tracking forward/back + manual edits.
    applyHash()
    routeReady = true
    window.addEventListener('hashchange', applyHash)
  }

  // Cooldown per (session, kind) — a session with a tight /loop interval
  // completes turns every 60s+ with no new user input each time, which would
  // otherwise fire a notification every single iteration. Keyed separately
  // per kind so a burst of turn_complete pings can't suppress a genuinely
  // distinct question_pending, or vice versa.
  const NOTIFY_COOLDOWN_MS = 5 * 60 * 1000
  const lastNotifiedAt: Record<string, number> = {}

  // Desktop notification for a session_activity the user isn't already
  // looking at in a focused tab — if they are, they'd see it happen live and
  // a notification would just be noise. No-op unless the user has the
  // Desktop Notifications preference on AND has granted browser permission.
  function notifyForSessionActivity(sid: string, kind: 'question_pending' | 'confirm_pending' | 'turn_complete') {
    if (!get(notificationsEnabled)) return
    const native = get(nativeShell)
    // The browser Notification API doesn't work in the desktop webview; native
    // mode routes to the OS via the bridge, so only gate on browser permission
    // for the browser path.
    if (!native && (!('Notification' in window) || Notification.permission !== 'granted')) return
    const viewingThisSession = document.hasFocus() && get(view) === 'chat' && get(activeSessionId) === sid
    if (viewingThisSession) return
    const cooldownKey = `${sid}:${kind}`
    const now = Date.now()
    if (now - (lastNotifiedAt[cooldownKey] ?? 0) < NOTIFY_COOLDOWN_MS) return
    lastNotifiedAt[cooldownKey] = now
    const sess = get(sessions).find(s => s.id === sid)
    const title = sess?.name || sess?.title || sid
    const bodyKey = kind === 'question_pending' ? 'header.notif_question_body'
      : kind === 'confirm_pending' ? 'header.notif_confirm_body'
      : 'header.notif_turn_complete_body'
    const body = tr(bodyKey)
    if (native) {
      // The native notification carries this session id so the desktop shell
      // routes to it on click. Browser path handles the click in-page below.
      api.nativeNotify(title, body, sid).catch(() => {})
      return
    }
    const n = new Notification(title, { body })
    n.onclick = () => {
      window.focus()
      activeSessionId.set(sid)
      view.set('chat')
      n.close()
    }
  }

  // soul_setup: key present, no identity yet → auto-launch one /onboard chat.
  // sessionStorage guards against a same-tab refresh spawning a second
  // session; markOnboardAttempted persists a server-side marker file so closing
  // the tab (or interrupting the chat) doesn't re-nudge on the next load either
  // — the server stops reporting phase 'soul_setup' once it's set (#1660). Await
  // the marker BEFORE opening the chat so an immediately-closed first run can't
  // race the write and re-nudge on reopen.
  async function maybeLaunchOnboard() {
    if (sessionStorage.getItem('octo-onboard-launched')) return
    sessionStorage.setItem('octo-onboard-launched', '1')
    await api.markOnboardAttempted().catch(() => {})
    const lang = get(locale).startsWith('zh') ? 'zh' : 'en'
    openAgentSession(`/onboard lang:${lang}`, '✨ Onboard').catch(() => {})
  }
</script>

{#if authDenied}
  <div class="splash splash-msg">{$t('auth.denied')}</div>
{:else if $onboardPhase === 'unknown'}
  <div class="splash"><div class="spinner"></div></div>
{:else if $onboardPhase === 'key_setup'}
  <FirstRunSetup />
{:else if mobileShell}
<MobileApp />
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
      {:else if $view === 'workflows'}
        <WorkflowsView />
      {:else if $view === 'browser'}
        <BrowserView />
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

<AuthGate />
<CommandPalette />
<McpModal />
<!-- Mobile approves via its own ApprovalDetail (web/src/mobile); suppress the
     desktop confirmation overlay there so it doesn't double up. -->
{#if !mobileShell}<ConfirmModal />{/if}
<ConfirmDialog />
<ArtifactModal />
<FeedbackModal />
<Toast />

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
.splash-msg {
  padding: 24px; text-align: center;
  font-size: 14px; line-height: 1.6; color: var(--text-secondary);
  max-width: 420px; margin: 0 auto;
}
.splash .spinner {
  width: 28px; height: 28px; border: 3px solid rgba(22,119,255,0.2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: octo-spin 0.7s linear infinite;
}
@keyframes octo-spin { to { transform: rotate(360deg); } }
</style>

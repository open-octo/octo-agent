<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sessions, sessionGroups, pinnedSessions, collapsedSessions, activeSessionId, onboardPhase, openAgentSession, chatShowReasoning, globalPermissionMode, nativeShell, mobileShell, panelContent, panelExpanded, cmdkOpen, settingsModalOpen, createNewSession, clearPendingSessionOpts, isDesktopShell } from './lib/stores'
  import MobileApp from './mobile/MobileApp.svelte'
  import { ws, wsState } from './lib/ws'
  import { notificationsEnabled } from './lib/notifications'
  import { locale, t, tr, setLocale } from './lib/i18n'
  import { checkAuth } from './lib/auth'
  import { get } from 'svelte/store'
  import * as api from './lib/api'
  import { installExternalLinkInterceptor } from './lib/externalLinks'
  import { startNativeHeartbeat } from './lib/nativeHeartbeat'
  import { normalizeHash, hashPicksChatTarget } from './lib/hashRouting'
  import { pruneSessions } from './lib/genui/panel-state'
  import { globalKeyIntent } from './lib/globalKeys'
  import AuthGate from './components/overlays/AuthGate.svelte'
  import FirstRunSetup from './components/overlays/FirstRunSetup.svelte'
  import Header from './components/layout/Header.svelte'
  import Sidebar from './components/layout/Sidebar.svelte'
  import AgentsView from './views/AgentsView.svelte'
  import ChatView from './views/ChatView.svelte'
  import SkillsView from './views/SkillsView.svelte'
  import WorkflowsView from './views/WorkflowsView.svelte'
  import BrowserView from './views/BrowserView.svelte'
  import TasksView from './views/TasksView.svelte'
  import McpView from './views/McpView.svelte'
  import ChannelsView from './views/ChannelsView.svelte'
  import LightAppsView from './views/LightAppsView.svelte'
  import CommandPalette from './components/overlays/CommandPalette.svelte'
  import McpModal from './components/overlays/McpModal.svelte'
  import SettingsModal from './components/overlays/SettingsModal.svelte'
  import ConfirmModal from './components/overlays/ConfirmModal.svelte'
  import ConfirmDialog from './components/overlays/ConfirmDialog.svelte'
  import ArtifactModal from './components/ArtifactModal.svelte'
  import ArtifactsPanel from './components/ArtifactsPanel.svelte'
  import FeedbackModal from './components/overlays/FeedbackModal.svelte'
  import Toast from './components/overlays/Toast.svelte'
  import { touchSession, markSessionSeen, sessionTouchedAt } from './lib/unread'

  // The session on screen is read by definition — this is the only place the
  // sidebar's unread dot gets cleared. It re-marks on every list change and
  // every observed turn ending rather than once on selection, so a reply that
  // lands while the user is watching (or a refetch carrying the server's newer
  // updated_at) can't leave a dot on the row they're looking at. Reading a
  // session means having its transcript open: parked on the tasks view with a
  // session still "active" behind it does not count, and switching back to
  // chat re-runs this and clears it then.
  $effect(() => {
    void $sessions; void $sessionTouchedAt
    const sid = $activeSessionId
    if ($view !== 'chat' || !sid) return
    markSessionSeen(sid)
  })

  let booted = false
  // Set when the server requires an access key the user couldn't provide; the
  // app shows a denied splash instead of booting.
  let authDenied = $state(false)

  // ── URL routing ─────────────────────────────────────────────────────────────
  // Reflect the current view (and active chat session) in the hash so a refresh
  // lands back where the user was instead of the default chat view.
  let routeReady = false
  // The "open the most recent session" fallback below fires at most once, on
  // the first session list of the boot — and not at all when the URL already
  // names the chat target. After that the active session belongs to the user:
  // a later list (another tab creating a session, a WS reconnect) must not
  // yank them off the new-session landing page, nor out of the empty state a
  // delete leaves behind. Read synchronously at module init, before any list
  // can arrive.
  let autoSelectDone = typeof location !== 'undefined' && hashPicksChatTarget(location.hash)
  const VALID_VIEWS = ['chat', 'agents', 'skills', 'workflows', 'browser', 'tasks', 'mcp', 'channels', 'lightapps']

  function applyHash() {
    const h = location.hash.replace(/^#\/?/, '')
    if (!h) return
    const [v, ...rest] = h.split('/')
    // The desktop shell's tray "Settings" item navigates to #settings; the
    // full-page view became a modal (settings modal redesign, PR #1955), so
    // route the hash to the modal instead of a VALID_VIEWS entry. Normalize
    // the hash back to the current view so a reload doesn't re-open the modal.
    if (v === 'settings') {
      settingsModalOpen.set(true)
      const hash = normalizeHash(get(view), get(activeSessionId))
      history.replaceState(null, '', location.pathname + location.search + hash)
      return
    }
    // The desktop tray's "New Session" item navigates to #new (see
    // nativeBridge.openNewSession). Desktop-gated: under a plain browser this
    // hash is unreachable from the shell, so don't let a hand-typed #new open
    // the landing page there. createNewSession() re-normalizes the hash to
    // #/chat, so a reload won't loop back into #new.
    if (v === 'new' && isDesktopShell) {
      // Claim the auto-select slot before opening the landing page. Reaching
      // #new from the tray with the window closed loads the page at this hash
      // from scratch, so the session list is still on its way; without this it
      // would arrive to an unset activeSessionId and helpfully open the most
      // recent session, throwing away the new-session the user just asked for.
      // (Nothing overwrites it any more — this used to be covered by
      // createNewSession awaiting a POST and setting the id itself.)
      autoSelectDone = true
      createNewSession()
      return
    }
    if (!VALID_VIEWS.includes(v)) return
    if (get(view) !== v) view.set(v)
    if (v === 'chat' && rest[0]) {
      const sid = decodeURIComponent(rest[0])
      if (get(activeSessionId) !== sid) activeSessionId.set(sid)
    }
  }

  // Navigating to a different view closes the artifacts panel — expanded, it
  // would otherwise keep covering the content area the new view just took
  // over (the main column stays display:none while the panel is expanded).
  // Guarded on an actual change: view.set re-fires subscribers on same-value
  // sets, and flows that open the panel without navigating (a Light App
  // opening over its own view, the header/palette toggles) must not be undone.
  let prevView = get(view)
  view.subscribe(v => {
    if (v === prevView) return
    prevView = v
    panelExpanded.set(false)
    panelContent.set(null)
  })

  onMount(() => {
    // Access-key gate, BEFORE any gated call. Loopback visits pass instantly
    // (the server exempts them); a networked server without a valid key prompts
    // via the AuthGate overlay. A denied result blocks boot with a message.
    let cancelled = false
    // Desktop shell: send http(s) link clicks to the system browser (the
    // webview can't open target="_blank" itself). Inert in a real browser.
    const uninstallLinks = installExternalLinkInterceptor()
    // Desktop shell: report page liveness so the shell can revive a dead or
    // black webview instead of foregrounding it. Inert in a real browser.
    const stopHeartbeat = startNativeHeartbeat()
    // Drop persisted GenUI panel state for sessions that no longer exist. The
    // session list is the natural GC point: it is when a deletion made
    // anywhere else first becomes visible to this client.
    //
    // Note this subscription fires immediately with the store's initial [],
    // before the list has been fetched. pruneSessions ignores an empty list
    // for exactly that reason — see the comment there.
    const stopPanelGC = sessions.subscribe(list => pruneSessions(list.map(s => s.id)))
    const cleanup = () => { cancelled = true; uninstallLinks(); stopHeartbeat(); stopPanelGC(); ws.disconnect() }
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
    const hash = normalizeHash(v, sid)
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
      if (!autoSelectDone) {
        autoSelectDone = true
        if (!get(activeSessionId) && list.length > 0) activeSessionId.set(list[0].id)
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
        clearPendingSessionOpts()
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

    // The group registry changed — in this tab or another (a group edit, a
    // membership move, a pin, a project's working directory or notes). The
    // groups snapshot is otherwise fetched once at sidebar mount, so without
    // this a project directory retargeted in one tab stays stale in every
    // other: the sidebar header keeps the old path and the composer chip of
    // member sessions (which derives from the groups store) misleads about
    // where tools actually run. The event carries no payload by design —
    // refetch, so there's no registry shape to mirror client-side.
    ws.on('session_groups_changed', () => {
      api.listSessionGroups().then(org => {
        sessionGroups.set(org.groups)
        pinnedSessions.set(org.pinned)
        collapsedSessions.set(org.collapsed)
      }).catch(() => { /* non-critical; next mount refetches */ })
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
      // Running-state pair — keeps the sidebar's activity spinner live for
      // sessions this tab isn't subscribed to (session_update carries status
      // only to subscribers).
      if (ev.kind === 'turn_started' || ev.kind === 'turn_ended') {
        sessions.update(list => list.map(s =>
          s.id === sid ? { ...s, status: ev.kind === 'turn_started' ? 'running' : 'idle' } : s
        ))
      }
      // A finished turn left something in that session to read. Stamped
      // unconditionally: the effect at the top of this file immediately
      // un-marks it again if it's the session on screen, and nothing else
      // refreshes updated_at for an open tab.
      if (ev.kind === 'turn_ended') touchSession(sid)
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
        if (!autoSelectDone) {
          autoSelectDone = true
          if (!get(activeSessionId)) activeSessionId.set(list[0].id)
        }
      }
    }).catch(() => { /* non-critical: WS session_list will arrive shortly */ })

    // Restore the view/session from the URL now — synchronously, before the
    // WS/REST auto-select above resolves (both guard on activeSessionId being
    // unset, and autoSelectDone already accounts for a chat hash, so this
    // wins). Then start tracking forward/back + manual edits.
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
    // When the user is in the app (document focused) but viewing a DIFFERENT
    // session, the in-app question note handles question_pending /
    // confirm_pending — don't also fire an OS notification, that's duplicate
    // noise. Only turn_complete surfaces as an OS notification while in-app
    // (there's no in-app note for it). When the user is away from the app
    // (tab hidden/minimized), all kinds fire as before.
    if (document.hasFocus() && (kind === 'question_pending' || kind === 'confirm_pending')) return
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
    openAgentSession(`/onboard lang:${lang}`, tr('onboard.session_title')).catch(() => {})
  }

  // Cmd/Ctrl+K toggles the command palette — the Header pill advertises the
  // shortcut, and it fires even while an input has focus (palette convention).
  // Cmd/Ctrl+N starts a new session — a native desktop-shell bind. Under a
  // plain browser Cmd+N is a reserved browser shortcut (new window) the page
  // never sees, so the handler is gated to the Wails shell (isDesktopShell)
  // where the keypress actually reaches the page and is safe to claim. The
  // key→intent mapping lives in lib/globalKeys for unit testing.
  function onGlobalKeydown(e: KeyboardEvent) {
    const intent = globalKeyIntent(e, { shell: isDesktopShell })
    if (intent === 'palette') {
      e.preventDefault()
      cmdkOpen.update(v => !v)
    } else if (intent === 'new-session') {
      e.preventDefault()
      createNewSession()
    }
  }
</script>

<svelte:window onkeydown={onGlobalKeydown} />

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
  <div class="content">
    <Sidebar />
    <!-- Yield the width only while the panel is actually there to take it:
         an expanded flag left over from a closed panel would hide the main
         column with nothing rendered beside it, i.e. a blank page. -->
    <main class="main" class:yielded={$panelExpanded && !!$panelContent}>
      <Header />
      {#if $view === 'chat'}
        <ChatView />
      {:else if $view === 'agents'}
        <AgentsView />
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
      {:else if $view === 'lightapps'}
        <LightAppsView />
      {/if}
    </main>
    {#if $panelContent}
      <ArtifactsPanel />
    {/if}
  </div>
</div>
{/if}

<AuthGate />
<CommandPalette />
<McpModal />
<SettingsModal />
<!-- Mobile approves via its own ApprovalDetail (web/src/mobile); suppress the
     desktop confirmation overlay there so it doesn't double up. -->
{#if !mobileShell}<ConfirmModal />{/if}
<ConfirmDialog />
<ArtifactModal />
<FeedbackModal />
<Toast />

<style>
/* height 100% (via the html/body/#app chain), NOT 100vh: viewport units are
   not compensated for the root zoom the font-size setting applies, so 100vh
   under zoom 1.1 paints 110% of the viewport and clips the composer. */
.app {
  height: 100%;
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
/* Gives its width to an expanded artifacts panel. display:none rather than
   unmounting the view: the conversation keeps its scroll position, its
   in-flight turn and its composer draft, so collapsing the panel comes back
   to exactly what was there. */
.main.yielded { display: none; }
.splash {
  height: 100%; display: flex; align-items: center; justify-content: center;
  background: var(--bg-layout);
}
.splash-msg {
  padding: 24px; text-align: center;
  font-size: 14px; line-height: 1.6; color: var(--text-secondary);
  max-width: 420px; margin: 0 auto;
}
.splash .spinner {
  width: 28px; height: 28px; border: 3px solid var(--blue-2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: octo-spin 0.7s linear infinite;
}
@keyframes octo-spin { to { transform: rotate(360deg); } }
</style>

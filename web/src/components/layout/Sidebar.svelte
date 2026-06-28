<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sidebar, sessions, activeSession, activeSessionId, selMode, sel, menuFor, editId, editDraft, showToast, mcpServers } from '../../lib/stores'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import VersionBadge from './VersionBadge.svelte'

  // Real count of configured MCP servers for the nav badge. Seeded here so the
  // badge is correct before the user ever opens the MCP panel; McpView keeps the
  // shared store in sync afterward.
  let mcpCount = $derived(($mcpServers as any[]).length)

  onMount(async () => {
    try {
      const d = await api.listMcpServers()
      mcpServers.set(d.servers as any)
    } catch { /* badge falls back to hidden */ }
  })

  $effect(() => {
    function onResize() {
      const w = window.innerWidth
      const next = w < 640 ? 'hidden' : w < 860 ? 'rail' : 'full'
      sidebar.set(next)
    }
    window.addEventListener('resize', onResize)
    onResize()
    return () => window.removeEventListener('resize', onResize)
  })

  const railNav = [
    { icon: 'ant-design:message-outlined', title: 'sidebar.chat', v: 'chat' },
    { icon: 'ant-design:clock-circle-outlined', title: 'nav.tasks', v: 'tasks' },
    { icon: 'ant-design:thunderbolt-outlined', title: 'nav.skills', v: 'skills' },
    { icon: 'ant-design:api-outlined', title: 'nav.mcp', v: 'mcp' },
    { icon: 'ant-design:mobile-outlined', title: 'nav.channels', v: 'channels' },
    { icon: 'ant-design:user-outlined', title: 'nav.memory', v: 'profile' },
    { icon: 'ant-design:folder-open-outlined', title: 'nav.file_recall', v: 'files' },
  ]

  function navActive(v: string) { return $view === v }

  function sessionIcon(s: any): string {
    if (s.source === 'cron') return 'ant-design:clock-circle-outlined'
    if (s.source === 'channel') return 'ant-design:send-outlined'
    if (s.status === 'working') return 'ant-design:code-outlined'
    return 'ant-design:message-outlined'
  }

  function toggleSel(id: string) {
    sel.update(s => { const n = { ...s }; n[id] ? delete n[id] : (n[id] = true); return n })
  }

  async function delSelected() {
    const ids = Object.keys($sel)
    try {
      await api.deleteSessions(ids)
      sessions.update(ss => ss.filter(s => !$sel[s.id]))
      if (ids.includes($activeSessionId ?? '')) {
        activeSessionId.set(null)
        view.set('chat')
      }
    } catch (e: any) { showToast(e.message, 'error') }
    sel.set({}); selMode.set(false)
  }

  async function delSession(id: string) {
    try {
      await api.deleteSession(id)
      sessions.update(ss => ss.filter(s => s.id !== id))
      if ($activeSessionId === id) {
        activeSessionId.set(null)
        // Clearing the active session while still on the chat view would leave
        // ChatView rendering a phantom "bound to another entry" banner. Fall
        // back to the session list landing so the deleted session is gone and
        // the user can pick or create a new one.
        view.set('chat')
      }
    } catch (e: any) { showToast(e.message, 'error') }
    menuFor.set(null)
  }

  async function commitRename() {
    if (!$editId) return
    const draft = $editDraft.trim()
    if (draft) {
      try {
        await api.updateSession($editId, { name: draft })
        sessions.update(ss => ss.map(s => s.id === $editId ? { ...s, name: draft, title: draft } : s))
      } catch (e: any) { showToast(e.message, 'error') }
    }
    editId.set(null)
  }

  async function newSession() {
    try {
      const sess = await api.createSession({ source: 'manual' }) as any
      sessions.update(ss => [sess.session ?? sess, ...ss])
      activeSessionId.set((sess.session ?? sess).id)
      activeSession.set((sess.session ?? sess).id)
      view.set('chat')
    } catch (e: any) { showToast(e.message, 'error') }
  }

  const selCount = $derived(Object.keys($sel).length)
</script>

<aside style="width:{$sidebar === 'full' ? '256px' : $sidebar === 'rail' ? '64px' : '0px'};flex:0 0 {$sidebar === 'full' ? '256px' : $sidebar === 'rail' ? '64px' : '0px'};background:var(--bg-sidebar);border-right:1px solid var(--border-secondary);overflow:hidden;transition:width 0.32s cubic-bezier(0.2,0,0,1),flex-basis 0.32s cubic-bezier(0.2,0,0,1);">

  {#if $sidebar === 'full'}
  <div class="full">
    <div class="new-btn-wrap">
      <button class="new-btn" onclick={newSession}>
        <iconify-icon icon="ant-design:plus-outlined" width="14"></iconify-icon>
        <span>{$t('nav.new_session')}</span>
      </button>
    </div>

    <div class="scroll">
      <!-- Sessions -->
      <div class="nav-group">
        <div class="group-header">
          <span class="group-label">{$t('nav.sessions')}</span>
          <span class="sel-toggle" onclick={() => { selMode.update(v => !v); sel.set({}); menuFor.set(null); editId.set(null) }}>
            {$selMode ? $t('sidebar.done') : $t('sidebar.select')}
          </span>
        </div>

        {#if $selMode && Object.keys($sel).length > 0}
        <div class="batch-bar">
          <span class="batch-count">{Object.keys($sel).length} selected</span>
          <button class="batch-del" onclick={delSelected}>
            <iconify-icon icon="ant-design:delete-outlined" width="12"></iconify-icon>
            {$t('common.delete')}
          </button>
        </div>
        {/if}

        {#each $sessions as s}
        {@const active = s.id === $activeSession && $view === 'chat'}
        {@const selected = !!$sel[s.id]}
        {@const editing = $editId === s.id}
        {@const menuOpen = $menuFor === s.id && !$selMode}
        {@const solid = active && !$selMode}
        {@const icon = sessionIcon(s)}
        <div
          class="nav-row"
          class:solid={solid}
          class:selected={selected && !solid}
          onclick={() => { if ($selMode) toggleSel(s.id); else { view.set('chat'); activeSession.set(s.id); activeSessionId.set(s.id); menuFor.set(null) } }}
        >
          {#if $selMode}
          <span
            class="checkbox"
            style="border-color:{selected ? 'var(--blue-6)' : 'var(--border)'};background:{selected ? 'var(--blue-6)' : '#fff'}"
            onclick={(e) => { e.stopPropagation(); toggleSel(s.id) }}
          >
            {#if selected}<iconify-icon icon="ant-design:check-outlined" width="11" style="color:#fff"></iconify-icon>{/if}
          </span>
          {/if}

          {#if (s as any).status === 'working'}
            <iconify-icon icon="ant-design:loading-outlined" width="14" style="color:{solid ? '#fff' : 'var(--blue-6)'};flex:0 0 auto;animation:octo-spin 0.8s linear infinite"></iconify-icon>
          {:else}
            <iconify-icon icon={icon} width="14" style="color:{solid ? '#fff' : 'var(--text-tertiary)'};flex:0 0 auto"></iconify-icon>
          {/if}

          {#if editing}
          <input
            class="rename-input"
            value={$editDraft}
            oninput={(e) => editDraft.set((e.target as HTMLInputElement).value)}
            onclick={(e) => e.stopPropagation()}
          />
          <span class="row-action" onclick={(e) => { e.stopPropagation(); commitRename() }} style="color:var(--success)">
            <iconify-icon icon="ant-design:check-outlined" width="13"></iconify-icon>
          </span>
          <span class="row-action" onclick={(e) => { e.stopPropagation(); editId.set(null) }} style="color:var(--text-tertiary)">
            <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
          </span>
          {:else}
          <span class="session-title" style="color:{solid ? '#fff' : 'var(--text)'};">{(s as any).name || (s as any).title || s.id}</span>
          {#if !menuOpen}
            <span class="session-time" style="color:{solid ? 'rgba(255,255,255,0.75)' : 'var(--text-quaternary)'};">
              {(s as any).source === 'cron' ? $t('sidebar.cron') : ''}
            </span>
          {/if}
          {#if !menuOpen && !$selMode}
            <span class="row-action kebab" onclick={(e) => { e.stopPropagation(); menuFor.update(m => m === s.id ? null : s.id) }} style="color:{solid ? '#fff' : 'var(--text-tertiary)'}">
              <iconify-icon icon="ant-design:more-outlined" width="14"></iconify-icon>
            </span>
          {/if}
          {#if menuOpen}
            <span class="row-action" onclick={(e) => { e.stopPropagation(); editId.set(s.id); editDraft.set((s as any).name || (s as any).title || s.id); menuFor.set(null) }} title={$t('sidebar.rename')}>
              <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
            </span>
            <span class="row-action del" onclick={(e) => { e.stopPropagation(); delSession(s.id) }} title={$t('common.delete')}>
              <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
            </span>
          {/if}
          {/if}
        </div>
        {/each}
      </div>

      <!-- Config -->
      <div class="nav-group">
        <div class="group-header"><span class="group-label">{$t('nav.config')}</span></div>
        {#each [
          { icon: 'ant-design:clock-circle-outlined', label: 'nav.tasks', v: 'tasks' },
          { icon: 'ant-design:thunderbolt-outlined', label: 'nav.skills', v: 'skills' },
          { icon: 'ant-design:api-outlined', label: 'nav.mcp', v: 'mcp', meta: mcpCount || '' },
          { icon: 'ant-design:mobile-outlined', label: 'nav.channels', v: 'channels' },
        ] as item}
        <div class="nav-row" class:solid={navActive(item.v)} onclick={() => view.set(item.v as any)}>
          <iconify-icon icon={item.icon} width="14" style="color:{navActive(item.v) ? '#fff' : 'var(--text-tertiary)'}"></iconify-icon>
          <span style="font-size:13px;color:{navActive(item.v) ? '#fff' : 'var(--text-secondary)'};">{$t(item.label)}</span>
          {#if item.meta}<span style="margin-left:auto;font-size:11px;color:{navActive(item.v) ? 'rgba(255,255,255,0.75)' : 'var(--text-tertiary)'};">{item.meta}</span>{/if}
        </div>
        {/each}
      </div>

      <!-- My Data -->
      <div class="nav-group">
        <div class="group-header"><span class="group-label">{$t('nav.my_data')}</span></div>
        {#each [
          { icon: 'ant-design:user-outlined', label: 'nav.memory', v: 'profile' },
          { icon: 'ant-design:folder-open-outlined', label: 'nav.file_recall', v: 'files' },
        ] as item}
        <div class="nav-row" class:solid={navActive(item.v)} onclick={() => view.set(item.v as any)}>
          <iconify-icon icon={item.icon} width="14" style="color:{navActive(item.v) ? '#fff' : 'var(--text-tertiary)'}"></iconify-icon>
          <span style="font-size:13px;color:{navActive(item.v) ? '#fff' : 'var(--text-secondary)'};">{$t(item.label)}</span>
        </div>
        {/each}
      </div>
    </div>

    <div class="footer">
      <div class="footer-settings" style="color:{navActive('settings') ? 'var(--blue-6)' : 'var(--text-secondary)'}" onclick={() => view.set('settings')}>
        <iconify-icon icon="ant-design:setting-outlined" width="14"></iconify-icon>
        <span>{$t('nav.settings')}</span>
      </div>
      <VersionBadge />
    </div>
  </div>
  {/if}

  {#if $sidebar === 'rail'}
  <div class="rail">
    <div style="padding:16px 0 8px 0;">
      <button class="rail-btn primary" title={$t('nav.new_session')} onclick={() => view.set('chat')}>
        <iconify-icon icon="ant-design:plus-outlined" width="16"></iconify-icon>
      </button>
    </div>
    <div class="rail-scroll">
      {#each railNav as item}
      <button
        class="rail-btn"
        class:active={navActive(item.v)}
        title={$t(item.title)}
        onclick={() => view.set(item.v as any)}
      >
        <iconify-icon icon={item.icon} width="16"></iconify-icon>
      </button>
      {/each}
    </div>
    <div class="rail-footer">
      <button class="rail-btn" class:active={navActive('settings')} title={$t('nav.settings')} onclick={() => view.set('settings')}>
        <iconify-icon icon="ant-design:setting-outlined" width="16"></iconify-icon>
      </button>
    </div>
  </div>
  {/if}
</aside>

<style>
.full { width: 256px; height: 100%; display: flex; flex-direction: column; min-height: 0; }
.new-btn-wrap { padding: 12px 12px 8px; }
.new-btn {
  width: 100%; height: 32px; border: none; border-radius: 6px;
  background: var(--blue-6); color: #fff; font-size: 14px;
  display: flex; align-items: center; justify-content: center; gap: 8px;
  cursor: pointer; font-family: inherit;
}
.new-btn:hover { background: var(--blue-5); }
.scroll { flex: 1; overflow-y: auto; padding: 8px 12px; display: flex; flex-direction: column; gap: 20px; }
.nav-group { display: flex; flex-direction: column; gap: 2px; }
.group-header { display: flex; align-items: center; justify-content: space-between; padding: 0 8px 6px; }
.group-label { font-size: 11px; font-weight: 600; letter-spacing: 0.5px; color: var(--text-quaternary); }
.sel-toggle { font-size: 11px; font-weight: 600; color: var(--blue-6); cursor: pointer; }
.batch-bar {
  display: flex; align-items: center; gap: 8px;
  margin: 0 4px 6px; padding: 6px 8px 6px 12px;
  background: var(--error-bg); border: 1px solid var(--error-border); border-radius: 8px;
}
.batch-count { font-size: 12px; color: var(--error-dark); flex: 1; }
.batch-del {
  height: 26px; padding: 0 10px; border: none; background: var(--error);
  border-radius: 6px; display: flex; align-items: center; gap: 6px;
  font-size: 12px; color: #fff; cursor: pointer; font-family: inherit;
}
.batch-del:hover { background: #FF7875; }
.nav-row {
  display: flex; align-items: center; gap: 10px;
  min-height: 36px; padding: 0 6px 0 10px;
  border-radius: 9999px; cursor: pointer;
}
.nav-row.solid { background: var(--blue-6); }
.nav-row.selected { background: var(--active-blue-bg); }
/* Hover never overrides the active (solid) row — that washed the blue pill out
   to grey with near-invisible white text. */
.nav-row:hover:not(.solid) { background: var(--hover-neutral); }
.checkbox {
  width: 16px; height: 16px; flex: 0 0 16px;
  border-radius: 4px; border: 1.5px solid;
  display: flex; align-items: center; justify-content: center;
}
.rename-input {
  flex: 1; min-width: 0; font-size: 13px; font-family: inherit;
  border: 1px solid var(--blue-6); border-radius: 4px;
  padding: 2px 6px; outline: none; color: var(--text);
}
.session-title {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis;
  white-space: nowrap; font-size: 13px;
}
.session-time { font-size: 11px; flex: 0 0 auto; padding-right: 4px; }
.row-action {
  width: 22px; height: 22px; flex: 0 0 22px; border-radius: 5px;
  display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
  opacity: 0; transition: opacity 0.12s;
}
.nav-row:hover .row-action { opacity: 1; }
.kebab { opacity: 0; }
.nav-row:hover .kebab { opacity: 1; }
.row-action:hover { background: var(--hover-neutral); }
.del:hover { color: var(--error) !important; }
.footer {
  flex: 0 0 auto; border-top: 1px solid var(--border-secondary);
  padding: 10px 12px; display: flex; align-items: center; justify-content: space-between;
}
.footer-settings {
  display: flex; align-items: center; gap: 8px;
  cursor: pointer; padding: 4px 8px; border-radius: 9999px;
}
.footer-settings:hover { background: var(--hover-neutral); }
.footer-settings span { font-size: 13px; }
/* Rail */
.rail {
  width: 64px; height: 100%; display: flex; flex-direction: column;
  align-items: center; min-height: 0;
}
.rail-scroll { flex: 1; overflow-y: auto; padding: 4px 0; display: flex; flex-direction: column; gap: 4px; align-items: center; }
.rail-footer { flex: 0 0 auto; border-top: 1px solid var(--border-secondary); padding: 8px 0; width: 100%; display: flex; justify-content: center; }
.rail-btn {
  width: 40px; height: 40px; border: none; border-radius: 9999px;
  background: transparent; color: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center; cursor: pointer;
}
.rail-btn:hover { background: var(--hover-neutral); }
.rail-btn.active { background: var(--blue-6); color: #fff; }
.rail-btn.primary { background: var(--blue-6); color: #fff; }
.rail-btn.primary:hover { background: var(--blue-5); }
</style>

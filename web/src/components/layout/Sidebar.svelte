<script lang="ts">
  import { onMount } from 'svelte'
  import { view, sidebar, sessions, sessionGroups, pinnedSessions, groupMenuFor, editGroupId, editGroupDraft, activeSession, activeSessionId, selMode, sel, menuFor, editId, editDraft, showToast, mcpServers, createNewSession } from '../../lib/stores'
  import * as api from '../../lib/api'
  import { t, tr } from '../../lib/i18n'
  import { confirmDialog } from '../../lib/confirm'
  import VersionBadge from './VersionBadge.svelte'

  // Seed the shared MCP-server store before the user ever opens the MCP panel;
  // McpView keeps it in sync afterward. Also seed the sidebar session groups so
  // the list can cluster on first paint.
  onMount(async () => {
    try {
      const d = await api.listMcpServers()
      mcpServers.set(d.servers as any)
    } catch { /* ignore — McpView will refetch */ }
    try {
      const org = await api.listSessionGroups()
      sessionGroups.set(org.groups)
      pinnedSessions.set(org.pinned)
    } catch { /* ignore — sessions just render flat under Ungrouped */ }
  })

  // The session list split into its groups (registry order) plus the leftover
  // "ungrouped" ones. Membership lives in the group registry; member IDs that
  // no longer resolve to a live session are dropped here, so a deleted session
  // leaves no ghost row.
  const groupedView = $derived.by(() => {
    const byId = new Map($sessions.map(s => [s.id, s] as const))
    const claimed = new Set<string>()
    // Pinned sessions float to a dedicated top section (registry order) and are
    // claimed first, so they don't also appear under their group or Ungrouped.
    const pinned = $pinnedSessions.map(id => byId.get(id)).filter(Boolean) as typeof $sessions
    pinned.forEach(s => claimed.add(s.id))
    const groups = $sessionGroups.map(g => {
      const items = g.session_ids.map(id => byId.get(id)).filter(Boolean).filter(s => !claimed.has((s as any).id)) as typeof $sessions
      items.forEach(s => claimed.add(s.id))
      return { group: g, items }
    })
    const ungrouped = $sessions.filter(s => !claimed.has(s.id))
    return { pinned, groups, ungrouped }
  })

  function groupIdOf(sessionId: string): string {
    return $sessionGroups.find(g => g.session_ids.includes(sessionId))?.id ?? ''
  }

  const isPinned = (sessionId: string): boolean => $pinnedSessions.includes(sessionId)

  // Pin/unpin a session. Optimistic: the row jumps into (or out of) the Pinned
  // section immediately, then the registry write follows; on failure, revert.
  // Pinning appends to the end (most-recently pinned last).
  async function togglePin(sessionId: string, pin: boolean) {
    menuFor.set(null)
    const before = $pinnedSessions
    pinnedSessions.set(pin
      ? [...before.filter(id => id !== sessionId), sessionId]
      : before.filter(id => id !== sessionId))
    try {
      await api.setSessionPinned(sessionId, pin)
    } catch {
      pinnedSessions.set(before)
      showToast(tr('sidebar.pin_failed'))
    }
  }

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

  // Dismiss any open floating UI (kebab menu, move-to-group popover, inline
  // rename) when the user clicks anywhere outside of it. Only clicks that land
  // inside a popover or a rename input are ignored (those must swallow the
  // click). Controls that OPEN a floating UI stop propagation themselves, so
  // this listener never fires for them; every other control (a group's
  // move-up/down, another row) correctly dismisses whatever was open. Inline
  // renames commit (rather than discard) on outside click — the commit helpers
  // no-op when no rename is active.
  $effect(() => {
    function onDocClick(e: MouseEvent) {
      const el = e.target as HTMLElement | null
      if (el?.closest('.grp-popover, .rename-input')) return
      menuFor.set(null)
      groupMenuFor.set(null)
      commitRename()
      commitGroupRename()
    }
    window.addEventListener('click', onDocClick)
    return () => window.removeEventListener('click', onDocClick)
  })

  const railNav = [
    { icon: 'ant-design:message-outlined', title: 'sidebar.chat', v: 'chat' },
    { icon: 'ant-design:clock-circle-outlined', title: 'nav.tasks', v: 'tasks' },
    { icon: 'ant-design:thunderbolt-outlined', title: 'nav.skills', v: 'skills' },
    { icon: 'ant-design:api-outlined', title: 'nav.mcp', v: 'mcp' },
    { icon: 'ant-design:partition-outlined', title: 'nav.workflows', v: 'workflows' },
    { icon: 'ant-design:global-outlined', title: 'nav.browser', v: 'browser' },
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
    if (!(await confirmDialog(tr('sidebar.confirm_delete_selected').replace('{n}', String(Object.keys($sel).length))))) return
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
    if (!(await confirmDialog(tr('sidebar.confirm_delete')))) return
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
      await createNewSession()
    } catch (e: any) { showToast(e.message, 'error') }
  }

  // A default name for a freshly created group: "Group N" / "分组N", where N is
  // the smallest positive integer whose name isn't already taken. Naming groups
  // distinctly (rather than every new group being "New group", which collides
  // with the "New group" create action in the popover) keeps the list readable.
  function nextDefaultGroupName(): string {
    const taken = new Set($sessionGroups.map(g => g.name))
    let n = 1
    let name = tr('sidebar.default_group_name').replace('{n}', String(n))
    while (taken.has(name)) {
      n++
      name = tr('sidebar.default_group_name').replace('{n}', String(n))
    }
    return name
  }

  // Create an empty group with a default name, then drop straight into inline
  // rename so the user names it without a separate prompt dialog (native
  // prompt() is unreliable in the desktop webview).
  async function newGroup() {
    try {
      const g = await api.createSessionGroup(nextDefaultGroupName())
      sessionGroups.update(gs => [...gs, g])
      editGroupId.set(g.id)
      editGroupDraft.set(g.name)
    } catch (e: any) { showToast(e.message, 'error') }
  }

  async function commitGroupRename() {
    const id = $editGroupId
    if (!id) return
    const draft = $editGroupDraft.trim()
    if (draft) {
      try {
        await api.updateSessionGroup(id, { name: draft })
        sessionGroups.update(gs => gs.map(g => g.id === id ? { ...g, name: draft } : g))
      } catch (e: any) { showToast(e.message, 'error') }
    }
    editGroupId.set(null)
  }

  async function deleteGroup(id: string, name: string) {
    if (!(await confirmDialog(tr('sidebar.confirm_delete_group').replace('{name}', name)))) return
    try {
      await api.deleteSessionGroup(id)
      sessionGroups.update(gs => gs.filter(g => g.id !== id))
    } catch (e: any) { showToast(e.message, 'error') }
  }

  // Move a group one slot up or down. Optimistic: swap locally, then persist
  // the full new order. On failure, revert.
  async function moveGroup(id: string, dir: -1 | 1) {
    const before = $sessionGroups
    const i = before.findIndex(g => g.id === id)
    const j = i + dir
    if (i < 0 || j < 0 || j >= before.length) return
    const next = [...before]
    ;[next[i], next[j]] = [next[j], next[i]]
    sessionGroups.set(next)
    try {
      await api.reorderSessionGroups(next.map(g => g.id))
    } catch (e: any) {
      sessionGroups.set(before)
      showToast(e.message, 'error')
    }
  }

  async function toggleCollapse(id: string, collapsed: boolean) {
    // Optimistic: flip locally, then persist. On failure, revert.
    sessionGroups.update(gs => gs.map(g => g.id === id ? { ...g, collapsed } : g))
    try {
      await api.updateSessionGroup(id, { collapsed })
    } catch (e: any) {
      sessionGroups.update(gs => gs.map(g => g.id === id ? { ...g, collapsed: !collapsed } : g))
      showToast(e.message, 'error')
    }
  }

  // From a session's "move to group" popover: create a fresh group, drop the
  // session into it, and open inline rename on the new group.
  async function newGroupForSession(sessionId: string) {
    groupMenuFor.set(null)
    try {
      const g = await api.createSessionGroup(nextDefaultGroupName())
      await api.setSessionGroup(sessionId, g.id)
      sessionGroups.update(gs => [
        ...gs.map(x => ({ ...x, session_ids: x.session_ids.filter(id => id !== sessionId) })),
        { ...g, session_ids: [sessionId] },
      ])
      editGroupId.set(g.id)
      editGroupDraft.set(g.name)
    } catch (e: any) { showToast(e.message, 'error') }
  }

  async function moveToGroup(sessionId: string, groupId: string) {
    groupMenuFor.set(null)
    try {
      await api.setSessionGroup(sessionId, groupId)
      // Update membership locally: drop from every group, then add to target.
      sessionGroups.update(gs => gs.map(g => ({
        ...g,
        session_ids: g.id === groupId
          ? [...g.session_ids.filter(id => id !== sessionId), sessionId]
          : g.session_ids.filter(id => id !== sessionId),
      })))
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
          <span class="header-actions">
            {#if !$selMode}
            <span class="header-btn" title={$t('sidebar.new_group')} onclick={newGroup}>
              <iconify-icon icon="ant-design:folder-add-outlined" width="14"></iconify-icon>
            </span>
            {/if}
            <span class="sel-toggle" onclick={() => { selMode.update(v => !v); sel.set({}); menuFor.set(null); editId.set(null); groupMenuFor.set(null) }}>
              {$selMode ? $t('sidebar.done') : $t('sidebar.select')}
            </span>
          </span>
        </div>

        {#if $selMode && Object.keys($sel).length > 0}
        <div class="batch-bar">
          <span class="batch-count">{$t('sidebar.n_selected').replace('{n}', String(Object.keys($sel).length))}</span>
          <button class="batch-del" onclick={delSelected}>
            <iconify-icon icon="ant-design:delete-outlined" width="12"></iconify-icon>
            {$t('common.delete')}
          </button>
        </div>
        {/if}

        <!-- Pinned: a dedicated top section, above all groups -->
        {#if groupedView.pinned.length > 0}
        <div class="grp-header">
          <iconify-icon icon="ant-design:pushpin-filled" width="11" style="color:var(--text-quaternary)"></iconify-icon>
          <span class="grp-name muted">{$t('sidebar.pinned')}</span>
          <span class="grp-count">{groupedView.pinned.length}</span>
        </div>
        {#each groupedView.pinned as s (s.id)}
          {@render sessionRow(s)}
        {/each}
        {/if}

        <!-- Groups (registry order), each collapsible -->
        {#each groupedView.groups as gv, gi (gv.group.id)}
        {@const g = gv.group}
        {@const editingG = $editGroupId === g.id}
        <div class="grp-header">
          <span class="grp-caret" onclick={() => toggleCollapse(g.id, !g.collapsed)}>
            <iconify-icon icon={g.collapsed ? 'ant-design:right-outlined' : 'ant-design:down-outlined'} width="10"></iconify-icon>
          </span>
          {#if editingG}
          <input
            class="rename-input"
            value={$editGroupDraft}
            oninput={(e) => editGroupDraft.set((e.target as HTMLInputElement).value)}
            onkeydown={(e) => { if (e.key === 'Enter') commitGroupRename(); if (e.key === 'Escape') editGroupId.set(null) }}
          />
          <span class="row-action" onclick={commitGroupRename} style="color:var(--success)">
            <iconify-icon icon="ant-design:check-outlined" width="13"></iconify-icon>
          </span>
          <span class="row-action" onclick={() => editGroupId.set(null)} style="color:var(--text-tertiary)">
            <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
          </span>
          {:else}
          <span class="grp-name" onclick={() => toggleCollapse(g.id, !g.collapsed)}>{g.name}</span>
          <span class="grp-count">{gv.items.length}</span>
          {#if !$selMode}
          {#if gi > 0}
          <span class="row-action" title={$t('sidebar.move_group_up')} onclick={() => moveGroup(g.id, -1)}>
            <iconify-icon icon="ant-design:arrow-up-outlined" width="13"></iconify-icon>
          </span>
          {/if}
          {#if gi < groupedView.groups.length - 1}
          <span class="row-action" title={$t('sidebar.move_group_down')} onclick={() => moveGroup(g.id, 1)}>
            <iconify-icon icon="ant-design:arrow-down-outlined" width="13"></iconify-icon>
          </span>
          {/if}
          <span class="row-action" title={$t('sidebar.rename_group')} onclick={(e) => { e.stopPropagation(); editGroupId.set(g.id); editGroupDraft.set(g.name) }}>
            <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
          </span>
          <span class="row-action del" title={$t('sidebar.delete_group')} onclick={() => deleteGroup(g.id, g.name)}>
            <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
          </span>
          {/if}
          {/if}
        </div>
        {#if !g.collapsed}
          {#each gv.items as s (s.id)}
            {@render sessionRow(s)}
          {/each}
        {/if}
        {/each}

        <!-- Ungrouped: header only shown when at least one group exists -->
        {#if groupedView.ungrouped.length > 0}
          {#if groupedView.groups.length > 0}
          <div class="grp-header">
            <span class="grp-name muted">{$t('sidebar.ungrouped')}</span>
            <span class="grp-count">{groupedView.ungrouped.length}</span>
          </div>
          {/if}
          {#each groupedView.ungrouped as s (s.id)}
            {@render sessionRow(s)}
          {/each}
        {/if}
      </div>

      {#snippet sessionRow(s: any)}
        {@const active = s.id === $activeSession && $view === 'chat'}
        {@const selected = !!$sel[s.id]}
        {@const editing = $editId === s.id}
        {@const menuOpen = $menuFor === s.id && !$selMode}
        {@const groupOpen = $groupMenuFor === s.id && !$selMode}
        {@const solid = active && !$selMode}
        {@const icon = sessionIcon(s)}
        <div
          class="nav-row"
          class:solid={solid}
          class:selected={selected && !solid}
          onclick={() => { if ($selMode) toggleSel(s.id); else { view.set('chat'); activeSession.set(s.id); activeSessionId.set(s.id); menuFor.set(null); groupMenuFor.set(null) } }}
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
          {#if isPinned(s.id) && !menuOpen}
            <iconify-icon icon="ant-design:pushpin-filled" width="11" title={$t('sidebar.pinned')} style="color:{solid ? 'rgba(255,255,255,0.75)' : 'var(--text-quaternary)'};flex:0 0 auto"></iconify-icon>
          {/if}
          {#if (s as any).pending_question}
            <span class="pending-dot" title={$t('sidebar.pending_question')}></span>
          {/if}
          {#if !menuOpen}
            <span class="session-time" style="color:{solid ? 'rgba(255,255,255,0.75)' : 'var(--text-quaternary)'};">
              {(s as any).source === 'cron' ? $t('sidebar.cron') : ''}
            </span>
          {/if}
          {#if !menuOpen && !$selMode}
            <span class="row-action kebab" onclick={(e) => { e.stopPropagation(); menuFor.update(m => m === s.id ? null : s.id); groupMenuFor.set(null) }} style="color:{solid ? '#fff' : 'var(--text-tertiary)'}">
              <iconify-icon icon="ant-design:more-outlined" width="14"></iconify-icon>
            </span>
          {/if}
          {#if menuOpen}
            {@const pinned = isPinned(s.id)}
            <span class="row-action" onclick={(e) => { e.stopPropagation(); togglePin(s.id, !pinned) }} title={pinned ? $t('sidebar.unpin') : $t('sidebar.pin')}>
              <iconify-icon icon={pinned ? 'ant-design:pushpin-filled' : 'ant-design:pushpin-outlined'} width="13"></iconify-icon>
            </span>
            <span class="row-action" onclick={(e) => { e.stopPropagation(); groupMenuFor.set(s.id); menuFor.set(null) }} title={$t('sidebar.move_to_group')}>
              <iconify-icon icon="ant-design:folder-outlined" width="13"></iconify-icon>
            </span>
            <span class="row-action" onclick={(e) => { e.stopPropagation(); editId.set(s.id); editDraft.set((s as any).name || (s as any).title || s.id); menuFor.set(null) }} title={$t('sidebar.rename')}>
              <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
            </span>
            <span class="row-action del" onclick={(e) => { e.stopPropagation(); delSession(s.id) }} title={$t('common.delete')}>
              <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
            </span>
          {/if}
          {/if}

          {#if groupOpen}
          {@const curGid = groupIdOf(s.id)}
          <div class="grp-popover" onclick={(e) => e.stopPropagation()}>
            {#each $sessionGroups as pg (pg.id)}
            <div class="grp-opt" class:cur={curGid === pg.id} onclick={() => moveToGroup(s.id, pg.id)}>
              <iconify-icon icon="ant-design:check-outlined" width="12" style="opacity:{curGid === pg.id ? 1 : 0}"></iconify-icon>
              <span class="grp-opt-name">{pg.name}</span>
            </div>
            {/each}
            {#if curGid}
            <div class="grp-opt" onclick={() => moveToGroup(s.id, '')}>
              <iconify-icon icon="ant-design:close-outlined" width="12"></iconify-icon>
              <span class="grp-opt-name">{$t('sidebar.remove_from_group')}</span>
            </div>
            {/if}
            <div class="grp-sep"></div>
            <div class="grp-opt" onclick={() => newGroupForSession(s.id)}>
              <iconify-icon icon="ant-design:plus-outlined" width="12"></iconify-icon>
              <span class="grp-opt-name">{$t('sidebar.new_group')}</span>
            </div>
          </div>
          {/if}
        </div>
      {/snippet}

      <!-- Config -->
      <div class="nav-group">
        <div class="group-header"><span class="group-label">{$t('nav.config')}</span></div>
        {#each [
          { icon: 'ant-design:clock-circle-outlined', label: 'nav.tasks', v: 'tasks' },
          { icon: 'ant-design:thunderbolt-outlined', label: 'nav.skills', v: 'skills' },
          { icon: 'ant-design:api-outlined', label: 'nav.mcp', v: 'mcp' },
          { icon: 'ant-design:partition-outlined', label: 'nav.workflows', v: 'workflows' },
          { icon: 'ant-design:global-outlined', label: 'nav.browser', v: 'browser' },
          { icon: 'ant-design:mobile-outlined', label: 'nav.channels', v: 'channels' },
        ] as item}
        <div class="nav-row" class:solid={navActive(item.v)} onclick={() => view.set(item.v as any)}>
          <iconify-icon icon={item.icon} width="14" style="color:{navActive(item.v) ? '#fff' : 'var(--text-tertiary)'}"></iconify-icon>
          <span style="font-size:13px;color:{navActive(item.v) ? '#fff' : 'var(--text-secondary)'};">{$t(item.label)}</span>
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
      <button class="rail-btn primary" title={$t('nav.new_session')} onclick={newSession}>
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
.header-actions { display: flex; align-items: center; gap: 8px; }
.header-btn {
  display: flex; align-items: center; justify-content: center;
  color: var(--text-tertiary); cursor: pointer;
}
.header-btn:hover { color: var(--blue-6); }
.sel-toggle { font-size: 11px; font-weight: 600; color: var(--blue-6); cursor: pointer; }
/* Group section header (folder row) */
.grp-header {
  display: flex; align-items: center; gap: 6px;
  min-height: 28px; padding: 0 6px 0 6px; margin-top: 2px;
  border-radius: 6px;
}
.grp-header:hover { background: var(--hover-neutral); }
.grp-caret {
  width: 16px; flex: 0 0 16px; display: flex; align-items: center; justify-content: center;
  color: var(--text-tertiary); cursor: pointer;
}
.grp-name {
  flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-size: 12px; font-weight: 600; color: var(--text-secondary); cursor: pointer;
}
.grp-name.muted { font-weight: 600; color: var(--text-quaternary); cursor: default; }
.grp-count { font-size: 11px; color: var(--text-quaternary); flex: 0 0 auto; padding: 0 2px; }
.grp-header .row-action { opacity: 0; }
.grp-header:hover .row-action { opacity: 1; }
/* Move-to-group popover */
.grp-popover {
  position: absolute; top: 100%; right: 8px; z-index: 30;
  min-width: 160px; max-width: 220px; max-height: 260px; overflow-y: auto;
  margin-top: 2px; padding: 4px;
  background: var(--bg-elevated, #fff); border: 1px solid var(--border-secondary);
  border-radius: 8px; box-shadow: 0 6px 20px rgba(0,0,0,0.18);
}
.grp-opt {
  display: flex; align-items: center; gap: 6px;
  padding: 6px 8px; border-radius: 6px; cursor: pointer;
  font-size: 13px; color: var(--text);
}
.grp-opt:hover { background: var(--hover-neutral); }
.grp-opt.cur { color: var(--blue-6); }
.grp-opt-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.grp-sep { height: 1px; margin: 4px 6px; background: var(--border-secondary); }
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
  position: relative;
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
.pending-dot {
  width: 6px; height: 6px; flex: 0 0 auto; border-radius: 50%;
  background: var(--blue-6); margin-right: 4px;
}
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

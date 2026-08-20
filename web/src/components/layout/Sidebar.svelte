<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { get } from 'svelte/store'
  import { view, sidebar, sessions, sessionGroups, pinnedSessions, collapsedSessions, editGroupId, editGroupDraft, activeSessionId, selMode, sel, menuFor, editId, editDraft, showToast, mcpServers, createNewSession, createSessionInGroup, clearPendingSessionOpts, settingsModalOpen } from '../../lib/stores'
  import * as api from '../../lib/api'
  import { t, tr } from '../../lib/i18n'
  import { confirmDialog } from '../../lib/confirm'
  import { splitSections, swapWithinSection, parseSectionFold, type SectionFold } from '../../lib/sidebarSections'
  import { ago } from '../../lib/relTime'
  import { ws } from '../../lib/ws'
  import VersionBadge from './VersionBadge.svelte'

  // Project whose row menu is open, by id ('' = none). Local rather than a store:
  // nothing outside this sidebar opens or reads it.
  let projectMenuFor = $state('')

  // Agent list for the new-session picker dropdown.
  let agents: api.Agent[] = $state([])

  // "More" flyout — collapses the remaining agentic-config surfaces (agents/
  // skills/mcp/workflows/channels) behind one sidebar row instead of a row
  // each. Unlike a sub-page with its own rail, clicking an item here
  // navigates straight to that view; there's no second layer of navigation.
  // One boolean covers both sidebar modes since 'full' and 'rail' are never
  // both mounted at once.
  let morePopoverOpen = $state(false)
  let morePopoverEl = $state<HTMLElement>()
  // The flyout must escape the sidebar <aside>'s overflow:hidden (needed for
  // the width-collapse transition) and, in full mode, the additional
  // clipping/scrolling .scroll region — an absolutely-positioned descendant
  // gets clipped by whichever ancestor's box ends first, which for the
  // full-mode row sits right where the footer begins. So in both modes it's
  // portaled to <body> and positioned via the anchor's captured rect instead.
  let morePos = $state({ top: 0, left: 0, width: 0 })
  function portal(node: HTMLElement) {
    document.body.appendChild(node)
    return { destroy() { node.remove() } }
  }
  function toggleMorePopover(anchor: HTMLElement, mode: 'full' | 'rail') {
    if (morePopoverOpen) { morePopoverOpen = false; return }
    const r = anchor.getBoundingClientRect()
    morePos = mode === 'rail'
      ? { top: r.top, left: r.right + 6, width: 168 }
      : { top: r.bottom + 2, left: r.left, width: r.width + 8 }
    morePopoverOpen = true
  }
  // Everything reachable but not reached often. The four destinations that ARE
  // reached often sit in the nav group instead; what is left is configuration you
  // visit to change something and then leave.
  const moreCategories = [
    { icon: 'ant-design:robot-outlined', label: 'nav.agents', v: 'agents' },
    { icon: 'ant-design:thunderbolt-outlined', label: 'nav.skills', v: 'skills' },
    { icon: 'ant-design:api-outlined', label: 'nav.mcp', v: 'mcp' },
    { icon: 'ant-design:partition-outlined', label: 'nav.workflows', v: 'workflows' },
    { icon: 'ant-design:global-outlined', label: 'nav.browser', v: 'browser' },
    { icon: 'ant-design:mobile-outlined', label: 'nav.channels', v: 'channels' },
  ]

  // The nav group, in both widths: the destinations worth a row of their own.
  const topNav = [
    { icon: 'ant-design:clock-circle-outlined', label: 'nav.tasks', v: 'tasks' },
    { icon: 'ant-design:appstore-outlined', label: 'nav.light_apps', v: 'lightapps' },
  ]
  function goToMore(v: string) {
    view.set(v as any)
    morePopoverOpen = false
  }

  function dismissPicker(e: MouseEvent) {
    // The popover itself is portaled to <body> in both modes, so it's no
    // longer a DOM descendant of morePopoverEl (the anchor's wrapper) —
    // check for a click inside it by class instead.
    if (morePopoverOpen && morePopoverEl && !morePopoverEl.contains(e.target as Node) && !(e.target as HTMLElement).closest?.('.more-popover')) {
      morePopoverOpen = false
    }
  }

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
      collapsedSessions.set(org.collapsed)
    } catch { /* ignore — sessions just render flat under Tasks */ }
    try {
      agents = await api.listAgents()
    } catch { /* agents list is optional */ }
  })

  // Reload agent list when an agent is created/updated/deleted via the API
  // (e.g. through expert-agent-manager skill in conversation).
  const unsubAgents = ws.on('agents_changed', async () => {
    try {
      agents = await api.listAgents()
    } catch { /* ignore */ }
  })

  onDestroy(() => { unsubAgents() })

  // The session list split into the sections this sidebar renders. The logic
  // lives in lib/sidebarSections so it can be tested: it decides whether a row
  // renders at all, and a group that never renders is unreachable rather than
  // merely misplaced.
  const groupedView = $derived(
    splitSections($sessions, $sessionGroups, $pinnedSessions, $collapsedSessions),
  )

  // Which sections are expanded. This is a per-browser view preference about
  // screen space, not a fact about the sessions, so it stays in localStorage
  // rather than in the server-side group registry (unlike a group's own
  // collapsed flag, which is shared so every surface folds it the same way).
  const SECTIONS_KEY = 'octo.sidebar.sections'
  function loadSections(): SectionFold {
    try {
      return parseSectionFold(localStorage.getItem(SECTIONS_KEY))
    } catch {
      // Storage access itself can throw (privacy mode): both sections open.
      return { tasks: true, projects: true }
    }
  }
  let sections = $state(loadSections())
  // Ids of the reorderable section, so a project's arrows know their own
  // neighbours (and whether they are at either end). Only Projects reorders:
  // Tasks is a flat recency list, and the Scheduled section is the scheduler's.
  const projectIds = $derived(groupedView.projects.map(gv => gv.group.id))
  function persistSections() {
    try { localStorage.setItem(SECTIONS_KEY, JSON.stringify(sections)) } catch { /* ignore */ }
  }
  type SectionKey = 'tasks' | 'projects'
  function toggleSection(k: SectionKey) {
    sections = { ...sections, [k]: !sections[k] }
    persistSections()
  }
  // Creating a project opens an inline rename box on the new row, so a folded
  // section would swallow the whole action: the project exists, named after its
  // directory, with no visible row to rename or configure. Anything that creates
  // or retargets a project reveals the section it lands in.
  function revealSection(k: SectionKey) {
    if (sections[k]) return
    sections = { ...sections, [k]: true }
    persistSections()
  }

  // On the new-session landing page: chat view with nothing selected. This is
  // the active state of the New session row.
  const onLanding = $derived($view === 'chat' && !$activeSessionId)


  const isPinned = (sessionId: string): boolean => $pinnedSessions.includes(sessionId)
  const isCollapsed = (sessionId: string): boolean => $collapsedSessions.includes(sessionId)

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

  // Whether the folded panel's list is expanded. Deliberately ephemeral (not
  // persisted like a group's collapsed flag): the panel exists to keep the
  // list short, so every fresh mount starts folded shut.

  // Collapse a session into the folded panel, or restore it. Optimistic like
  // togglePin. The collapse action is only offered on unpinned, ungrouped
  // sessions (the server rejects the rest), so no local guard is needed.
  // Un-archiving lives in Settings' 数据管理 now (SettingsModal.svelte), the
  // only place an archived session is still visible — hence one direction only.
  async function archiveSession(sessionId: string) {
    menuFor.set(null)
    const before = $collapsedSessions
    collapsedSessions.set([...before.filter(id => id !== sessionId), sessionId])
    try {
      await api.setSessionCollapsed(sessionId, true)
    } catch {
      collapsedSessions.set(before)
      showToast(tr('sidebar.collapse_failed'))
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
      if (el?.closest('.rename-input')) return
      menuFor.set(null)
      projectMenuFor = ''
      commitRename()
      commitGroupRename()
    }
    window.addEventListener('click', onDocClick)
    return () => window.removeEventListener('click', onDocClick)
  })

  // Same destinations as topNav, plus chat — the rail is the same sidebar with
  // the labels taken away, so the two must not drift apart.
  const railNav = [
    { icon: 'ant-design:message-outlined', title: 'sidebar.chat', v: 'chat' },
    ...topNav.map(item => ({ icon: item.icon, title: item.label, v: item.v })),
  ]

  function navActive(v: string) { return $view === v }
  function moreActive() { return moreCategories.some(c => c.v === $view) }

  function toggleSel(id: string) {
    sel.update(s => { const n = { ...s }; n[id] ? delete n[id] : (n[id] = true); return n })
  }

  // Batch mode is entered from one session's menu, and that session starts
  // selected: the row you opened the menu on is the one you meant, so the first
  // action is one click away rather than two.
  function enterBatchMode(seedId: string) {
    menuFor.set(null)
    editId.set(null)
    sel.set({ [seedId]: true })
    selMode.set(true)
  }

  function exitBatchMode() {
    selMode.set(false)
    sel.set({})
  }

  // Every session the list can reach, in render order — the pool "select all"
  // acts on, and what a section's own checkbox is a slice of. Pinned sessions
  // are left out: they have no checkbox of their own (see sessionRow), so a
  // pool that included them would select something the UI never showed as
  // selected.
  const allListedIds = $derived([
    ...groupedView.ungrouped.map(s => s.id),
    ...groupedView.projects.flatMap(gv => gv.items.map(s => s.id)),
  ])

  type TriState = 'none' | 'some' | 'all'

  // A group's checkbox reflects its members rather than holding state of its
  // own: partial selection has to be visible, or clicking a half-selected
  // section would look like it did nothing.
  function triStateOf(ids: string[]): TriState {
    if (ids.length === 0) return 'none'
    const n = ids.reduce((acc, id) => acc + ($sel[id] ? 1 : 0), 0)
    return n === 0 ? 'none' : n === ids.length ? 'all' : 'some'
  }

  // Clicking a partially selected group selects the rest of it, matching what
  // the box shows: anything short of "all" fills up.
  function toggleMany(ids: string[]) {
    const on = triStateOf(ids) !== 'all'
    sel.update(s => {
      const n = { ...s }
      for (const id of ids) on ? (n[id] = true) : delete n[id]
      return n
    })
  }

  async function delSelected() {
    const ids = Object.keys($sel)
    if (ids.length === 0) return
    if (!(await confirmDialog(tr('sidebar.confirm_delete_selected').replace('{n}', String(ids.length))))) return
    try {
      await api.deleteSessions(ids)
      sessions.update(ss => ss.filter(s => !$sel[s.id]))
      if (ids.includes($activeSessionId ?? '')) {
        activeSessionId.set(null)
        clearPendingSessionOpts()
        view.set('chat')
      }
    } catch (e: any) { showToast(e.message, 'error') }
    exitBatchMode()
  }

  // Archiving is only illegal for a pinned session (the two states contradict —
  // pin means always at the top, archive means out of sight; the server refuses
  // it). A session in a project archives fine and keeps its membership, so
  // restoring it later puts it back exactly where it was. A selection spanning
  // pinned and unpinned sessions archives what it can and says how many it left
  // alone — refusing the whole batch over one pinned member would be the more
  // annoying answer.
  async function archiveSelected() {
    const ids = Object.keys($sel)
    if (ids.length === 0) return
    const eligible = ids.filter(id => !isPinned(id) && !isCollapsed(id))
    const skipped = ids.length - eligible.length
    if (eligible.length === 0) {
      showToast(tr('sidebar.archive_none_eligible'), 'error')
      return
    }
    const before = $collapsedSessions
    collapsedSessions.set([...before.filter(id => !eligible.includes(id)), ...eligible])
    const failed: string[] = []
    for (const id of eligible) {
      try {
        await api.setSessionCollapsed(id, true)
      } catch {
        failed.push(id)
      }
    }
    if (failed.length) {
      collapsedSessions.set(before)
      showToast(tr('sidebar.collapse_failed'), 'error')
    } else if (skipped > 0) {
      showToast(tr('sidebar.archived_some').replace('{n}', String(eligible.length)).replace('{k}', String(skipped)))
    }
    exitBatchMode()
  }

  async function delSession(id: string) {
    if (!(await confirmDialog(tr('sidebar.confirm_delete')))) return
    try {
      await api.deleteSession(id)
      sessions.update(ss => ss.filter(s => s.id !== id))
      if ($activeSessionId === id) {
        activeSessionId.set(null)
        clearPendingSessionOpts()
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

  // Deleting a project takes its sessions with it — they were work on that
  // directory, and leaving them behind as unattached tasks is a mess nobody asked
  // for. The confirmation therefore says how many transcripts are about to go,
  // and the deletion is one request so it cannot half-happen.
  async function deleteGroup(id: string, name: string, count: number) {
    projectMenuFor = ''
    const msg = count > 0
      ? tr('sidebar.confirm_delete_project').replace('{name}', name).replace('{n}', String(count))
      : tr('sidebar.confirm_delete_empty_project').replace('{name}', name)
    const ok = await confirmDialog(msg, {
      title: tr('sidebar.confirm_delete_project_title'),
      danger: count > 0,
      confirmLabel: tr('sidebar.confirm_delete_project_ok'),
    })
    if (!ok) return
    try {
      await api.deleteSessionGroup(id, count > 0)
      const gone = new Set(get(sessionGroups).find(g => g.id === id)?.session_ids ?? [])
      sessionGroups.update(gs => gs.filter(g => g.id !== id))
      if (gone.size) {
        sessions.update(ss => ss.filter(x => !gone.has(x.id)))
        if (gone.has($activeSessionId ?? '')) {
          activeSessionId.set(null)
          clearPendingSessionOpts()
          view.set('chat')
        }
      }
    } catch (e: any) { showToast(e.message, 'error') }
  }

  // Move a group one slot up or down WITHIN ITS OWN SECTION: `siblings` is the
  // id list of the section it renders in, so a project swaps with the previous
  // project and a task group with the previous task group. Swapping with
  // whichever group happens to be adjacent in the registry would make the row
  // vanish from under the cursor into the other section. The registry itself
  // stays one flat ordered list — only the two groups actually swap places in
  // it. Optimistic: swap locally, then persist the full new order; revert on
  // failure.
  async function moveGroup(id: string, dir: -1 | 1, siblings: string[]) {
    const before = $sessionGroups
    const next = swapWithinSection(before, id, dir, siblings)
    if (!next) return
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

  const selCount = $derived(Object.keys($sel).length)

  function agentNameOf(profileId: string): string {
    if (!profileId || profileId === 'default') return ''
    return agents.find(a => a.id === profileId)?.name ?? profileId
  }
</script>

<svelte:window onclick={dismissPicker} />

<!-- One checkbox shape for a session, a group, and the whole list. The
     group and list boxes are three-state because partial selection has
     to be visible: a half-filled box that clicked to "all" is honest,
     a full-looking one that clicked to "none" is not. -->
{#snippet triBox(state: TriState, onPick: () => void)}
  <span
    class="checkbox tri"
    class:on={state !== 'none'}
    onclick={(e) => { e.stopPropagation(); onPick() }}
  >
    {#if state === 'all'}
      <iconify-icon icon="ant-design:check-outlined" width="11" style="color:#fff"></iconify-icon>
    {:else if state === 'some'}
      <iconify-icon icon="ant-design:minus-outlined" width="11" style="color:#fff"></iconify-icon>
    {/if}
  </span>
{/snippet}

<aside style="width:{$sidebar === 'full' ? '256px' : $sidebar === 'rail' ? '64px' : '0px'};flex:0 0 {$sidebar === 'full' ? '256px' : $sidebar === 'rail' ? '64px' : '0px'};background:var(--sidebar-frost);backdrop-filter:blur(var(--frost-blur));-webkit-backdrop-filter:blur(var(--frost-blur));border-right:1px solid var(--border-secondary);overflow:hidden;transition:width 0.32s cubic-bezier(0.2,0,0,1),flex-basis 0.32s cubic-bezier(0.2,0,0,1);">

  {#if $sidebar === 'full'}
  <div class="full">
    <div class="scroll">
      <!-- Where you go, above what you have been doing. The session list is the
           long, growing part of this sidebar; putting it last means a nav row's
           position does not depend on how many sessions exist, and no amount of
           history can push a destination out of reach.
           New session is one of these rows rather than an accent button: it goes
           to the same landing page they go to, and its active state is being ON
           that landing page — chat view with no session picked. -->
      <div class="nav-group">
        <div class="nav-row" class:solid={onLanding} onclick={() => createNewSession()}>
          <iconify-icon icon="ant-design:plus-circle-outlined" width="14" style="color:{onLanding ? 'var(--blue-6)' : 'var(--text-tertiary)'}"></iconify-icon>
          <span style="font-size:13px;color:{onLanding ? 'var(--blue-6)' : 'var(--text-secondary)'};font-weight:{onLanding ? '600' : '400'};">{$t('nav.new_session')}</span>
        </div>
        {#each topNav as item (item.v)}
        <div class="nav-row" class:solid={navActive(item.v)} onclick={() => view.set(item.v as any)}>
          <iconify-icon icon={item.icon} width="14" style="color:{navActive(item.v) ? 'var(--blue-6)' : 'var(--text-tertiary)'}"></iconify-icon>
          <span style="font-size:13px;color:{navActive(item.v) ? 'var(--blue-6)' : 'var(--text-secondary)'};font-weight:{navActive(item.v) ? '600' : '400'};">{$t(item.label)}</span>
        </div>
        {/each}
        <div class="more-wrap" bind:this={morePopoverEl}>
          <div class="nav-row" class:solid={moreActive()} onclick={(e) => toggleMorePopover(e.currentTarget as HTMLElement, 'full')}>
            <iconify-icon icon="ant-design:menu-outlined" width="14" style="color:{moreActive() ? 'var(--blue-6)' : 'var(--text-tertiary)'}"></iconify-icon>
            <span style="font-size:13px;color:{moreActive() ? 'var(--blue-6)' : 'var(--text-secondary)'};font-weight:{moreActive() ? '600' : '400'};">{$t('nav.manage')}</span>
          </div>
          {#if morePopoverOpen}
          <div class="more-popover" use:portal style="top:{morePos.top}px; left:{morePos.left}px; width:{morePos.width}px;">
            {#each moreCategories as c (c.v)}
            <button class="ap-item" onclick={() => goToMore(c.v)}>
              <iconify-icon icon={c.icon} width="14" style="color:var(--text-tertiary)"></iconify-icon>
              <span>{$t(c.label)}</span>
            </button>
            {/each}
          </div>
          {/if}
        </div>
      </div>

      <!-- The session list, last and elastic: it sizes to its own content and is
           the one thing that gives (bounded by min-height, scrolling internally)
           once the stack would overflow .scroll. -->
      <div class="nav-group sessions-group">

        <!-- Pinned: a dedicated top section, above all groups -->
        {#if groupedView.pinned.length > 0}
        <div class="grp-header">
          <iconify-icon icon="ant-design:pushpin-filled" width="11" style="color:var(--text-quaternary)"></iconify-icon>
          <span class="grp-name muted">{$t('sidebar.pinned')}</span>
        </div>
        {#each groupedView.pinned as s (s.id)}
          {@render sessionRow(s)}
        {/each}
        {/if}

        <!-- Projects: a directory plus the sessions working in it. Counted by
             project, not by session — a project is the unit here, and its own
             header already carries how many sessions are in it. -->
        {#if groupedView.projects.length > 0}
        <div class="sec-header" onclick={() => toggleSection('projects')}>
          {#if $selMode}{@render triBox(triStateOf(groupedView.projects.flatMap(gv => gv.items.map(s => s.id))), () => toggleMany(groupedView.projects.flatMap(gv => gv.items.map(s => s.id))))}{/if}
          <span class="sec-name">{$t('sidebar.projects')}</span>
          <iconify-icon class="sec-caret" class:folded={!sections.projects} icon="ant-design:right-outlined" width="10"></iconify-icon>
        </div>
        {#if sections.projects}
          {#each groupedView.projects as gv, gi (gv.group.id)}
            {@render groupBlock(gv, gi, projectIds)}
          {/each}
        {/if}
        {/if}

        <!-- Tasks: every session that belongs to no project, flat. A task is
             one session — there is no naming or nesting layer inside this
             section, which is what the retired "plain group" used to add. -->
        {#if groupedView.ungrouped.length > 0}
        <div class="sec-header" onclick={() => toggleSection('tasks')}>
          {#if $selMode}{@render triBox(triStateOf(groupedView.ungrouped.map(s => s.id)), () => toggleMany(groupedView.ungrouped.map(s => s.id)))}{/if}
          <span class="sec-name">{$t('sidebar.tasks')}</span>
          <iconify-icon class="sec-caret" class:folded={!sections.tasks} icon="ant-design:right-outlined" width="10"></iconify-icon>
        </div>
        {#if sections.tasks}
          {#each groupedView.ungrouped as s (s.id)}
            {@render sessionRow(s)}
          {/each}
        {/if}
        {/if}

      </div>

      {#snippet groupBlock(gv: any, gi: number, siblings: string[])}
        {@const g = gv.group}
        {@const editingG = $editGroupId === g.id}
        <div class="grp-header" class:menu-open={projectMenuFor === g.id}>
          {#if $selMode}{@render triBox(triStateOf(gv.items.map((s: any) => s.id)), () => toggleMany(gv.items.map((s: any) => s.id)))}{/if}
          <!-- Folder icon doubles as the collapse toggle: open when expanded,
               closed when collapsed, so it carries both identity and state. -->
          <span class="grp-caret" onclick={() => toggleCollapse(g.id, !g.collapsed)}>
            <iconify-icon icon={g.collapsed ? 'ant-design:folder-outlined' : 'ant-design:folder-open-outlined'} width="13"></iconify-icon>
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
          <span class="grp-name" title={g.working_dir} onclick={() => toggleCollapse(g.id, !g.collapsed)}>{g.name}</span>
          {#if g.task_id}
            <!-- Marks where the project came from without giving it a section of
                 its own: it is an ordinary project, made by the scheduler. -->
            <iconify-icon class="from-cron" icon="ant-design:clock-circle-outlined" width="11" title={$t('sidebar.from_scheduled_task')}></iconify-icon>
          {/if}
          {#if !$selMode}
          <!-- Six icons used to sit here — reorder, rename, delete, new session,
               settings — and the row read as a toolbar with a name attached. Two
               remain, both things you do TO the project rather than to its
               configuration: start work in it, and open its menu. The rest are in
               the menu, where a destructive action is not one stray click away. -->
          <span class="row-action on-hover" title={$t('sidebar.project_more')} onclick={(e) => { e.stopPropagation(); projectMenuFor = projectMenuFor === g.id ? '' : g.id }}>
            <iconify-icon icon="ant-design:more-outlined" width="14"></iconify-icon>
          </span>
          <span class="row-action on-hover" title={tr('sidebar.new_session_in_group')} onclick={(e) => { e.stopPropagation(); createSessionInGroup(g.id) }}>
            <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
          </span>
          {#if projectMenuFor === g.id}
          <div class="row-menu" onclick={(e) => e.stopPropagation()}>
            <div class="row-menu-item" onclick={(e) => { e.stopPropagation(); projectMenuFor = ''; editGroupId.set(g.id); editGroupDraft.set(g.name) }}>
              <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
              <span>{$t('sidebar.rename_group')}</span>
            </div>
            {#if gi > 0}
            <div class="row-menu-item" onclick={(e) => { e.stopPropagation(); projectMenuFor = ''; moveGroup(g.id, -1, siblings) }}>
              <iconify-icon icon="ant-design:arrow-up-outlined" width="13"></iconify-icon>
              <span>{$t('sidebar.move_group_up')}</span>
            </div>
            {/if}
            {#if gi < siblings.length - 1}
            <div class="row-menu-item" onclick={(e) => { e.stopPropagation(); projectMenuFor = ''; moveGroup(g.id, 1, siblings) }}>
              <iconify-icon icon="ant-design:arrow-down-outlined" width="13"></iconify-icon>
              <span>{$t('sidebar.move_group_down')}</span>
            </div>
            {/if}
            <div class="row-menu-sep"></div>
            <div class="row-menu-item del" onclick={(e) => { e.stopPropagation(); deleteGroup(g.id, g.name, gv.items.length) }}>
              <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
              <span>{$t('sidebar.delete_group')}</span>
            </div>
          </div>
          {/if}
          {/if}
          {/if}
        </div>
        {#if !g.collapsed}
          <!-- Indented under the project's own row, the way a child sits under
               its parent — a session filed under a project is one, and looked
               flat before like it wasn't. -->
          {#each gv.items as s (s.id)}
            {@render sessionRow(s, true)}
          {/each}
        {/if}
      {/snippet}

      {#snippet sessionRow(s: any, nested = false)}
        {@const active = s.id === $activeSessionId && $view === 'chat'}
        {@const selected = !!$sel[s.id]}
        {@const editing = $editId === s.id}
        {@const menuOpen = $menuFor === s.id && !$selMode}
        {@const solid = active && !$selMode}
        <div
          class="nav-row"
          class:nested
          class:solid={solid}
          class:selected={selected && !solid}
          class:menu-open={menuOpen}
          onclick={() => {
          if ($selMode) { if (!isPinned(s.id)) toggleSel(s.id) }
          else { view.set('chat'); activeSessionId.set(s.id); menuFor.set(null) }
        }}
        >
          {#if $selMode && !isPinned(s.id)}
            {@render triBox(selected ? 'all' : 'none', () => toggleSel(s.id))}
          {/if}

          <!-- No icon in front of the name. It was one of four glyphs standing
               for the session's source, which is either obvious from the project
               it sits under or not worth a column in every row. Whether a session
               is RUNNING is worth showing, and that goes on the right where the
               timestamp it replaces was. -->
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
          <span class="session-title">{(s as any).name || (s as any).title || s.id}</span>
          <!-- Metadata gives way to the row's actions on hover (CSS, not state:
               the actions are the same width every time, so swapping them in
               must not reflow the title). -->
          {#if (s as any).agent_profile && (s as any).agent_profile !== 'default'}
            {@const aName = agentNameOf((s as any).agent_profile)}
            <span class="agent-tag on-rest" style="background:{solid ? 'rgba(255,255,255,0.2)' : 'var(--active-blue-bg)'};color:var(--blue-6);">
              {aName}
            </span>
          {/if}
          {#if (s as any).pending_question}
            <span class="pending-dot" title={$t('sidebar.pending_question')}></span>
          {/if}
          {#if (s as any).status === 'running'}
            <iconify-icon class="on-rest" icon="ant-design:loading-outlined" width="13" style="color:var(--blue-6);flex:0 0 auto;animation:octo-spin 0.8s linear infinite" title={$t('sidebar.running')}></iconify-icon>
          {:else}
            <span class="session-time on-rest" style="color:var(--text-quaternary);">
              {ago((s as any).updated_at, $t)}
            </span>
          {/if}
          {#if !$selMode}
            {@const pinned = isPinned(s.id)}
            <!-- Two actions earn a place on the row: they are the ones used
                 while scanning the list, and both are one click with nothing to
                 confirm. Everything else lives in the menu — rename opens an
                 input, delete destroys a transcript; neither belongs under a
                 cursor that is just passing over the row. Archiving a session
                 (and un-archiving one) now lives in Settings' 数据管理, not here
                 — a session dropped off this list entirely once archived, so
                 there was no row left to offer "un-archive" from anyway. -->
            <span class="row-action on-hover kebab" onclick={(e) => { e.stopPropagation(); menuFor.update(m => m === s.id ? null : s.id) }} style="color:{solid ? 'var(--blue-6)' : 'var(--text-tertiary)'}">
              <iconify-icon icon="ant-design:more-outlined" width="14"></iconify-icon>
            </span>
            {#if !pinned}
            <!-- Archiving is only illegal while pinned, matching the server's
                 guard — a session in a project archives fine and keeps its
                 membership. -->
            <span class="row-action on-hover" title={$t('sidebar.collapse')} onclick={(e) => { e.stopPropagation(); archiveSession(s.id) }}>
              <iconify-icon icon="lucide:archive" width="13"></iconify-icon>
            </span>
            {/if}
            <span class="row-action on-hover" title={pinned ? $t('sidebar.unpin') : $t('sidebar.pin')} onclick={(e) => { e.stopPropagation(); togglePin(s.id, !pinned) }}>
              <iconify-icon icon={pinned ? 'ant-design:pushpin-filled' : 'ant-design:pushpin-outlined'} width="13"></iconify-icon>
            </span>
            {#if menuOpen}
            <div class="row-menu" onclick={(e) => e.stopPropagation()}>
              {#if !pinned}
              <!-- First entry: acting on many sessions starts from one of them,
                   which is also why this one is pre-selected. Not offered on a
                   pinned session — it has no checkbox to seed a selection with,
                   so there is nothing for this to start. -->
              <div class="row-menu-item" onclick={(e) => { e.stopPropagation(); enterBatchMode(s.id) }}>
                <iconify-icon icon="ant-design:profile-outlined" width="13"></iconify-icon>
                <span>{$t('sidebar.batch_actions')}</span>
              </div>
              <div class="row-menu-sep"></div>
              {/if}
              <div class="row-menu-item" onclick={(e) => { e.stopPropagation(); menuFor.set(null); editId.set(s.id); editDraft.set((s as any).name || (s as any).title || s.id) }}>
                <iconify-icon icon="ant-design:edit-outlined" width="13"></iconify-icon>
                <span>{$t('sidebar.rename')}</span>
              </div>
              <div class="row-menu-item del" onclick={(e) => { e.stopPropagation(); menuFor.set(null); delSession(s.id) }}>
                <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
                <span>{$t('common.delete')}</span>
              </div>
            </div>
            {/if}
          {/if}
          {/if}
        </div>
      {/snippet}

    </div>

    {#if $selMode}
    <!-- Batch mode replaces the footer rather than stacking on it: while a
         selection is live, settings and the version badge are not what the
         bottom of the sidebar is for. -->
    <div class="batch-bar">
      <div class="batch-top">
        {@render triBox(triStateOf(allListedIds), () => toggleMany(allListedIds))}
        <span class="batch-label">{$t('sidebar.select_all')}</span>
        <span class="batch-count">({selCount})</span>
        <span class="batch-close" title={$t('sidebar.done')} onclick={exitBatchMode}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </span>
      </div>
      <div class="batch-actions">
        <button class="batch-btn del" disabled={selCount === 0} onclick={delSelected}>
          <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
          {$t('common.delete')}
        </button>
        <button class="batch-btn" disabled={selCount === 0} onclick={archiveSelected}>
          <iconify-icon icon="lucide:archive" width="13"></iconify-icon>
          {$t('sidebar.collapse')}
        </button>
      </div>
    </div>
    {:else}
    <div class="footer">
      <div class="footer-settings" style="color:{$settingsModalOpen ? 'var(--blue-6)' : 'var(--text-secondary)'}" onclick={() => settingsModalOpen.set(true)}>
        <iconify-icon icon="ant-design:setting-outlined" width="14"></iconify-icon>
        <span>{$t('nav.settings')}</span>
      </div>
      <VersionBadge />
    </div>
    {/if}
  </div>
  {/if}

  {#if $sidebar === 'rail'}
  <div class="rail">
    <div style="padding:16px 0 8px 0;">
      <button class="rail-btn primary" title={$t('nav.new_session')} onclick={() => createNewSession()}>
        <iconify-icon icon="ant-design:plus-outlined" width="16"></iconify-icon>
      </button>
    </div>
    <div class="rail-scroll">
      {#each railNav.slice(0, 3) as item}
      <button
        class="rail-btn"
        class:active={navActive(item.v)}
        title={$t(item.title)}
        onclick={() => view.set(item.v as any)}
      >
        <iconify-icon icon={item.icon} width="16"></iconify-icon>
      </button>
      {/each}
      <div class="more-wrap" bind:this={morePopoverEl}>
        <button class="rail-btn" class:active={moreActive()} title={$t('nav.manage')} onclick={(e) => toggleMorePopover(e.currentTarget as HTMLElement, 'rail')}>
          <iconify-icon icon="ant-design:menu-outlined" width="16"></iconify-icon>
        </button>
        {#if morePopoverOpen}
        <div class="more-popover" use:portal style="top:{morePos.top}px; left:{morePos.left}px; width:{morePos.width}px;">
          {#each moreCategories as c (c.v)}
          <button class="ap-item" onclick={() => goToMore(c.v)}>
            <iconify-icon icon={c.icon} width="14" style="color:var(--text-tertiary)"></iconify-icon>
            <span>{$t(c.label)}</span>
          </button>
          {/each}
        </div>
        {/if}
      </div>
      {#each railNav.slice(3) as item}
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
      <button class="rail-btn" class:active={$settingsModalOpen} title={$t('nav.settings')} onclick={() => settingsModalOpen.set(true)}>
        <iconify-icon icon="ant-design:setting-outlined" width="16"></iconify-icon>
      </button>
    </div>
  </div>
  {/if}
</aside>


<style>
.full { width: 256px; height: 100%; display: flex; flex-direction: column; min-height: 0; }
/* Matches .scroll's horizontal padding so the row lines up with every nav row
   below it — it is one of them, just pinned above the scrolling area. */
.ap-item {
  display: flex; align-items: center; gap: 8px; width: 100%; padding: 8px 12px; border: none;
  background: transparent; color: var(--text); font-size: 13px;
  text-align: left; cursor: pointer; border-radius: 6px;
}
.ap-item:hover { background: var(--hover-neutral); }
.more-wrap { position: relative; }
/* Portaled to <body> (see the portal action) and positioned via the anchor's
   captured rect (morePos) — fixed, not absolute, since it must escape the
   sidebar's own overflow:hidden ancestors rather than being clipped by them. */
.more-popover {
  position: fixed; z-index: 30;
  padding: 4px;
  background: var(--bg-container); border: 1px solid var(--border-secondary);
  border-radius: 8px; box-shadow: 0 6px 20px rgba(0,0,0,0.18);
}
.agent-tag {
  flex: 0 0 auto; padding: 0 5px; border-radius: 3px;
  font-size: 10px; font-weight: 600; white-space: nowrap;
  max-width: 64px; overflow: hidden; text-overflow: ellipsis;
}
.scroll { flex: 1; min-height: 0; overflow: hidden; padding: 12px 12px 8px; display: flex; flex-direction: column; gap: 18px; }
.nav-group { display: flex; flex-direction: column; gap: 2px; flex: 0 0 auto; }
/* Sessions is the only group with no forced grow-to-fill (flex-grow:0, unlike
   the "1 1 auto" this used to be) — a short list sizes to its own content
   instead of being stretched to consume whatever space Config/My Data don't
   need, which used to leave a big reserved gap above them. flex-shrink:1
   (bounded by min-height) is unchanged from before: once the combined stack
   would overflow .scroll, sessions is still the one that gives, scrolling
   internally so a long list never pushes Config/My Data out of view. */
.sessions-group { flex: 0 1 auto; min-height: 80px; overflow-y: auto; }
/* Group section header (folder row) */
/* Section heading (Tasks / Projects). Sits a tier above .grp-header: it names
   a whole kind of entry rather than one group, so it reads quieter and tighter
   than the group rows nested under it. */
.sec-header {
  display: flex; align-items: center; gap: 6px;
  min-height: 24px; padding: 0 8px; margin-top: 10px;
  color: var(--text-quaternary); cursor: pointer; user-select: none;
}
.sec-header:first-child { margin-top: 0; }
.sec-header:hover { color: var(--text-secondary); }
/* Same font and color as Pinned's own label (.grp-name.muted) — Tasks,
   Projects and Pinned are three peers at the same level, not three different
   weights of heading. The caret sits right after the name rather than leading
   it — the name is what's being scanned for, the caret is a detail about it.
   Expanded, the content below already says so, so the caret stays invisible
   until the row is hovered — folded, there is nothing below to say it, so it
   stays visible at rest. */
.sec-name {
  flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
  font-size: 12px; font-weight: 600;
}
/* Right-pointing at rest reads as "expand this way"; rotated a quarter turn
   it reads as "collapse downward" — so the base icon points right (folded)
   and rotates to point down once the section is actually open. */
.sec-caret { flex: 0 0 auto; opacity: 0; transform: rotate(90deg); transition: opacity 0.1s, transform 0.15s; }
.sec-header:hover .sec-caret,
.sec-caret.folded { opacity: 1; }
.sec-caret.folded { transform: rotate(0deg); }

.grp-header {
  position: relative;
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
/* Group-header actions in two frequency tiers. The high-frequency pair
   (new session, settings) is always visible, anchored at the right edge
   (explicit opacity:1 overrides .row-action's base opacity:0, the session
   rows' hover-reveal). The occasional ones (rename / delete / reorder)
   appear on hover and — crucially — take NO width until then: display:none,
   not opacity:0, because an invisible 22px slot per icon is what squeezed
   the group name into an ellipsis. They expand between the name and the
   anchored pair, so the icons that are always there never shift. */
.grp-header .row-action { opacity: 1; width: 20px; flex-basis: 20px; }
/* A session row swaps its metadata for its actions on hover. Kept in CSS rather
   than a hover-tracking $state: the row must not re-render (and the title must
   not reflow) just because the cursor crossed it. The menu-open case holds the
   swap while the panel is up, so the kebab the panel belongs to stays under the
   cursor that opened it. */
.nav-row .row-action.on-hover,
.grp-header .row-action.on-hover { display: none; }
.nav-row:hover .row-action.on-hover,
.nav-row.menu-open .row-action.on-hover,
.grp-header:hover .row-action.on-hover,
.grp-header.menu-open .row-action.on-hover { display: flex; }
.nav-row:hover .on-rest,
.nav-row.menu-open .on-rest { display: none; }
.row-menu {
  position: absolute; top: 100%; right: 6px; z-index: 30;
  min-width: 132px; margin-top: 2px; padding: 4px;
  background: var(--bg-container); border: 1px solid var(--border-secondary);
  border-radius: 8px; box-shadow: 0 6px 20px rgba(0,0,0,0.18);
}
.row-menu-item {
  display: flex; align-items: center; gap: 8px;
  padding: 7px 9px; border-radius: 6px; cursor: pointer;
  font-size: 13px; color: var(--text);
}
.row-menu-item:hover { background: var(--hover-neutral); }
.row-menu-item.del { color: var(--error); }
/* Sits where the footer does, so entering batch mode does not shift the list
   above it — the rows must stay under the cursor that is selecting them. */
.batch-bar {
  flex: 0 0 auto; padding: 10px 12px 12px;
  border-top: 1px solid var(--border);
  display: flex; flex-direction: column; gap: 8px;
}
.batch-top { display: flex; align-items: center; gap: 10px; }
.batch-label { font-size: 13px; color: var(--text); }
.batch-count { font-size: 12px; color: var(--text-tertiary); flex: 1; }
.batch-close {
  flex: 0 0 auto; width: 22px; height: 22px; border-radius: 5px;
  display: flex; align-items: center; justify-content: center;
  color: var(--text-tertiary); cursor: pointer;
}
.batch-close:hover { background: var(--hover-neutral); color: var(--text); }
.batch-actions { display: flex; gap: 8px; }
.batch-btn {
  flex: 1; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 7px;
  background: var(--bg-container); color: var(--text-secondary);
  display: flex; align-items: center; justify-content: center; gap: 6px;
  font: 500 13px/1 inherit; font-family: inherit; cursor: pointer;
}
.batch-btn:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-6); }
.batch-btn.del { border-color: var(--error-border); color: var(--error); }
.batch-btn.del:hover:not(:disabled) { border-color: var(--error); background: var(--error-bg); }
.batch-btn:disabled { opacity: 0.45; cursor: default; }
/* One box for a session, a group, and the whole list. */
.checkbox.tri { border-color: var(--border); background: var(--bg-container); cursor: pointer; }
.checkbox.tri.on { border-color: var(--blue-6); background: var(--blue-6); }
.row-menu-sep { height: 1px; margin: 4px 6px; background: var(--border-secondary); }
.from-cron { flex: 0 0 auto; color: var(--text-quaternary); }
.nav-row {
  position: relative;
  display: flex; align-items: center; gap: 10px;
  min-height: 34px; padding: 0 6px 0 9px;
  border-radius: 7px; cursor: pointer;
}
/* A session filed under a project is indented under it, the way a child sits
   under its parent, rather than sharing the project header's own left edge —
   which is what made a project's sessions read as siblings of the project
   instead of members of it. Aligned to the project name's own text start
   (6px padding + 16px folder icon + 6px gap), not indented past it. */
.nav-row.nested { padding-left: 28px; }
/* Active row is a soft accent tint with accent text (the redesign's
   data-on state), not a solid blue pill. */
.nav-row.solid { background: var(--active-blue-bg); }
.nav-row.solid .session-title { color: var(--blue-6); font-weight: 600; }
.nav-row.selected { background: var(--active-blue-bg); }
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
  white-space: nowrap; font-size: 13px; color: var(--text);
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
.rail-btn.active { background: var(--active-blue-bg); color: var(--blue-6); }
.rail-btn.primary { background: var(--blue-6); color: #fff; }
.rail-btn.primary:hover { background: var(--blue-5); }
</style>

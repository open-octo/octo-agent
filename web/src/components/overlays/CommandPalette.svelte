<script lang="ts">
  import { cmdkOpen, view, sessions, activeSessionId, activeSession, skills } from '../../lib/stores'
  import { t } from '../../lib/i18n'

  let query = $state('')
  let inputEl = $state<HTMLInputElement | null>(null)
  let activeIdx = $state(0)

  function close() {
    cmdkOpen.set(false)
    query = ''
    activeIdx = 0
  }

  function sessionIcon(s: any): string {
    if (s.source === 'cron') return 'ant-design:clock-circle-outlined'
    if (s.source === 'channel') return 'ant-design:send-outlined'
    if (s.status === 'working') return 'ant-design:loading-outlined'
    return 'ant-design:message-outlined'
  }

  function sessionName(s: any): string {
    return s.name || s.title || s.id
  }

  function openSession(id: string) {
    activeSessionId.set(id)
    activeSession.set(id)
    view.set('chat')
    close()
  }

  function goTo(v: string) {
    view.set(v as any)
    close()
  }

  // Static actions (always available)
  const actions = [
    { id: 'new', icon: 'ant-design:plus-outlined', label: () => t('nav.new_session'), shortcut: '⌘N', run: () => goTo('chat') },
    { id: 'skills', icon: 'ant-design:thunderbolt-outlined', label: () => t('nav.skills'), shortcut: '', run: () => goTo('skills') },
    { id: 'tasks', icon: 'ant-design:clock-circle-outlined', label: () => t('nav.tasks'), shortcut: '', run: () => goTo('tasks') },
    { id: 'mcp', icon: 'ant-design:api-outlined', label: () => t('nav.mcp'), shortcut: '', run: () => goTo('mcp') },
    { id: 'channels', icon: 'ant-design:mobile-outlined', label: () => t('nav.channels'), shortcut: '', run: () => goTo('channels') },
    { id: 'memory', icon: 'ant-design:user-outlined', label: () => t('nav.memory'), shortcut: '', run: () => goTo('profile') },
    { id: 'files', icon: 'ant-design:folder-open-outlined', label: () => t('nav.file_recall'), shortcut: '', run: () => goTo('files') },
    { id: 'settings', icon: 'ant-design:setting-outlined', label: () => t('nav.settings'), shortcut: '', run: () => goTo('settings') },
  ]

  // Reactive filtered results
  let q = $derived(query.trim().toLowerCase())

  let matchedSessions = $derived(
    $sessions.filter((s: any) => !q || sessionName(s).toLowerCase().includes(q)).slice(0, 8)
  )

  let matchedSkills = $derived(
    q ? $skills.filter((s: any) => s.name.toLowerCase().includes(q) || (s.description ?? '').toLowerCase().includes(q)).slice(0, 5) : []
  )

  let matchedActions = $derived(
    actions.filter((a) => !q || a.label().toLowerCase().includes(q))
  )

  // Flat list for keyboard navigation
  let flatItems = $derived([
    ...matchedSessions.map((s: any) => ({ kind: 'session', data: s })),
    ...matchedSkills.map((s: any) => ({ kind: 'skill', data: s })),
    ...matchedActions.map((a) => ({ kind: 'action', data: a })),
  ])

  // Clamp activeIdx whenever the list changes
  $effect(() => {
    if (activeIdx >= flatItems.length) activeIdx = Math.max(0, flatItems.length - 1)
  })

  // Autofocus when opened
  $effect(() => {
    if ($cmdkOpen && inputEl) {
      inputEl.focus()
    }
  })

  function runItem(item: any) {
    if (!item) return
    if (item.kind === 'session') openSession(item.data.id)
    else if (item.kind === 'skill') { view.set('skills'); close() }
    else if (item.kind === 'action') item.data.run()
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); close() }
    else if (e.key === 'ArrowDown') { e.preventDefault(); activeIdx = Math.min(activeIdx + 1, flatItems.length - 1) }
    else if (e.key === 'ArrowUp') { e.preventDefault(); activeIdx = Math.max(activeIdx - 1, 0) }
    else if (e.key === 'Enter') { e.preventDefault(); runItem(flatItems[activeIdx]) }
  }

  // Index helpers so each section knows its global offset for highlight
  let skillsOffset = $derived(matchedSessions.length)
  let actionsOffset = $derived(matchedSessions.length + matchedSkills.length)
</script>

{#if $cmdkOpen}
<div class="backdrop" onclick={close}>
  <div class="palette" onclick={(e) => e.stopPropagation()}>
    <div class="search-row">
      <iconify-icon icon="ant-design:search-outlined" width="16" style="color:rgba(0,0,0,0.45)"></iconify-icon>
      <input
        bind:this={inputEl}
        bind:value={query}
        onkeydown={onKeydown}
        class="search-input"
        placeholder="Search sessions, skills, commands…"
      />
      <kbd>esc</kbd>
    </div>
    <div class="results">
      {#if flatItems.length === 0}
        <div class="empty">No matches for "{query}"</div>
      {/if}

      {#if matchedSessions.length > 0}
        <div class="group-label">{t('nav.sessions')}</div>
        {#each matchedSessions as s, i (s.id)}
          <div
            class="result-row"
            class:active={activeIdx === i}
            onclick={() => openSession(s.id)}
            onmouseenter={() => activeIdx = i}
          >
            <iconify-icon icon={sessionIcon(s)} width="14" style="color:{activeIdx === i ? '#1677FF' : 'rgba(0,0,0,0.45)'}"></iconify-icon>
            <span class="result-title" class:dim={activeIdx !== i}>{sessionName(s)}</span>
            {#if activeIdx === i}
              <iconify-icon icon="lucide:corner-down-left" width="13" style="color:rgba(0,0,0,0.35)"></iconify-icon>
            {/if}
          </div>
        {/each}
      {/if}

      {#if matchedSkills.length > 0}
        <div class="group-label">{t('nav.skills')}</div>
        {#each matchedSkills as s, i (s.name)}
          {@const gi = skillsOffset + i}
          <div
            class="result-row"
            class:active={activeIdx === gi}
            onclick={() => { view.set('skills'); close() }}
            onmouseenter={() => activeIdx = gi}
          >
            <iconify-icon icon="ant-design:thunderbolt-outlined" width="14" style="color:{activeIdx === gi ? '#1677FF' : 'rgba(0,0,0,0.45)'}"></iconify-icon>
            <span class="result-title mono" class:dim={activeIdx !== gi}>{s.name}</span>
          </div>
        {/each}
      {/if}

      {#if matchedActions.length > 0}
        <div class="group-label">ACTIONS</div>
        {#each matchedActions as a, i (a.id)}
          {@const gi = actionsOffset + i}
          <div
            class="result-row"
            class:active={activeIdx === gi}
            onclick={() => a.run()}
            onmouseenter={() => activeIdx = gi}
          >
            <iconify-icon icon={a.icon} width="14" style="color:{activeIdx === gi ? '#1677FF' : 'rgba(0,0,0,0.45)'}"></iconify-icon>
            <span class="result-title" class:dim={activeIdx !== gi}>{a.label()}</span>
            {#if a.shortcut}<span class="shortcut mono">{a.shortcut}</span>{/if}
          </div>
        {/each}
      {/if}
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000;
  background: rgba(0,0,0,0.35);
  display: flex; align-items: flex-start; justify-content: center; padding-top: 12vh;
}
.palette {
  width: 92%; max-width: 520px; background: #fff;
  border-radius: 12px; overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.search-row {
  display: flex; align-items: center; gap: 10px;
  padding: 12px 16px; border-bottom: 1px solid #F0F0F0;
}
.search-input {
  flex: 1; border: none; outline: none; background: transparent;
  font-size: 14px; color: rgba(0,0,0,0.88); font-family: inherit;
}
.search-input::placeholder { color: rgba(0,0,0,0.45); }
kbd {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: #FAFAFA; border: 1px solid #EEEFF1; border-radius: 4px;
  padding: 1px 6px; color: rgba(0,0,0,0.45);
}
.results { padding: 8px; max-height: 360px; overflow-y: auto; }
.empty { padding: 16px 8px; font-size: 13px; color: rgba(0,0,0,0.45); text-align: center; }
.group-label { font-size: 11px; font-weight: 600; letter-spacing: 0.4px; color: rgba(0,0,0,0.35); padding: 6px 8px; }
.result-row {
  display: flex; align-items: center; gap: 10px;
  padding: 8px; border-radius: 6px; cursor: pointer;
}
.result-row.active { background: rgba(22,119,255,0.06); }
.result-title { font-size: 13px; color: rgba(0,0,0,0.88); flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.result-title.dim { color: rgba(0,0,0,0.65); }
.shortcut { font-size: 11px; color: rgba(0,0,0,0.35); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

<script lang="ts">
  import { onMount } from 'svelte'
  import { t, tr, pickLocalized, pickLocalizedList } from '../lib/i18n'
  import { showToast, openAgentSession } from '../lib/stores'
  import { confirmDialog } from '../lib/confirm'
  import * as api from '../lib/api'
  import AgentDetailModal from '../components/overlays/AgentDetailModal.svelte'

  let agents: api.Agent[] = $state([])
  let loading = $state(true)
  let query = $state('')
  let activeCategory = $state('all')
  let selectedAgent: api.Agent | null = $state(null)

  // Fixed, closed set of category chips — 'mine' buckets every user-created
  // agent (which has no curated `category`), plus any curated expert that
  // somehow ships without one. Order matches the CATEGORIES declared for
  // curated content, so new categories just need a matching i18n key.
  const CATEGORY_ORDER = ['content-creation', 'life', 'learning', 'productivity', 'career']

  async function loadAgents() {
    loading = true
    try {
      agents = await api.listAgents()
    } catch (err) {
      showToast('Failed to load agents: ' + (err as Error).message, 'error')
    } finally {
      loading = false
    }
  }

  async function handleDelete(agent: api.Agent) {
    const confirmed = await confirmDialog(
      $t('agents.delete_confirm').replace('{name}', agentName(agent))
    )
    if (!confirmed) return
    try {
      await api.deleteAgent(agent.id)
      showToast('Agent deleted', 'success')
      await loadAgents()
    } catch (err) {
      showToast('Failed to delete agent: ' + (err as Error).message, 'error')
    }
  }

  async function handleToggle(agent: api.Agent) {
    try {
      await api.toggleAgent(agent.id)
      await loadAgents()
    } catch (err) {
      showToast('Failed to update agent: ' + (err as Error).message, 'error')
    }
  }

  // Agentic-first: create and edit through conversation with the
  // expert-agent-manager skill. Curated experts are read-only, so only the
  // user's own agents offer this.
  function handleCreateWithAgent() {
    openAgentSession('/expert-agent-manager', tr('agents.session_new'))
  }

  function handleEditWithAgent(agent: api.Agent, e: MouseEvent) {
    e.stopPropagation()
    openAgentSession(`/expert-agent-manager edit ${agent.id}`, tr('agents.session_edit').replace('{name}', agentName(agent)))
  }

  function agentName(agent: api.Agent): string {
    return pickLocalized(agent.name, agent.name_en)
  }
  function agentDesc(agent: api.Agent): string {
    return pickLocalized(agent.description, agent.description_en)
  }
  function isHidden(agent: api.Agent): boolean {
    return agent.enabled === false
  }
  function agentCategory(agent: api.Agent): string {
    return agent.category && CATEGORY_ORDER.includes(agent.category) ? agent.category : 'mine'
  }

  function agentInitial(name: string): string {
    return name.charAt(0).toUpperCase()
  }
  function agentIconColor(name: string): string {
    const colors = ['#1677ff', '#722ed1', '#13c2c2', '#52c41a', '#eb2f96', '#fa8c16', '#2f54eb', '#a0d911']
    let hash = 0
    for (let i = 0; i < name.length; i++) {
      hash = name.charCodeAt(i) + ((hash << 5) - hash)
    }
    return colors[Math.abs(hash) % colors.length]
  }

  let categoriesPresent = $derived(
    CATEGORY_ORDER.filter(c => agents.some(a => agentCategory(a) === c))
  )

  let filtered = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return agents.filter(a => {
      if (activeCategory !== 'all' && agentCategory(a) !== activeCategory) return false
      if (!q) return true
      const name = agentName(a).toLowerCase()
      const desc = agentDesc(a).toLowerCase()
      const tags = pickLocalizedList(a.tags, a.tags_en).join(' ').toLowerCase()
      return name.includes(q) || desc.includes(q) || tags.includes(q)
    })
  })

  onMount(loadAgents)
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('nav.agents')}</h2>
        <p>{$t('agents.desc')}</p>
      </div>
      <div class="header-actions">
        <button class="btn-primary" onclick={handleCreateWithAgent}>
          <iconify-icon icon="ant-design:message-outlined" width="14"></iconify-icon>
          {$t('agents.create_with_agent')}
        </button>
      </div>
    </div>

    {#if !loading && agents.length > 0}
      <div class="toolbar">
        <div class="search-box">
          <iconify-icon icon="ant-design:search-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
          <input bind:value={query} placeholder={$t('agents.search_placeholder')} />
        </div>
        <div class="chip-row">
          <button class="chip" class:active={activeCategory === 'all'} onclick={() => activeCategory = 'all'}>
            {$t('agents.category.all')}
          </button>
          {#each categoriesPresent as cat}
            <button class="chip" class:active={activeCategory === cat} onclick={() => activeCategory = cat}>
              {$t(`agents.category.${cat}`)}
            </button>
          {/each}
          {#if agents.some(a => agentCategory(a) === 'mine')}
            <button class="chip" class:active={activeCategory === 'mine'} onclick={() => activeCategory = 'mine'}>
              {$t('agents.category.mine')}
            </button>
          {/if}
        </div>
      </div>
    {/if}

    {#if loading}
      <div class="empty-state">
        <div class="spinner"></div>
        <span>{$t('common.loading')}</span>
      </div>
    {:else if agents.length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:robot-outlined" width="32"></iconify-icon>
        <span>{$t('agents.empty')}</span>
        <button class="btn-primary" onclick={handleCreateWithAgent}>{$t('agents.create_with_agent')}</button>
      </div>
    {:else if filtered.length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:search-outlined" width="32"></iconify-icon>
        <span>{$t('cmdk.no_matches').replace('{query}', query)}</span>
      </div>
    {:else}
      <div class="agent-grid">
        {#each filtered as agent (agent.id)}
          <div class="agent-card" class:is-hidden={isHidden(agent)} onclick={() => selectedAgent = agent}>
            <div class="card-top">
              {#if agent.icon}
                <span class="agent-icon" style="background-color: {agentIconColor(agentName(agent))}11; color: {agentIconColor(agentName(agent))}">
                  <iconify-icon icon={agent.icon} width="18"></iconify-icon>
                </span>
              {:else}
                <span class="agent-icon" style="background-color: {agentIconColor(agentName(agent))}11; color: {agentIconColor(agentName(agent))}">
                  {agentInitial(agentName(agent))}
                </span>
              {/if}
              <div class="card-actions">
                {#if agent.source === 'default'}
                  <!-- A curated expert is read-only: it is the same on every
                       machine and keeps receiving content updates. Hiding it
                       is the only knob, so editing isn't offered rather than
                       offered-and-refused. -->
                  <button class="act-btn" title={isHidden(agent) ? $t('agents.show') : $t('agents.hide')} onclick={(e) => { e.stopPropagation(); handleToggle(agent) }}>
                    <iconify-icon icon={isHidden(agent) ? 'ant-design:eye-outlined' : 'ant-design:eye-invisible-outlined'} width="13"></iconify-icon>
                  </button>
                {:else}
                  <button class="act-btn" title={$t('agents.edit_with_agent')} onclick={(e) => handleEditWithAgent(agent, e)}>
                    <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
                  </button>
                  <button class="act-btn del" title={$t('common.delete')} onclick={(e) => { e.stopPropagation(); handleDelete(agent) }}>
                    <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
                  </button>
                {/if}
              </div>
            </div>
            <span class="agent-name">{agentName(agent)}</span>
            <span class="agent-desc">{agentDesc(agent)}</span>
            <div class="card-bottom">
              {#if isHidden(agent)}
                <span class="transport-badge hidden-badge">{$t('agents.hidden')}</span>
              {/if}
              {#if agent.model}
                <span class="transport-badge mono">{$t('agents.model')}: {agent.model}</span>
              {/if}
              {#if agent.tools && agent.tools.length > 0}
                <span class="transport-badge">{agent.tools.length} {$t('agents.tools')}</span>
              {:else if agent.source !== 'default'}
                <span class="transport-badge muted">{$t('agents.all_tools')}</span>
              {/if}
              {#if agent.tool_skills && agent.tool_skills.length > 0}
                <!-- An expert's skills are as much a part of what it can do as
                     its tools, and they are configured the same way — the card
                     said nothing about them before. -->
                <span class="transport-badge" title={agent.tool_skills.join(', ')}>
                  {agent.tool_skills.length} {$t('agents.skills')}
                </span>
              {/if}
              {#if agent.channel_bindings && agent.channel_bindings.length > 0}
                <span class="transport-badge">{agent.channel_bindings.length} {$t('agents.bound_chats')}</span>
              {/if}
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if selectedAgent}
  <AgentDetailModal agent={selectedAgent} onClose={() => selectedAgent = null} />
{/if}

<style>
/* ── layout ──────────────────────────────────────────────────────────────── */
.page  { flex: 1; overflow-y: auto; min-height: 0; background: var(--bg-layout); }
.inner { max-width: 1160px; margin: 0 auto; padding: 26px 28px 40px; display: flex; flex-direction: column; gap: 20px; }

/* ── page header ─────────────────────────────────────────────────────────── */
.page-header  { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.title-block  { display: flex; flex-direction: column; }
h2 { margin: 0; font-size: 22px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-heading); }
p  { margin: 4px 0 0; font-size: 13px; color: var(--text-secondary); max-width: 60ch; }
.header-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }

/* ── buttons (mirror McpView) ────────────────────────────────────────────── */
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 8px; font-size: 13px; font-weight: 600; color: #fff; cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
  box-shadow: 0 1px 2px rgba(0,122,255,0.35);
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── toolbar: search + category chips ───────────────────────────────────── */
.toolbar { display: flex; flex-direction: column; gap: 12px; }
.search-box {
  display: flex; align-items: center; gap: 8px;
  height: 36px; padding: 0 12px; max-width: 360px;
  border: 1px solid var(--border); border-radius: 8px; background: var(--bg-container);
}
.search-box input {
  flex: 1; border: none; outline: none; background: transparent;
  font-size: 13px; color: var(--text); font-family: inherit;
}
.search-box input::placeholder { color: var(--text-tertiary); }
.chip-row { display: flex; gap: 8px; flex-wrap: wrap; }
.chip {
  height: 28px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 999px; font-size: 12.5px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit;
}
.chip:hover { border-color: var(--blue-2); color: var(--blue-6); }
.chip.active { border-color: var(--blue-6); background: var(--blue-1); color: var(--blue-6); font-weight: 600; }

/* ── agent grid ──────────────────────────────────────────────────────────── */
.agent-grid {
  display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: 16px;
}

.agent-card {
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); box-shadow: var(--card-shadow);
  padding: 16px; display: flex; flex-direction: column; gap: 8px;
  cursor: pointer; transition: border-color 0.15s, transform 0.15s;
}
.agent-card:hover { border-color: var(--blue-2); transform: translateY(-1px); }
/* A hidden curated expert stays in the gallery — dimmed, with its actions
   always visible so the re-show button is reachable without hunting. */
.agent-card.is-hidden { opacity: 0.5; border-style: dashed; }
.agent-card.is-hidden:hover { opacity: 0.75; }
.agent-card.is-hidden .card-actions { opacity: 1; }

.card-top { display: flex; align-items: flex-start; justify-content: space-between; }
.agent-icon {
  width: 36px; height: 36px; flex: 0 0 36px; border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  font-size: 15px; font-weight: 600;
}

.card-actions { display: flex; align-items: center; gap: 2px; opacity: 0; transition: opacity 0.15s; }
.agent-card:hover .card-actions { opacity: 1; }
.act-btn {
  width: 24px; height: 24px; border: none; background: transparent;
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
}
.act-btn:hover:not(:disabled)      { background: var(--hover-neutral); color: var(--text); }
.act-btn.del:hover:not(:disabled)  { background: var(--error-bg); color: var(--error); }

.agent-name { font-size: 14.5px; font-weight: 600; color: var(--text-heading); }

.agent-desc {
  font-size: 12.5px; color: var(--text-secondary); line-height: 1.5;
  display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden;
}

.card-bottom { display: flex; gap: 6px; flex-wrap: wrap; margin-top: auto; padding-top: 4px; }
.transport-badge {
  height: 20px; padding: 0 7px; border: 1px solid var(--border-secondary); background: var(--bg-table-header);
  border-radius: 4px; display: flex; align-items: center; font-size: 11px; color: var(--text-tertiary);
}
.transport-badge.muted { opacity: 0.6; }
.transport-badge.hidden-badge { border-color: var(--border); color: var(--text-secondary); }

/* ── empty state ─────────────────────────────────────────────────────────── */
.empty-state {
  display: flex; flex-direction: column; align-items: center; gap: 14px;
  padding: 64px 0; color: var(--text-tertiary); font-size: 14px;
}

/* ── loading spinner ─────────────────────────────────────────────────────── */
.spinner {
  width: 18px; height: 18px; border: 2px solid var(--blue-2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: spin 0.6s linear infinite; flex: 0 0 18px;
}

@keyframes spin { to { transform: rotate(360deg); } }
</style>

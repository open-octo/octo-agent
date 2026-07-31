<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import { showToast, openAgentSession } from '../lib/stores'
  import { confirmDialog } from '../lib/confirm'
  import * as api from '../lib/api'

  let agents: api.Agent[] = []
  let loading = true

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
      $t('agents.delete_confirm').replace('{name}', agent.name)
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

  // Agentic-first: create and edit through conversation with the
  // expert-agent-manager skill.
  function handleCreateWithAgent() {
    openAgentSession('/expert-agent-manager', 'New agent')
  }

  function handleEditWithAgent(agent: api.Agent) {
    openAgentSession(`/expert-agent-manager edit ${agent.id}`, `Edit agent: ${agent.name}`)
  }

  // Derive icon avatar from agent name
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
    {:else}
      <div class="agent-list">
        {#each agents as agent (agent.id)}
          <div class="agent-card">
            <span class="agent-icon" style="background-color: {agentIconColor(agent.name)}11; color: {agentIconColor(agent.name)}">
              {agentInitial(agent.name)}
            </span>
            <div class="agent-info">
              <div class="agent-title-row">
                <span class="agent-name">{agent.name}</span>
                {#if agent.model}
                  <span class="transport-badge mono">{$t('agents.model')}: {agent.model}</span>
                {/if}
                {#if agent.tools && agent.tools.length > 0}
                  <span class="transport-badge">{agent.tools.length} {$t('agents.tools')}</span>
                {:else}
                  <span class="transport-badge muted">{$t('agents.all_tools')}</span>
                {/if}
                {#if agent.channel_bindings && agent.channel_bindings.length > 0}
                  <span class="transport-badge">{agent.channel_bindings.length} {$t('agents.bound_chats')}</span>
                {/if}
              </div>
              <span class="agent-desc">{agent.description}</span>
              {#if agent.tool_skills && agent.tool_skills.length > 0}
                <div class="agent-skills">
                  {#each agent.tool_skills as skill}
                    <span class="skill-chip">{skill}</span>
                  {/each}
                </div>
              {/if}
            </div>
            <div class="agent-actions">
              <button
                class="act-btn"
                title={$t('agents.edit_with_agent')}
                onclick={() => handleEditWithAgent(agent)}
              >
                <iconify-icon icon="ant-design:message-outlined" width="14"></iconify-icon>
              </button>
              <button
                class="act-btn del"
                title={$t('common.delete')}
                onclick={() => handleDelete(agent)}
              >
                <iconify-icon icon="ant-design:delete-outlined" width="14"></iconify-icon>
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
/* ── layout ──────────────────────────────────────────────────────────────── */
.page  { flex: 1; overflow-y: auto; min-height: 0; background: var(--bg-layout); }
.inner { max-width: 1000px; margin: 0 auto; padding: 26px 28px 40px; display: flex; flex-direction: column; gap: 20px; }

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

/* ── agent list ──────────────────────────────────────────────────────────── */
.agent-list { display: flex; flex-direction: column; gap: 16px; }

.agent-card {
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); box-shadow: var(--card-shadow);
  padding: 18px 24px; display: flex; align-items: center; gap: 16px;
  transition: opacity 0.2s;
}

.agent-icon {
  width: 36px; height: 36px; flex: 0 0 36px; border-radius: 10px;
  display: flex; align-items: center; justify-content: center;
  font-size: 15px; font-weight: 600;
}

.agent-info       { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.agent-title-row  { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.agent-name       { font-size: 15px; font-weight: 600; color: var(--text-heading); }

.transport-badge {
  height: 20px; padding: 0 7px; border: 1px solid var(--border-secondary); background: var(--bg-table-header);
  border-radius: 4px; display: flex; align-items: center; font-size: 11px; color: var(--text-tertiary);
}
.transport-badge.muted { opacity: 0.6; }

.agent-desc {
  font-size: 13px; color: var(--text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

/* ── skill chips ─────────────────────────────────────────────────────────── */
.agent-skills { display: flex; gap: 6px; flex-wrap: wrap; }
.skill-chip {
  height: 20px; padding: 0 7px; background: var(--blue-1); color: var(--blue-6);
  border-radius: 4px; display: flex; align-items: center; font-size: 11px;
}

/* ── agent actions ───────────────────────────────────────────────────────── */
.agent-actions { display: flex; align-items: center; gap: 4px; flex: 0 0 auto; }

.act-btn {
  width: 28px; height: 28px; border: none; background: transparent;
  border-radius: 7px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
}
.act-btn:hover:not(:disabled)      { background: var(--hover-neutral); color: var(--text); }
.act-btn.del:hover:not(:disabled)  { background: var(--error-bg); color: var(--error); }
.act-btn:disabled { opacity: 0.35; cursor: not-allowed; }

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

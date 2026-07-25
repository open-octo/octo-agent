<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import { showToast, openAgentSession } from '../lib/stores'
  import { confirmDialog } from '../lib/confirm'
  import * as api from '../lib/api'
  import AgentEdit from './AgentEdit.svelte'

  let agents: api.Agent[] = []
  let editing: api.Agent | null = null
  let deleting: api.Agent | null = null
  let creating = false
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

  async function handleSave(agent: api.Agent) {
    try {
      if (creating) {
        await api.createAgent(agent)
        showToast('Agent created', 'success')
      } else {
        await api.updateAgent(agent.id, agent)
        showToast('Agent updated', 'success')
      }
      editing = null
      creating = false
      await loadAgents()
    } catch (err) {
      showToast('Failed to save agent: ' + (err as Error).message, 'error')
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
      deleting = null
      await loadAgents()
    } catch (err) {
      showToast('Failed to delete agent: ' + (err as Error).message, 'error')
    }
  }

  // Agentic-first: create an agent through conversation with the expert-agent-manager
  // meta-skill, mirroring the skill-creator flow in SkillsView.
  function handleCreateWithAgent() {
    openAgentSession('/expert-agent-manager', 'New agent')
  }

  function handleCreate() {
    editing = null
    creating = true
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
        <button class="btn-secondary" onclick={handleCreateWithAgent}>
          <iconify-icon icon="ant-design:message-outlined" width="14"></iconify-icon>
          {$t('agents.create_with_agent')}
        </button>
        <button class="btn-primary" onclick={handleCreate}>
          <iconify-icon icon="ant-design:plus-outlined" width="14"></iconify-icon>
          {$t('agents.create')}
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
        <button class="btn-primary" onclick={handleCreate}>{$t('agents.create')}</button>
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
                title={$t('common.edit')}
                onclick={() => { editing = {...agent}; creating = false }}
              >
                <iconify-icon icon="ant-design:edit-outlined" width="14"></iconify-icon>
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

{#if editing || creating}
  <AgentEdit
    agent={editing}
    onSave={handleSave}
    onCancel={() => { editing = null; creating = false }}
  />
{/if}

<style>
/* ── layout ──────────────────────────────────────────────────────────────── */
.page  { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }

/* ── page header ─────────────────────────────────────────────────────────── */
.page-header  { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.title-block  { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p  { margin: 0; font-size: 14px; color: var(--text-secondary); }
.header-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }

/* ── buttons (mirror McpView) ────────────────────────────────────────────── */
.btn-primary {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }

.btn-secondary {
  height: 32px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── agent list ──────────────────────────────────────────────────────────── */
.agent-list { display: flex; flex-direction: column; gap: 16px; }

.agent-card {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
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
  width: 30px; height: 30px; border: 1px solid var(--border-secondary); background: var(--bg-container);
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.act-btn:hover:not(:disabled)      { border-color: var(--blue-5); color: var(--blue-5); }
.act-btn.del:hover:not(:disabled)  { border-color: var(--error); color: var(--error); }
.act-btn:disabled { opacity: 0.35; cursor: not-allowed; }

/* ── empty state ─────────────────────────────────────────────────────────── */
.empty-state {
  display: flex; flex-direction: column; align-items: center; gap: 14px;
  padding: 64px 0; color: var(--text-tertiary); font-size: 14px;
}

/* ── loading spinner ─────────────────────────────────────────────────────── */
.spinner {
  width: 18px; height: 18px; border: 2px solid rgba(22,119,255,0.2);
  border-top-color: var(--blue-6); border-radius: 50%;
  animation: spin 0.6s linear infinite; flex: 0 0 18px;
}

@keyframes spin { to { transform: rotate(360deg); } }
</style>

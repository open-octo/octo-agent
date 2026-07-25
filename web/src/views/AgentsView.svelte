<script lang="ts">
  import { onMount } from 'svelte'
  import { t } from '../lib/i18n'
  import { showToast } from '../lib/stores'
  import * as api from '../lib/api'

  interface Agent {
    id: string
    name: string
    description: string
    model?: string
    tools?: string[]
    tool_skills?: string[]
    mention_as?: string[]
    channel_bindings?: { platform: string; chat_id: string }[]
  }

  let agents: Agent[] = []
  let editing: Agent | null = null
  let deleting: Agent | null = null
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

  async function handleSave(agent: Agent) {
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

  async function handleDelete(agent: Agent) {
    if (!confirm(`Delete agent "${agent.name}" (${agent.id})? This cannot be undone.`)) return
    try {
      await api.deleteAgent(agent.id)
      showToast('Agent deleted', 'success')
      deleting = null
      await loadAgents()
    } catch (err) {
      showToast('Failed to delete agent: ' + (err as Error).message, 'error')
    }
  }

  onMount(loadAgents)
</script>

<div class="agents-view">
  <div class="header">
    <h2>{$t('nav.agents')}</h2>
    <button class="primary" onclick={() => { editing = null; creating = true }}>
      {$t('agents.create')}
    </button>
  </div>

  {#if loading}
    <div class="empty">{$t('common.loading')}</div>
  {:else if agents.length === 0}
    <div class="empty">{$t('agents.empty')}</div>
  {:else}
    <div class="agent-list">
      {#each agents as agent (agent.id)}
        <div class="agent-card">
          <div class="agent-info">
            <h3>{agent.name}</h3>
            <p class="description">{agent.description}</p>
            <div class="meta">
              {#if agent.model}<span class="badge">{$t('agents.model')}: {agent.model}</span>{/if}
              {#if agent.tools && agent.tools.length > 0}
                <span class="badge">{agent.tools.length} {$t('agents.tools')}</span>
              {:else}
                <span class="badge muted">{$t('agents.all_tools')}</span>
              {/if}
              {#if agent.channel_bindings && agent.channel_bindings.length > 0}
                <span class="badge">{agent.channel_bindings.length} {$t('agents.bound_chats')}</span>
              {/if}
            </div>
          </div>
          <div class="actions">
            <button class="icon" onclick={() => { editing = {...agent}; creating = false }} title={$t('common.edit')}>
              <iconify-icon icon="ant-design:edit-outlined" width="16"></iconify-icon>
            </button>
            <button class="icon danger" onclick={() => deleting = agent} title={$t('common.delete')}>
              <iconify-icon icon="ant-design:delete-outlined" width="16"></iconify-icon>
            </button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
</div>

{#if editing || creating}
  <AgentEdit
    agent={editing}
    onSave={handleSave}
    onCancel={() => { editing = null; creating = false }}
  />
{/if}

{#if deleting}
  <ConfirmDialog
    title={$t('agents.delete_title')}
    message={$t('agents.delete_confirm', { name: deleting.name })}
    confirmText={$t('common.delete')}
    onConfirm={() => handleDelete(deleting!)}
    onCancel={() => deleting = null}
  />
{/if}

<style>
  .agents-view { padding: 24px; max-width: 800px; }
  .header { display: flex; justify-content: space-between; align-items: center; margin-bottom: 24px; }
  .header h2 { margin: 0; font-size: 20px; font-weight: 600; }
  .agent-list { display: flex; flex-direction: column; gap: 12px; }
  .agent-card {
    display: flex; justify-content: space-between; align-items: flex-start;
    padding: 16px; border: 1px solid var(--border-secondary); border-radius: 8px;
    background: var(--bg-container);
  }
  .agent-info h3 { margin: 0 0 4px; font-size: 16px; font-weight: 600; }
  .description { margin: 0 0 12px; color: var(--text-secondary); font-size: 13px; }
  .meta { display: flex; gap: 8px; flex-wrap: wrap; }
  .badge {
    font-size: 11px; padding: 2px 8px; border-radius: 4px;
    background: var(--bg-hover); color: var(--text-secondary);
  }
  .badge.muted { opacity: 0.6; }
  .actions { display: flex; gap: 4px; }
  .icon {
    padding: 6px; border-radius: 4px; background: transparent; border: none;
    cursor: pointer; color: var(--text-secondary);
    &:hover { background: var(--bg-hover); color: var(--text-primary); }
    &.danger:hover { color: var(--error); }
  }
  .empty { text-align: center; padding: 40px; color: var(--text-secondary); }
  .primary {
    padding: 8px 16px; background: var(--primary); color: white; border: none;
    border-radius: 6px; font-size: 13px; font-weight: 500; cursor: pointer;
    &:hover { opacity: 0.9; }
  }
</style>

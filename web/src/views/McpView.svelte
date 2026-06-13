<script lang="ts">
  import { onMount } from 'svelte'
  import { mcpServers, toolSearchMode, mcpModalOpen, mcpModalState, showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import * as api from '../lib/api'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import Segment from '../components/ui/Segment.svelte'

  // ─── local state ────────────────────────────────────────────────────────────

  let loading = $state(false)

  // ─── icon map ───────────────────────────────────────────────────────────────

  const iconMap: Record<string, string> = {
    github:     'ant-design:github-outlined',
    filesystem: 'ant-design:folder-outlined',
    fs:         'ant-design:folder-outlined',
    postgres:   'ant-design:database-outlined',
    sqlite:     'ant-design:database-outlined',
    db:         'ant-design:database-outlined',
    fetch:      'ant-design:global-outlined',
    web:        'ant-design:global-outlined',
    browser:    'ant-design:global-outlined',
    slack:      'ant-design:message-outlined',
    notion:     'ant-design:file-text-outlined',
    jira:       'ant-design:project-outlined',
    linear:     'ant-design:project-outlined',
    search:     'ant-design:search-outlined',
    memory:     'ant-design:bulb-outlined',
    git:        'ant-design:branches-outlined',
    docker:     'ant-design:cloud-server-outlined',
    k8s:        'ant-design:cloud-outlined',
    kubernetes: 'ant-design:cloud-outlined',
    time:       'ant-design:clock-circle-outlined',
    clock:      'ant-design:clock-circle-outlined',
    brave:      'ant-design:search-outlined',
  }

  function serverIcon(name: string): string {
    const lower = name.toLowerCase()
    for (const [key, icon] of Object.entries(iconMap)) {
      if (lower.includes(key)) return icon
    }
    return 'ant-design:api-outlined'
  }

  // ─── status tag mapping ──────────────────────────────────────────────────────

  type TagStatus = 'success' | 'error' | 'default'

  function statusTag(status: string): { tagStatus: TagStatus; tagLabel: string } {
    switch (status) {
      case 'connected':    return { tagStatus: 'success', tagLabel: tr('status.connected') }
      case 'error':        return { tagStatus: 'error',   tagLabel: tr('status.error') }
      case 'invalid':      return { tagStatus: 'error',   tagLabel: tr('mcp.status_invalid') }
      case 'disabled':     return { tagStatus: 'default', tagLabel: tr('status.disabled') }
      case 'disconnected': return { tagStatus: 'default', tagLabel: tr('mcp.status_disconnected') }
      default:             return { tagStatus: 'default', tagLabel: status }
    }
  }

  // ─── data loading ────────────────────────────────────────────────────────────

  async function reload() {
    loading = true
    try {
      const data = await api.listMcpServers()
      mcpServers.set(data.servers as any)
      toolSearchMode.set(data.tool_search.enabled)
    } catch (e: any) {
      showToast(e.message ?? 'Failed to load MCP servers', 'error')
    } finally {
      loading = false
    }
  }

  onMount(reload)

  // ─── add / edit / import / AI setup ───────────────────────────────────────────

  function openAdd() {
    mcpModalState.set({ mode: 'add' })
    mcpModalOpen.set(true)
  }

  function openEdit(srv: any) {
    mcpModalState.set({ mode: 'edit', server: srv })
    mcpModalOpen.set(true)
  }

  function openImport() {
    mcpModalState.set({ mode: 'import' })
    mcpModalOpen.set(true)
  }

  // Agentic-first: open a fresh chat that invokes the mcp-creator skill, which
  // walks the user through picking + configuring a server in conversation.
  function aiSetup() {
    openAgentSession('/mcp-creator', 'MCP Setup')
  }

  // ─── delete server ──────────────────────────────────────────────────────────

  async function deleteServer(name: string) {
    try {
      await api.deleteMcpServer(name)
      mcpServers.update(list => (list as any[]).filter((s: any) => s.name !== name))
      showToast('Server removed')
    } catch (e: any) {
      showToast(e.message ?? 'Failed to delete server', 'error')
    }
  }

  // ─── toggle enabled ─────────────────────────────────────────────────────────

  async function toggleServer(name: string, currentEnabled: boolean) {
    const newEnabled = !currentEnabled
    try {
      await api.toggleMcpServer(name, newEnabled)
      mcpServers.update(list =>
        (list as any[]).map((s: any) =>
          s.name === name ? { ...s, disabled: !newEnabled } : s
        )
      )
    } catch (e: any) {
      showToast(e.message ?? 'Failed to toggle server', 'error')
    }
  }

  // ─── reconnect ──────────────────────────────────────────────────────────────

  async function reconnect(name: string) {
    try {
      await api.reconnectMcpServer(name)
      showToast('Reconnecting ' + name + '…')
      setTimeout(reload, 1500)
    } catch (e: any) {
      showToast(e.message ?? 'Failed to reconnect', 'error')
    }
  }

  // ─── tool search segment ────────────────────────────────────────────────────

  async function onToolSearchChange(newMode: string) {
    const mode = newMode.toLowerCase() as 'auto' | 'on' | 'off'
    try {
      await api.updateToolSearch(mode)
      toolSearchMode.set(mode)
    } catch (e: any) {
      showToast(e.message ?? 'Failed to update tool search', 'error')
    }
  }

  // Capitalize first letter for segment display value
  function capitalize(s: string): string {
    return s ? s.charAt(0).toUpperCase() + s.slice(1) : 'Auto'
  }
</script>

<div class="page">
  <div class="inner">

    <!-- Header -->
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('mcp.title')}</h2>
        <p>{$t('mcp.desc')}</p>
      </div>
      <div class="header-actions">
        <button class="btn-secondary" onclick={reload} disabled={loading}>
          <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
          {$t('mcp.reload')}
        </button>
        <button class="btn-secondary" onclick={openImport}>
          <iconify-icon icon="ant-design:code-outlined" width="14"></iconify-icon>
          {$t('mcp.import_json')}
        </button>
        <button class="btn-primary" onclick={openAdd}>
          <iconify-icon icon="ant-design:plus-outlined" width="14"></iconify-icon>
          {$t('mcp.add')}
        </button>
        <button class="btn-primary" onclick={aiSetup}>
          <iconify-icon icon="ant-design:thunderbolt-outlined" width="14"></iconify-icon>
          {$t('mcp.ai_setup')}
        </button>
      </div>
    </div>

    <!-- Tool Search card -->
    <div class="tool-search-card">
      <div class="ts-info">
        <span class="ts-title">{$t('mcp.tool_search')}</span>
        <span class="ts-desc">{$t('mcp.tool_search_desc')}</span>
      </div>
      <Segment
        options={['Auto', 'On', 'Off']}
        labels={{ Auto: $t('mcp.ts_auto'), On: $t('mcp.ts_on'), Off: $t('mcp.ts_off') }}
        value={capitalize($toolSearchMode ?? 'auto')}
        onchange={onToolSearchChange}
      />
    </div>

    <!-- Server list -->
    {#if loading && ($mcpServers as any[]).length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:loading-outlined" width="24" class="spin"></iconify-icon>
        <span>{$t('mcp.loading')}</span>
      </div>
    {:else if ($mcpServers as any[]).length === 0}
      <div class="empty-state">
        <iconify-icon icon="ant-design:api-outlined" width="32"></iconify-icon>
        <span>{$t('mcp.empty')}</span>
        <button class="btn-primary" onclick={openAdd}>{$t('mcp.add_first')}</button>
      </div>
    {:else}
      <div class="server-list">
        {#each ($mcpServers as any[]) as srv (srv.name)}
          {@const tag = statusTag(srv.status)}
          {@const enabled = !srv.disabled}
          <div class="server-card" class:disabled-card={!enabled}>
            <span class="server-icon">
              <iconify-icon icon={serverIcon(srv.name)} width="17"></iconify-icon>
            </span>
            <div class="server-info">
              <div class="server-title-row">
                <span class="server-name">{srv.name}</span>
                {#if srv.transport}
                  <span class="transport-badge mono">{srv.transport}</span>
                {/if}
                {#if srv.source === 'project'}
                  <span class="transport-badge mono">project</span>
                {/if}
                <StatusTag status={tag.tagStatus}>{tag.tagLabel}</StatusTag>
                {#if srv.status === 'connected'}
                  <span class="tool-count">{srv.tools} tool{srv.tools !== 1 ? 's' : ''}</span>
                {/if}
              </div>
              <span class="server-cmd mono">{srv.command || srv.url || ''}</span>
              {#if srv.error}
                <span class="server-error">{srv.error}</span>
              {/if}
            </div>
            <div class="server-actions">
              <button
                class="srv-btn"
                title={$t('common.edit')}
                disabled={srv.source === 'project'}
                onclick={() => openEdit(srv)}
              >
                <iconify-icon icon="ant-design:edit-outlined" width="14"></iconify-icon>
              </button>
              <button
                class="srv-btn"
                title={$t('status.reconnect')}
                disabled={srv.status === 'connected'}
                onclick={() => reconnect(srv.name)}
              >
                <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
              </button>
              <button
                class="srv-btn del"
                title={$t('common.delete')}
                disabled={srv.source === 'project'}
                onclick={() => deleteServer(srv.name)}
              >
                <iconify-icon icon="ant-design:delete-outlined" width="14"></iconify-icon>
              </button>
              <span style="width:8px"></span>
              <Switch
                checked={enabled}
                onchange={() => toggleServer(srv.name, enabled)}
              />
            </div>
          </div>
        {/each}
      </div>
    {/if}

  </div>
</div>

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

/* ── buttons ─────────────────────────────────────────────────────────────── */
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

/* ── tool-search card ────────────────────────────────────────────────────── */
.tool-search-card {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
  padding: 20px 24px; display: flex; align-items: center; gap: 24px;
}
.ts-info  { display: flex; flex-direction: column; gap: 3px; flex: 1; min-width: 0; }
.ts-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.ts-desc  { font-size: 13px; line-height: 1.5; color: var(--text-secondary); }

/* ── server list ─────────────────────────────────────────────────────────── */
.server-list { display: flex; flex-direction: column; gap: 16px; }

.server-card {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
  padding: 18px 24px; display: flex; align-items: center; gap: 16px;
  transition: opacity 0.2s;
}
.server-card.disabled-card { opacity: 0.6; }

.server-icon {
  width: 36px; height: 36px; flex: 0 0 36px; border-radius: 10px;
  background: var(--blue-1); color: var(--blue-6); display: flex; align-items: center; justify-content: center;
}

.server-info       { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 5px; }
.server-title-row  { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.server-name       { font-size: 15px; font-weight: 600; color: var(--text-heading); }

.transport-badge {
  height: 20px; padding: 0 7px; border: 1px solid var(--border-secondary); background: var(--bg-table-header);
  border-radius: 4px; display: flex; align-items: center; font-size: 11px; color: var(--text-tertiary);
}

.tool-count  { font-size: 12px; color: var(--text-tertiary); }

.server-cmd {
  font-size: 13px; color: var(--text-tertiary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

.server-error {
  font-size: 12px; color: var(--error);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}

/* ── server actions ──────────────────────────────────────────────────────── */
.server-actions { display: flex; align-items: center; gap: 4px; flex: 0 0 auto; }

.srv-btn {
  width: 30px; height: 30px; border: 1px solid var(--border-secondary); background: var(--bg-container);
  border-radius: 6px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-tertiary);
}
.srv-btn:hover:not(:disabled)      { border-color: var(--blue-5); color: var(--blue-5); }
.srv-btn.del:hover:not(:disabled)  { border-color: var(--error); color: var(--error); }
.srv-btn:disabled { opacity: 0.35; cursor: not-allowed; }

/* ── empty state ─────────────────────────────────────────────────────────── */
.empty-state {
  display: flex; flex-direction: column; align-items: center; gap: 14px;
  padding: 64px 0; color: var(--text-tertiary); font-size: 14px;
}

/* ── utilities ───────────────────────────────────────────────────────────── */
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

@keyframes spin { to { transform: rotate(360deg); } }
.spin { animation: spin 1s linear infinite; display: inline-block; }
</style>

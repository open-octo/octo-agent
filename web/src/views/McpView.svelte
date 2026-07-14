<script lang="ts">
  import { onMount, onDestroy } from 'svelte'
  import { mcpServers, toolSearchMode, mcpModalOpen, showToast, openAgentSession } from '../lib/stores'
  import { t, tr } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'
  import { openUrl } from '../lib/externalLinks'
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
      showToast(e.message ?? tr('mcp.toast_reload_fail'), 'error')
    } finally {
      loading = false
    }
  }

  onMount(reload)

  // Full reload from disk: picks up hand-edited config files and retries
  // every failed connection, unlike reload() which just re-reads cached state.
  async function fullReload() {
    loading = true
    try {
      const data = await api.reloadMcpServers()
      mcpServers.set(data.servers as any)
      toolSearchMode.set(data.tool_search.enabled)
    } catch (e: any) {
      showToast(e.message ?? tr('mcp.toast_reload_fail'), 'error')
    } finally {
      loading = false
    }
  }

  // ─── add / edit / import / AI setup ───────────────────────────────────────────

  // Adding and editing a server — whether it lives in ~/.octo/mcp.json or a
  // project's .octo/mcp.json — both go through the mcp-creator skill instead
  // of a structured form. That works identically regardless of which file
  // the entry is defined in, so there's no need for a separate "Add Server"
  // form, and it keeps edit behavior in one place (the skill) instead of a
  // second hand-written prompt that could drift from it.
  function askAgentToEdit(name: string) {
    openAgentSession(`/mcp-creator Edit the existing MCP server "${name}" — find its entry, show me the current config, then ask what I want changed.`, `Edit MCP: ${name}`)
  }

  function openImport() {
    mcpModalOpen.set(true)
  }

  // Agentic-first: open a fresh chat that invokes the mcp-creator skill, which
  // walks the user through picking + configuring a server in conversation.
  function aiSetup() {
    openAgentSession('/mcp-creator', 'MCP Setup')
  }

  // ─── delete server ──────────────────────────────────────────────────────────

  async function deleteServer(name: string) {
    if (!(await confirmDialog(tr('mcp.confirm_delete').replace('{name}', name)))) return
    try {
      await api.deleteMcpServer(name)
      mcpServers.update(list => (list as any[]).filter((s: any) => s.name !== name))
      showToast(tr('mcp.toast_removed'))
    } catch (e: any) {
      showToast(tr('mcp.toast_delete_fail'), 'error')
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
      showToast(tr('mcp.toast_toggle_fail'), 'error')
      // The Switch already flipped optimistically on click; a rejected
      // toggle (e.g. a project-scoped server) needs a real reload to snap
      // its visual state back to what the server actually has.
      reload()
    }
  }

  // ─── OAuth Authorization Code + PKCE flow ────────────────────────────────
  // The serve process can't block the request that starts the flow on the
  // user completing a browser redirect, so authorizing is split: start kicks
  // it off, then we poll status until it settles. See
  // internal/server/mcp_oauth_handlers.go.

  let oauth = $state<{ name: string; state: string; link: string; error: string } | null>(null)
  let oauthTimer: ReturnType<typeof setInterval> | null = null
  // #1109: the poll had no upper bound — an authorization that never
  // completes (user never opens the link, or abandons it) polled forever.
  // 2 minutes is a generous window for a browser redirect; past that we stop
  // and show a timeout instead of leaving the modal spinning silently.
  const OAUTH_TIMEOUT_MS = 120_000
  let oauthDeadline: ReturnType<typeof setTimeout> | null = null

  function oauthSettled(state: string): boolean {
    return state === 'connected' || state === 'failed'
  }

  function applyOAuth(name: string, d: api.McpOAuthState) {
    const isNewLink = !!d.authorize_url && d.authorize_url !== oauth?.link
    oauth = {
      name,
      state: d.state,
      link: d.authorize_url ?? '',
      error: d.error ?? '',
    }
    if (isNewLink && d.authorize_url) {
      // Best-effort: this fires from inside a poll tick, not synchronously
      // from the user's click, so popup blockers may silently swallow it —
      // the link rendered below is the reliable fallback. openUrl routes
      // through the native bridge in the desktop shell (window.open is dead
      // in the webview).
      openUrl(d.authorize_url)
    }
  }

  function stopPolling() {
    if (oauthTimer) { clearInterval(oauthTimer); oauthTimer = null }
    if (oauthDeadline) { clearTimeout(oauthDeadline); oauthDeadline = null }
  }

  async function authorize(name: string) {
    try {
      const d = await api.startMcpOAuth(name)
      applyOAuth(name, d)
      if (oauthSettled(d.state)) { onOAuthSettled(d.state); return }
      stopPolling()
      oauthTimer = setInterval(async () => {
        try {
          const s = await api.mcpOAuthStatus(name)
          // clearInterval() only cancels *future* ticks — a tick already
          // in flight when the user closes this modal or opens a different
          // server's flow still resolves and lands here. Without this guard
          // it reassigns `oauth` unconditionally: a closed modal pops back
          // open with stale data, or server B's flow gets clobbered by a
          // late response for server A.
          if (oauth?.name !== name) return
          applyOAuth(name, s)
          if (oauthSettled(s.state)) { stopPolling(); onOAuthSettled(s.state) }
        } catch { /* transient — keep polling until the modal closes */ }
      }, 1500)
      oauthDeadline = setTimeout(() => {
        stopPolling()
        if (oauth?.name === name && !oauthSettled(oauth.state)) {
          oauth = { ...oauth, state: 'failed', error: 'Authorization timed out — the link may have expired. Try again.' }
        }
      }, OAUTH_TIMEOUT_MS)
    } catch (e: any) {
      showToast(e.message ?? 'Authorization failed', 'error')
    }
  }

  function onOAuthSettled(state: string) {
    reload() // refresh statuses (connected / error)
    if (state === 'connected') setTimeout(() => { oauth = null }, 1200)
  }

  // #1109: closing only stopped watching — the authorization flow continues
  // server-side and a token cached right after close never refreshed the
  // list, so a server the user just authorized could still show
  // "disconnected" until the next full reload. One extra reload on close
  // covers the flow finishing right around that moment; it doesn't chase a
  // flow that completes long after the modal is gone (matching the issue's
  // proposed fix — server state is still correct, just not reflected here).
  function closeOAuth() {
    stopPolling()
    oauth = null
    reload()
  }

  onDestroy(stopPolling)

  // ─── reconnect ──────────────────────────────────────────────────────────────

  async function reconnect(name: string) {
    try {
      await api.reconnectMcpServer(name)
      showToast(tr('mcp.toast_reconnecting').replace('{name}', name))
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
        <button class="btn-secondary" onclick={fullReload} disabled={loading}>
          <iconify-icon icon="ant-design:reload-outlined" width="14"></iconify-icon>
          {$t('mcp.reload')}
        </button>
        <button class="btn-secondary" onclick={openImport}>
          <iconify-icon icon="ant-design:code-outlined" width="14"></iconify-icon>
          {$t('mcp.import_json')}
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
        <button class="btn-primary" onclick={aiSetup}>{$t('mcp.add_first')}</button>
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
                  <span class="transport-badge mono">{$t('mcp.badge_project')}</span>
                {/if}
                <StatusTag status={tag.tagStatus}>{tag.tagLabel}</StatusTag>
                {#if srv.status === 'connected'}
                  <span class="tool-count">{$t('mcp.tool_count').replace('{n}', String(srv.tools))}</span>
                {/if}
              </div>
              <span class="server-cmd mono">{srv.command || srv.url || ''}</span>
              {#if srv.error}
                <span class="server-error">{srv.error}</span>
              {/if}
            </div>
            <div class="server-actions">
              {#if srv.auth === 'oauth' && !srv.disabled && !srv.invalid && srv.status !== 'connected'}
                <button
                  class="srv-btn"
                  title={$t('mcp.btn.authorize')}
                  onclick={() => authorize(srv.name)}
                >
                  <iconify-icon icon="ant-design:key-outlined" width="14"></iconify-icon>
                </button>
              {/if}
              <button
                class="srv-btn"
                title={$t('mcp.btn.edit_with_agent')}
                onclick={() => askAgentToEdit(srv.name)}
              >
                <iconify-icon icon="ant-design:message-outlined" width="14"></iconify-icon>
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

{#if oauth}
<div class="oauth-backdrop" role="presentation" onclick={closeOAuth}>
  <div class="oauth-modal" role="dialog" aria-modal="true" onclick={(e) => e.stopPropagation()}>
    <div class="oauth-header">
      <iconify-icon icon="ant-design:key-outlined" width="16" style="color:var(--blue-6)"></iconify-icon>
      <span class="oauth-title">{$t('mcp.oauth.title')}</span>
      <span class="oauth-name mono">{oauth.name}</span>
      <button class="oauth-close" onclick={closeOAuth} aria-label="close">
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>
    <div class="oauth-body">
      {#if oauth.state === 'starting'}
        <div class="oauth-wait"><div class="oauth-spinner"></div><span>{$t('mcp.oauth.starting')}</span></div>
      {:else if oauth.state === 'authorizing'}
        <p class="oauth-instruction">{$t('mcp.oauth.instruction')}</p>
        {#if oauth.link}
          <a class="oauth-link" href={oauth.link} target="_blank" rel="noopener noreferrer">{$t('mcp.oauth.openLink')}</a>
        {/if}
        <div class="oauth-wait"><div class="oauth-spinner"></div><span>{$t('mcp.oauth.waiting')}</span></div>
      {:else if oauth.state === 'connected'}
        <p class="oauth-success"><span class="oauth-ok">✓</span> {$t('mcp.oauth.success')}</p>
      {:else}
        <p class="oauth-failed">{$t('mcp.oauth.failed')}</p>
        {#if oauth.error}<p class="server-error">{oauth.error}</p>{/if}
      {/if}
    </div>
  </div>
</div>
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

/* ── OAuth modal ─────────────────────────────────────────────────────────── */
.oauth-backdrop {
  position: fixed; inset: 0; z-index: 1100; background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.oauth-modal {
  width: 100%; max-width: 440px; background: var(--bg-container);
  border: 1px solid var(--border); border-radius: 12px; overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18); animation: octo-fadein 0.16s ease;
}
.oauth-header {
  display: flex; align-items: center; gap: 8px; padding: 12px 18px;
  border-bottom: 1px solid var(--border);
}
.oauth-title { font-size: 14px; font-weight: 600; color: var(--text-heading); }
.oauth-name { font-size: 13px; color: var(--text-secondary); flex: 1; }
.oauth-close {
  border: none; background: none; cursor: pointer; color: var(--text-tertiary);
  display: flex; align-items: center; padding: 2px;
}
.oauth-close:hover { color: var(--text-secondary); }
.oauth-body {
  padding: 20px 18px; display: flex; flex-direction: column; align-items: center; gap: 12px;
}
.oauth-instruction { margin: 0; font-size: 13px; color: var(--text-secondary); text-align: center; }
.oauth-link { font-size: 13px; color: var(--blue-6); text-decoration: none; }
.oauth-link:hover { text-decoration: underline; }
.oauth-wait { display: flex; align-items: center; gap: 8px; font-size: 12px; color: var(--text-tertiary); }
.oauth-spinner {
  width: 16px; height: 16px; border: 2px solid var(--border);
  border-top-color: var(--blue-6); border-radius: 50%; animation: octo-spin 0.7s linear infinite;
}
.oauth-success { margin: 0; font-size: 14px; color: var(--text-heading); }
.oauth-ok { color: var(--success); }
.oauth-failed { margin: 0; font-size: 14px; color: var(--error); }
</style>

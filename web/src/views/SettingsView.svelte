<script lang="ts">
  import { onMount } from 'svelte'
  import Segment from '../components/ui/Segment.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import QrCode from '../components/ui/QrCode.svelte'
  import { get } from 'svelte/store'
  import { showToast, nativeShell, openAgentSession } from '../lib/stores'
  import { setLocale, t, tr } from '../lib/i18n'
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import { notificationsEnabled, setNotificationsEnabled } from '../lib/notifications'
  import * as api from '../lib/api'
  import type { EndpointConfig } from '../lib/api'

  // --- local state ---
  let language      = $state('en')
  let fontSize      = $state('Medium')
  let theme         = $state('Light')
  let autostart     = $state(false) // desktop shell only
  let versionStr    = $state('')
  let latestStr     = $state('')
  let updateAvail   = $state(false)
  let downloadUrl   = $state('')
  let checkingUpdate = $state(false)
  let loading       = $state(true)

  // Managed-tunnel pairing material (null until fetched; .enabled false when
  // the server was not started with --tunnel).
  let tunnelPairing = $state<api.TunnelPairing | null>(null)

  // ── Agent defaults (display-only, configured through conversation) ──────────
  let reasoningEffort  = $state('off')
  let permissionMode   = $state('interactive')
  let showReasoningVal = $state(true)
  let coauthorVal      = $state(true)
  let workspaceDir     = $state('')

  // ── Endpoints (display-only, configured through conversation) ───────────────
  let endpoints  = $state<EndpointConfig[]>([])
  let defaultCid = $state('')
  let liteCid    = $state('')

  // ── helpers ─────────────────────────────────────────────────────────────────
  function effortLabel(effort: string): string {
    const labels: Record<string, string> = {
      off: 'Off', low: 'Low', medium: 'Medium', high: 'High', xhigh: 'Xhigh', max: 'Max',
    }
    return labels[effort.toLowerCase()] ?? 'Off'
  }

  function permissionLabel(mode: string): string {
    const labels: Record<string, string> = {
      interactive: 'Ask', auto: 'Auto', strict: 'Strict',
    }
    return labels[mode.toLowerCase()] ?? 'Ask'
  }

  const langOptions = [
    { value: 'en', label: 'English' },
    { value: 'zh', label: '简体中文' },
  ]

  const fontZoomMap: Record<string, string> = { Small: '0.9', Medium: '1', Large: '1.1' }
  const modeToThemeLabel: Record<string, string> = { light: 'Light', dark: 'Dark', system: 'System' }

  onMount(async () => {
    const savedFont = localStorage.getItem('octo.fontSize')
    if (savedFont) fontSize = savedFont
    theme = modeToThemeLabel[getMode()] ?? 'Light'
    await Promise.all([loadConfig(), loadVersion()])
    if (get(nativeShell)) api.getAutostart().then(v => (autostart = v)).catch(() => {})
    api.getTunnelPairing().then(p => { tunnelPairing = p }).catch(() => {})
  })

  async function loadConfig() {
    loading = true
    try {
      const cfg = await api.getConfig() as any
      // Absent = old server dropping "" through omitempty = reasoning off.
      reasoningEffort  = cfg.reasoning_effort ?? 'off'
      permissionMode   = cfg.permission_mode ?? 'interactive'
      showReasoningVal = cfg.show_reasoning ?? true
      coauthorVal      = cfg.coauthor ?? true
      workspaceDir     = cfg.workspace_dir ?? ''
      if (cfg.language) language = cfg.language
      setLocale(cfg.language === 'zh' || cfg.language === 'zh-TW' ? 'zh' : 'en')

      try {
        const ep = await api.getEndpoints()
        endpoints = ep.endpoints ?? []
        defaultCid = ep.default ?? ''
        liteCid = ep.lite ?? ''
      } catch {
        endpoints = []
        defaultCid = ''
        liteCid = ''
      }
    } catch (e: any) {
      showToast(`Failed to load config: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function loadVersion() {
    try {
      const v = await api.getVersion() as any
      versionStr = v.current ?? v.version ?? ''
      latestStr = v.latest ?? ''
      updateAvail = !!v.needs_update
      downloadUrl = v.download_url ?? ''
    } catch { /* non-critical */ }
  }

  async function checkUpdate() {
    if (checkingUpdate) return
    checkingUpdate = true
    try {
      await loadVersion()
      if (updateAvail && downloadUrl) {
        try { await api.openExternal(downloadUrl) }
        catch { window.open(downloadUrl, '_blank', 'noopener') }
      } else {
        showToast($t('settings.update.uptodate'), 'success')
      }
    } finally {
      checkingUpdate = false
    }
  }

  async function copyPairURL() {
    if (!tunnelPairing?.pair_url) return
    try {
      await navigator.clipboard.writeText(tunnelPairing.pair_url)
      showToast($t('settings.mobile.copied'), 'success')
    } catch {
      showToast('Copy failed', 'error')
    }
  }

  async function toggleAutostart(v: boolean) {
    try {
      await api.setAutostart(v)
      autostart = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to change autostart', 'error')
      autostart = !v
    }
  }

  // ── agentic-first actions ───────────────────────────────────────────────────
  function configureDefaults() {
    openAgentSession('/config-setup', 'Configure defaults')
  }

  function configureEndpoints() {
    openAgentSession('/config-setup add endpoint', 'Configure endpoints')
  }

  function editEndpoint(id: string) {
    openAgentSession(`/config-setup edit endpoint ${id}`, `Edit endpoint: ${id}`)
  }

  $effect(() => {
    ;(document.documentElement.style as any).zoom = fontZoomMap[fontSize] ?? '1'
    localStorage.setItem('octo.fontSize', fontSize)
  })

  const themeLabelToMode: Record<string, ThemeMode> = { Light: 'light', Dark: 'dark', System: 'system' }
  $effect(() => {
    setMode(themeLabelToMode[theme] ?? 'light')
  })

  $effect(() => {
    setLocale(language === 'zh' || language === 'zh-TW' ? 'zh' : 'en')
  })
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <h2>{$t('settings.title')}</h2>
      <p>{$t('settings.subtitle')}</p>
    </div>

    {#if loading}
      <div class="loading-state">{$t('settings.loading')}</div>
    {:else}
      <!-- Endpoints (read-only, configured through conversation) -->
      <div class="section-card">
        <div class="section-head">
          <span class="section-title-inline">{$t('settings.endpoints.title')}</span>
          <button class="btn-primary-sm" onclick={configureEndpoints}>
            <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
            {$t('settings.endpoints.configure')}
          </button>
        </div>
        {#if endpoints.length === 0}
          <div class="models-empty">{$t('settings.endpoints.empty')}</div>
        {:else}
          {#each endpoints as ep (ep.id)}
            <div class="endpoint-card">
              <div class="endpoint-head">
                <div class="endpoint-title-line">
                  <span class="endpoint-id mono">{ep.id}</span>
                  {#if ep.name}<span class="endpoint-name">{ep.name}</span>{/if}
                  {#if defaultCid.startsWith(`${ep.id}::`)}
                    <StatusTag status="success">{$t('settings.endpoints.badge.default')}</StatusTag>
                  {/if}
                  {#if liteCid.startsWith(`${ep.id}::`)}
                    <StatusTag status="info">{$t('settings.endpoints.badge.lite')}</StatusTag>
                  {/if}
                </div>
                <div class="endpoint-meta mono">
                  <span>{ep.provider}</span>
                  {#if ep.base_url}<span> · {ep.base_url}</span>{/if}
                  {#if ep.protocol}<span> · {ep.protocol}</span>{/if}
                </div>
                <div class="endpoint-key">
                  {#if ep.has_api_key}
                    <span class="key-set">{$t('settings.endpoints.api_key')}: {$t('settings.endpoints.api_key.set')}</span>
                  {:else}
                    <span class="key-missing">{$t('settings.endpoints.api_key')}: {$t('settings.endpoints.api_key.missing')}</span>
                  {/if}
                </div>
                <div class="endpoint-actions">
                  <button class="btn-edit-agent" onclick={() => editEndpoint(ep.id)}>
                    <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
                    {$t('settings.endpoints.configure_with_agent')}
                  </button>
                </div>
              </div>
              {#if ep.models && ep.models.length > 0}
                <div class="endpoint-models">
                  <div class="endpoint-models-head">{$t('settings.endpoints.models')} ({ep.models.length})</div>
                  {#each ep.models as m (m.model)}
                    {@const isDefault = defaultCid === `${ep.id}::${m.model}`}
                    {@const isLite = liteCid === `${ep.id}::${m.model}`}
                    <div class="endpoint-model-row">
                      <span class="mono">{m.model}</span>
                      {#if m.vision}<span class="vision-tag">{$t('settings.endpoints.models.vision')}</span>{/if}
                      {#if isDefault}
                        <StatusTag status="success">{$t('settings.endpoints.badge.default')}</StatusTag>
                      {/if}
                      {#if isLite}
                        <StatusTag status="info">{$t('settings.endpoints.badge.lite')}</StatusTag>
                      {/if}
                    </div>
                  {/each}
                </div>
              {/if}
            </div>
          {/each}
        {/if}
      </div>

      <!-- General (UI preferences — not agent configuration, keep as-is) -->
      <div class="section-card">
        <div class="section-title">{$t('settings.general')}</div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.language')}</span>
            <span class="setting-desc">{$t('settings.language_desc')}</span>
          </div>
          <select class="sel" bind:value={language}>
            {#each langOptions as o}
              <option value={o.value}>{o.label}</option>
            {/each}
          </select>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.font_size')}</span>
            <span class="setting-desc">{$t('settings.font_size_desc')}</span>
          </div>
          <Segment options={['Small', 'Medium', 'Large']} labels={{ Small: $t('settings.fs_small'), Medium: $t('settings.fs_medium'), Large: $t('settings.fs_large') }} bind:value={fontSize} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.theme')}</span>
            <span class="setting-desc">{$t('settings.theme_desc')}</span>
          </div>
          <Segment options={['Light', 'Dark', 'System']} labels={{ Light: $t('settings.theme_light'), Dark: $t('settings.theme_dark'), System: $t('settings.theme_system') }} bind:value={theme} />
        </div>
        {#if $nativeShell}
          <div class="setting-row">
            <div class="setting-info">
              <span class="setting-label">{$t('settings.autostart')}</span>
              <span class="setting-desc">{$t('settings.autostart_desc')}</span>
            </div>
            <Switch checked={autostart} onchange={(v) => toggleAutostart(v)} />
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <span class="setting-label">{$t('settings.update')}</span>
              <span class="setting-desc">
                {#if updateAvail}
                  {$t('settings.update_available')} v{latestStr}
                {:else}
                  {$t('settings.update_desc')}
                {/if}
              </span>
            </div>
            <button class="btn-secondary" onclick={checkUpdate} disabled={checkingUpdate}>
              {checkingUpdate ? $t('settings.update.checking') : updateAvail ? $t('upgrade.btn.download') : $t('settings.update.check')}
            </button>
          </div>
        {/if}
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.notifications')}</span>
            <span class="setting-desc">{$t('settings.notifications_desc')}</span>
          </div>
          <Switch checked={$notificationsEnabled} onchange={(v) => setNotificationsEnabled(v)} />
        </div>
      </div>

      <!-- Mobile (managed tunnel pairing) -->
      <div class="section-card">
        <div class="section-title">{$t('settings.mobile')}</div>
        {#if tunnelPairing?.enabled && tunnelPairing.pair_url}
          <div class="mobile-pair">
            <QrCode text={tunnelPairing.pair_url} />
            <div class="mobile-info">
              <p class="mobile-scan">{$t('settings.mobile.scan')}</p>
              <div class="mobile-meta mono">
                <div>{$t('settings.mobile.relay')}: {tunnelPairing.relay}</div>
                <div>{$t('settings.mobile.tunnel_id')}: {tunnelPairing.tunnel_id}</div>
              </div>
              <button class="btn-secondary" onclick={copyPairURL}>{$t('settings.mobile.copy_url')}</button>
            </div>
          </div>
        {:else}
          <div class="mobile-disabled">{$t('settings.mobile.disabled')}</div>
        {/if}
      </div>

      <!-- Agent defaults (read-only, configured through conversation) -->
      <div class="section-card">
        <div class="section-title">{$t('settings.agent')}</div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.reasoning')}</span>
            <span class="setting-desc">{$t('settings.reasoning_desc')}</span>
          </div>
          <span class="setting-readonly">{effortLabel(reasoningEffort)}</span>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.perm_mode')}</span>
            <span class="setting-desc">{$t('settings.perm_mode_desc')}</span>
          </div>
          <span class="setting-readonly">{permissionLabel(permissionMode)}</span>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.show_reasoning')}</span>
            <span class="setting-desc">{$t('settings.show_reasoning_desc')}</span>
          </div>
          <span class="setting-readonly">{showReasoningVal ? $t('settings.on') : $t('settings.off')}</span>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.coauthor')}</span>
            <span class="setting-desc">{$t('settings.coauthor_desc')}</span>
          </div>
          <span class="setting-readonly">{coauthorVal ? $t('settings.on') : $t('settings.off')}</span>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.workspace_dir')}</span>
            <span class="setting-desc">{$t('settings.workspace_dir_desc')}</span>
          </div>
          <span class="setting-readonly mono">{workspaceDir || 'auto'}</span>
        </div>
        <div class="setting-row last">
          <div class="setting-info"></div>
          <button class="btn-edit-agent" onclick={configureDefaults}>
            <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
            {$t('settings.configure_with_agent')}
          </button>
        </div>
      </div>

      <!-- Version badge -->
      <div class="version-row">
        {#if versionStr}
          <span class="version-badge">{$t('common.version')} {versionStr}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 800px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }
.page-header { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.loading-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }
.section-card { background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow); overflow: hidden; }
.section-title { padding: 16px 24px; border-bottom: 1px solid var(--border-table); font-size: 16px; font-weight: 600; color: var(--text-heading); }
.setting-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 24px; padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.setting-row.last { border-bottom: none; }
.setting-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.setting-label { font-size: 14px; color: var(--text); }
.setting-desc { font-size: 12px; color: var(--text-tertiary); }
.setting-readonly {
  font-size: 13px; color: var(--text-secondary); flex: 0 0 auto;
  background: var(--bg-table-header); padding: 4px 12px; border-radius: 6px;
}
.sel {
  width: 220px; flex: 0 0 auto; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 6px; font-size: 13px;
  color: var(--text); font-family: inherit; background: var(--bg-container); cursor: pointer; outline: none;
}
.sel:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.version-row { display: flex; align-items: center; justify-content: flex-end; }
.version-badge { font-size: 12px; color: var(--text-tertiary); }

/* ── buttons ───────────────────────────────────────────────────────────────── */
.btn-primary-sm {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 6px; font-size: 13px; color: #fff; cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
}
.btn-primary-sm:hover { background: var(--blue-5); }
.btn-secondary {
  height: 32px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-edit-agent {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 6px;
}
.btn-edit-agent:hover { border-color: var(--blue-5); color: var(--blue-5); }

/* ── endpoints (read-only display) ──────────────────────────────────────────── */
.section-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.section-title-inline { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.endpoint-card {
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.endpoint-card:last-child { border-bottom: none; }
.endpoint-head { display: flex; flex-direction: column; gap: 4px; margin-bottom: 8px; }
.endpoint-title-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.endpoint-id { font-size: 14px; font-weight: 600; color: var(--text); }
.endpoint-name { font-size: 13px; color: var(--text-secondary); }
.endpoint-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-wrap: wrap; }
.endpoint-key { font-size: 12px; }
.endpoint-key .key-set { color: var(--green-6, #16a34a); }
.endpoint-key .key-missing { color: var(--red-6, #dc2626); }
.endpoint-actions { display: flex; align-items: center; gap: 6px; margin-top: 4px; }
.endpoint-models { margin-top: 6px; padding-left: 12px; border-left: 2px solid var(--border-table); }
.endpoint-models-head {
  font-size: 12px; color: var(--text-tertiary);
  text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 4px;
}
.endpoint-model-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  padding: 4px 0; font-size: 13px; color: var(--text);
}
.vision-tag {
  font-size: 11px; padding: 1px 6px; border-radius: 4px;
  background: var(--bg-subtle, rgba(0,0,0,0.04)); color: var(--text-tertiary);
}
.models-empty { padding: 28px 24px; text-align: center; font-size: 13px; color: var(--text-tertiary); }

/* ── mobile ─────────────────────────────────────────────────────────────────── */
.mobile-pair { display: flex; gap: 20px; padding: 20px 24px; align-items: flex-start; flex-wrap: wrap; }
.mobile-info { display: flex; flex-direction: column; gap: 12px; min-width: 0; flex: 1; }
.mobile-scan { margin: 0; font-size: 14px; color: var(--text); }
.mobile-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-direction: column; gap: 4px; word-break: break-all; }
.mobile-meta > div { min-width: 0; }
.mobile-info .btn-secondary { align-self: flex-start; }
.mobile-disabled { padding: 28px 24px; text-align: center; font-size: 13px; color: var(--text-tertiary); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

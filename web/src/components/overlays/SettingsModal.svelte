<script lang="ts">
  import Segment from '../ui/Segment.svelte'
  import Switch from '../ui/Switch.svelte'
  import StatusTag from '../ui/StatusTag.svelte'
  import QrCode from '../ui/QrCode.svelte'
  import { get } from 'svelte/store'
  import { showToast, nativeShell, openAgentSession, settingsModalOpen, onboardPhase } from '../../lib/stores'
  import { setLocale, t } from '../../lib/i18n'
  import { getMode, setMode, type ThemeMode } from '../../lib/theme'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import { openUrl } from '../../lib/externalLinks'
  import * as api from '../../lib/api'
  import type { EndpointConfig } from '../../lib/api'

  const LICENSE_URL = 'https://github.com/open-octo/octo-agent/blob/main/LICENSE.txt'

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

  let cat = $state<'general' | 'endpoints' | 'agent' | 'mobile' | 'about'>('general')
  let modalEl = $state<HTMLDivElement | null>(null)

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

  const categories: { key: typeof cat, icon: string, label: string }[] = [
    { key: 'general',   icon: 'ant-design:sliders-outlined',       label: 'settings.general' },
    { key: 'endpoints', icon: 'ant-design:api-outlined',           label: 'settings.endpoints.title' },
    { key: 'agent',     icon: 'ant-design:robot-outlined',         label: 'settings.agent' },
    { key: 'mobile',    icon: 'ant-design:mobile-outlined',        label: 'settings.mobile' },
    { key: 'about',     icon: 'ant-design:info-circle-outlined',   label: 'settings.about' },
  ]

  // Re-seed on every open, same as the other global modals — reflects
  // whatever config was saved elsewhere (agent chat, another window) since
  // the modal last closed.
  $effect(() => {
    if ($settingsModalOpen) {
      cat = 'general'
      loadConfig()
      loadVersion()
      if (get(nativeShell)) api.getAutostart().then(v => (autostart = v)).catch(() => {})
      api.getTunnelPairing().then(p => { tunnelPairing = p }).catch(() => {})
      theme = modeToThemeLabel[getMode()] ?? 'Light'
      modalEl?.focus()
    }
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
    settingsModalOpen.set(false)
    openAgentSession('/config-setup', 'Configure defaults')
  }

  function configureEndpoints() {
    settingsModalOpen.set(false)
    openAgentSession('/config-setup add endpoint', 'Configure endpoints')
  }

  function editEndpoint(id: string) {
    settingsModalOpen.set(false)
    openAgentSession(`/config-setup edit endpoint ${id}`, `Edit endpoint: ${id}`)
  }

  function close() {
    settingsModalOpen.set(false)
  }

  // Re-runs the same blocking first-run panel a fresh install shows (App.svelte
  // gates <FirstRunSetup /> on this store) — lets a configured user redo the
  // model/browser setup + personalization chat without wiping any config first.
  function rerunFirstRun() {
    settingsModalOpen.set(false)
    onboardPhase.set('key_setup')
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

{#if $settingsModalOpen}
<div class="backdrop" role="presentation" onclick={close}>
  <div
    class="modal"
    bind:this={modalEl}
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    onclick={(e) => e.stopPropagation()}
    onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); close() } }}
  >
    <div class="modal-header">
      <span class="modal-title">{$t('settings.title')}</span>
      <button class="modal-close" onclick={close} aria-label={$t('common.close')}>
        <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
      </button>
    </div>

    <div class="modal-body">
      <div class="rail">
        {#each categories as c (c.key)}
          <div class="scat" class:on={cat === c.key} onclick={() => (cat = c.key)}>
            <iconify-icon icon={c.icon} width="15"></iconify-icon>
            <span>{$t(c.label)}</span>
          </div>
        {/each}
      </div>

      <div class="pane">
        {#if loading}
          <div class="loading-state">{$t('settings.loading')}</div>
        {:else if cat === 'general'}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.language')}</span>
              <span class="setd">{$t('settings.language_desc')}</span>
            </div>
            <select class="sinput" bind:value={language}>
              {#each langOptions as o}
                <option value={o.value}>{o.label}</option>
              {/each}
            </select>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.font_size')}</span>
              <span class="setd">{$t('settings.font_size_desc')}</span>
            </div>
            <Segment options={['Small', 'Medium', 'Large']} labels={{ Small: $t('settings.fs_small'), Medium: $t('settings.fs_medium'), Large: $t('settings.fs_large') }} bind:value={fontSize} />
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.theme')}</span>
              <span class="setd">{$t('settings.theme_desc')}</span>
            </div>
            <Segment options={['Light', 'Dark', 'System']} labels={{ Light: $t('settings.theme_light'), Dark: $t('settings.theme_dark'), System: $t('settings.theme_system') }} bind:value={theme} />
          </div>
          {#if $nativeShell}
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.autostart')}</span>
                <span class="setd">{$t('settings.autostart_desc')}</span>
              </div>
              <Switch checked={autostart} onchange={(v) => toggleAutostart(v)} />
            </div>
          {/if}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.notifications')}</span>
              <span class="setd">{$t('settings.notifications_desc')}</span>
            </div>
            <Switch checked={$notificationsEnabled} onchange={(v) => setNotificationsEnabled(v)} />
          </div>

        {:else if cat === 'endpoints'}
          <div class="pane-head">
            <button class="btnp" onclick={configureEndpoints}>
              <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
              {$t('settings.endpoints.configure')}
            </button>
          </div>
          {#if endpoints.length === 0}
            <div class="models-empty">{$t('settings.endpoints.empty')}</div>
          {:else}
            <div class="card2">
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
                      <button class="btns" onclick={() => editEndpoint(ep.id)}>
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
            </div>
          {/if}

        {:else if cat === 'agent'}
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.reasoning')}</span>
              <span class="setd">{$t('settings.reasoning_desc')}</span>
            </div>
            <span class="setro">{effortLabel(reasoningEffort)}</span>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.perm_mode')}</span>
              <span class="setd">{$t('settings.perm_mode_desc')}</span>
            </div>
            <span class="setro">{permissionLabel(permissionMode)}</span>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.show_reasoning')}</span>
              <span class="setd">{$t('settings.show_reasoning_desc')}</span>
            </div>
            <span class="setro">{showReasoningVal ? $t('settings.on') : $t('settings.off')}</span>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.coauthor')}</span>
              <span class="setd">{$t('settings.coauthor_desc')}</span>
            </div>
            <span class="setro">{coauthorVal ? $t('settings.on') : $t('settings.off')}</span>
          </div>
          <div class="setrow">
            <div class="seti">
              <span class="setl">{$t('settings.workspace_dir')}</span>
              <span class="setd">{$t('settings.workspace_dir_desc')}</span>
            </div>
            <span class="setro mono">{workspaceDir || 'auto'}</span>
          </div>
          <div class="pane-foot">
            <button class="btns" onclick={configureDefaults}>
              <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
              {$t('settings.configure_with_agent')}
            </button>
          </div>

        {:else if cat === 'mobile'}
          {#if tunnelPairing?.enabled && tunnelPairing.pair_url}
            <div class="mobile-pair">
              <QrCode text={tunnelPairing.pair_url} />
              <div class="mobile-info">
                <p class="mobile-scan">{$t('settings.mobile.scan')}</p>
                <div class="mobile-meta mono">
                  <div>{$t('settings.mobile.relay')}: {tunnelPairing.relay}</div>
                  <div>{$t('settings.mobile.tunnel_id')}: {tunnelPairing.tunnel_id}</div>
                </div>
                <button class="btns" onclick={copyPairURL}>{$t('settings.mobile.copy_url')}</button>
              </div>
            </div>
          {:else}
            <div class="mobile-disabled">{$t('settings.mobile.disabled')}</div>
          {/if}

        {:else if cat === 'about'}
          <div class="card2">
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('common.version')}</span>
                <span class="setd">{$t('settings.about.version_desc')}</span>
              </div>
              <span class="setver mono">v{versionStr}</span>
            </div>
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.update')}</span>
                <span class="setd">{$t('settings.update_desc')}</span>
              </div>
              {#if $nativeShell}
                <button class="btns" onclick={checkUpdate} disabled={checkingUpdate}>
                  {checkingUpdate ? $t('settings.update.checking') : updateAvail ? $t('upgrade.btn.download') : $t('settings.update.check')}
                </button>
              {/if}
            </div>
            {#if $nativeShell && updateAvail}
              <div class="setrow">
                <div class="seti">
                  <span class="setl">{$t('settings.update_available')} v{latestStr}</span>
                </div>
              </div>
            {/if}
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.about.firstrun')}</span>
                <span class="setd">{$t('settings.about.firstrun_desc')}</span>
              </div>
              <button class="btns" onclick={rerunFirstRun}>{$t('settings.about.firstrun_btn')}</button>
            </div>
            <div class="setrow">
              <div class="seti">
                <span class="setl">{$t('settings.about.license')}</span>
                <span class="setd">{$t('settings.about.license_desc')}</span>
              </div>
              <button class="link-btn" onclick={() => openUrl(LICENSE_URL)}>{$t('settings.about.license_view')}</button>
            </div>
          </div>
          <div class="about-footer">
            {$t('settings.about.footer').replace('{tagline}', $t('nav.workbench')).replace('{year}', String(new Date().getFullYear()))}
          </div>
        {/if}
      </div>
    </div>
  </div>
</div>
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1000; background: var(--scrim);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.modal {
  width: 100%; max-width: 760px; max-height: 78vh;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card);
  box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.modal-header {
  display: flex; align-items: center; justify-content: space-between; flex: 0 0 auto;
  padding: 16px 20px; border-bottom: 1px solid var(--border);
}
.modal-title { font-size: 16px; font-weight: 700; letter-spacing: -0.01em; color: var(--text-heading); }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 7px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-secondary);
}
.modal-close:hover { background: var(--hover-neutral); color: var(--text); }
.modal-body { flex: 1; min-height: 0; display: flex; }

/* ── category rail ─────────────────────────────────────────────────────────── */
.rail {
  width: 190px; flex: 0 0 190px; border-right: 1px solid var(--border);
  background: var(--bg-layout); padding: 10px; display: flex; flex-direction: column; gap: 2px;
  overflow-y: auto;
}
.scat {
  display: flex; align-items: center; gap: 9px; padding: 7px 10px;
  border-radius: 7px; cursor: pointer; color: var(--text-tertiary);
}
.scat span { font-size: 13px; color: var(--text-secondary); }
.scat:hover { background: var(--hover-neutral); }
.scat.on { background: var(--active-blue-bg); }
.scat.on span { color: var(--blue-6); font-weight: 600; }
.scat.on { color: var(--blue-6); }

/* ── content pane ──────────────────────────────────────────────────────────── */
.pane { flex: 1; min-width: 0; overflow-y: auto; padding: 20px 24px; display: flex; flex-direction: column; gap: 0; }
.loading-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }
.pane-head { display: flex; justify-content: flex-end; padding-bottom: 14px; }
.pane-foot { display: flex; justify-content: flex-start; padding-top: 14px; }

.setrow {
  display: flex; align-items: center; justify-content: space-between;
  gap: 20px; padding: 14px 2px; border-bottom: 1px solid var(--border-secondary);
}
.setrow:last-child { border-bottom: none; }
/* Rows nested in a card (About) need the card's own inset — the flat rows
   above sit directly in .pane, which already provides that inset itself. */
.card2 .setrow { padding: 16px 20px; }
.seti { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.setl { font-size: 13px; color: var(--text); }
.setd { font-size: 12px; color: var(--text-secondary); margin-top: 2px; line-height: 1.45; max-width: 42ch; }
.setro {
  font-size: 13px; color: var(--text-secondary); flex: 0 0 auto;
  background: var(--bg-table-header); padding: 4px 12px; border-radius: 6px;
}
.setver { font-size: 13px; color: var(--text-tertiary); flex: 0 0 auto; }
.link-btn {
  border: none; background: transparent; color: var(--blue-6); font-size: 13px;
  font-weight: 500; cursor: pointer; font-family: inherit; padding: 0; flex: 0 0 auto;
}
.link-btn:hover { text-decoration: underline; }
.about-footer { padding: 28px 2px 4px; text-align: center; font-size: 12px; color: var(--text-tertiary); }
.sinput {
  width: 220px; flex: 0 0 auto; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 8px; font-size: 13px;
  color: var(--text); font-family: inherit; background: var(--bg-container); cursor: pointer; outline: none;
}
.sinput:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }

/* ── buttons ───────────────────────────────────────────────────────────────── */
.btnp {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 8px; font-size: 13px; font-weight: 600; color: #fff; cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 8px;
  box-shadow: 0 1px 2px rgba(0,122,255,0.35);
}
.btnp:hover { background: var(--blue-5); }
.btns {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; font-size: 13px; font-weight: 500; color: var(--text); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 6px;
}
.btns:hover:not(:disabled) { background: var(--hover-neutral); border-color: var(--text-quaternary); }
.btns:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── endpoints (read-only display) ──────────────────────────────────────────── */
.card2 { background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); overflow: hidden; }
.endpoint-card { padding: 14px 16px; border-bottom: 1px solid var(--border-secondary); }
.endpoint-card:last-child { border-bottom: none; }
.endpoint-head { display: flex; flex-direction: column; gap: 4px; margin-bottom: 8px; }
.endpoint-title-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.endpoint-id { font-size: 14px; font-weight: 600; color: var(--text); }
.endpoint-name { font-size: 13px; color: var(--text-secondary); }
.endpoint-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-wrap: wrap; }
.endpoint-key { font-size: 12px; }
.endpoint-key .key-set { color: var(--success-text); }
.endpoint-key .key-missing { color: var(--error); }
.endpoint-actions { display: flex; align-items: center; gap: 6px; margin-top: 4px; }
.endpoint-models { margin-top: 6px; padding-left: 12px; border-left: 2px solid var(--border-secondary); }
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
  background: var(--hover-neutral); color: var(--text-tertiary);
}
.models-empty { padding: 28px 16px; text-align: center; font-size: 13px; color: var(--text-tertiary); }

/* ── mobile ─────────────────────────────────────────────────────────────────── */
.mobile-pair { display: flex; gap: 20px; padding: 6px 2px; align-items: flex-start; flex-wrap: wrap; }
.mobile-info { display: flex; flex-direction: column; gap: 12px; min-width: 0; flex: 1; }
.mobile-scan { margin: 0; font-size: 14px; color: var(--text); }
.mobile-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-direction: column; gap: 4px; word-break: break-all; }
.mobile-meta > div { min-width: 0; }
.mobile-info .btns { align-self: flex-start; }
.mobile-disabled { padding: 28px 16px; text-align: center; font-size: 13px; color: var(--text-tertiary); }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

<script lang="ts">
  import { onMount } from 'svelte'
  import { get } from 'svelte/store'
  import { channels, showToast, openAgentSession, nativeShell } from '../lib/stores'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'

  // platform-to-icon mapping for well-known channels
  const platformIcons: Record<string, string> = {
    telegram: 'logos:telegram',
    discord:  'logos:discord-icon',
    feishu:   'mdi:feather',
    dingtalk: 'ant-design:dingtalk-outlined',
    wecom:    'ant-design:wechat-work-filled',
    weixin:   'simple-icons:wechat',
  }

  interface ChannelRow {
    platform: string
    enabled: boolean
    running: boolean
    has_config: boolean
    fields: Record<string, string>
    // #1121: why the adapter isn't healthy — a startup skip reason or the
    // latest crash/restart status. Absent when healthy.
    issue?: string
  }

  let rows = $state<ChannelRow[]>([])
  let loading = $state(true)
  let busyPlatform = $state<string | null>(null)

  // Desktop hub only: whether this machine runs channels at all. The desktop
  // app can be a hub for the UI/sessions without bridging IM, so channels are
  // opt-in per machine; when off, configuring a channel below does nothing
  // until this is turned on. isNative gates the whole control out of plain web.
  const isNative = get(nativeShell)
  let hubChannelsOn = $state(false)
  let hubBusy = $state(false)

  onMount(async () => {
    if (isNative) {
      try { hubChannelsOn = await api.getChannelsEnabled() } catch { /* leave off */ }
    }
    await reload()
  })

  async function toggleHubChannels(on: boolean) {
    if (hubBusy) return
    hubBusy = true
    const prev = hubChannelsOn
    hubChannelsOn = on // optimistic
    try {
      hubChannelsOn = await api.setChannelsEnabled(on)
      await reload()
    } catch (e: any) {
      hubChannelsOn = prev
      showToast(tr('channels.hub_toggle_failed') + `: ${e.message}`, 'error')
    } finally {
      hubBusy = false
    }
  }

  async function reload() {
    loading = true
    try {
      // /api/channels returns only CONFIGURED channels; show every supported
      // platform as a card by merging the available-platform list with config.
      const [avail, data] = await Promise.all([
        api.listAvailableChannels().catch(() => []),
        api.listChannels() as any,
      ])
      const configured: ChannelRow[] = Array.isArray(data) ? data : (data.channels ?? [])
      const byPlatform: Record<string, ChannelRow> = {}
      for (const c of configured) byPlatform[c.platform] = c
      if (avail.length > 0) {
        rows = avail.map((a: any) =>
          byPlatform[a.platform] ?? { platform: a.platform, enabled: false, running: false, has_config: false, fields: {} }
        )
      } else {
        rows = configured
      }
    } catch (e: any) {
      showToast(`Failed to load channels: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function handleToggle(platform: string, enabled: boolean) {
    const row = rows.find(r => r.platform === platform)
    if (!row) return
    // Optimistic update
    rows = rows.map(r => r.platform === platform ? { ...r, enabled } : r)
    try {
      await api.saveChannel(platform, { enabled, fields: row.fields })
    } catch (e: any) {
      // Revert
      rows = rows.map(r => r.platform === platform ? { ...r, enabled: !enabled } : r)
      showToast(`Failed to save channel: ${e.message}`, 'error')
    }
  }

  async function handleTest(platform: string) {
    busyPlatform = platform
    try {
      await api.testChannel(platform)
      showToast(`${platform} connection test passed`, 'success')
    } catch (e: any) {
      showToast(`Test failed: ${e.message}`, 'error')
    } finally {
      busyPlatform = null
    }
  }

  // The six platforms are fixed — "unconfigure" clears the saved credentials
  // and returns the card to the "Not configured" state rather than removing it.
  async function handleUnconfigure(platform: string) {
    if (!(await confirmDialog(tr('channels.confirm_unconfigure').replace('{platform}', labelFor(platform))))) return
    busyPlatform = platform
    try {
      await api.deleteChannel(platform)
      rows = rows.map(r => r.platform === platform
        ? { ...r, enabled: false, running: false, has_config: false, fields: {} }
        : r)
      showToast(`${labelFor(platform)} configuration cleared`, 'success')
    } catch (e: any) {
      showToast(`Failed to clear configuration: ${e.message}`, 'error')
    } finally {
      busyPlatform = null
    }
  }

  // Agentic-first: open a fresh chat that invokes the channel-manager skill,
  // which guides the user through the platform console + credentials.
  function openSetup(platform: string) {
    openAgentSession(`/channel-manager setup ${platform}`, `Channel Setup — ${labelFor(platform)}`)
  }

  function iconFor(platform: string) {
    return platformIcons[platform.toLowerCase()] ?? 'ant-design:message-outlined'
  }

  function tagFor(row: ChannelRow): { status: string; label: string } {
    if (!row.has_config) return { status: 'default', label: tr('channels.not_configured') }
    if (!row.enabled)    return { status: 'default', label: tr('status.disabled') }
    // #1121: a config/startup problem or an ongoing crash-restart loop — show
    // it distinctly from a plain "Stopped" (which reads as "not started yet",
    // not "something is wrong").
    if (row.issue)       return { status: 'error', label: tr('status.error') }
    if (row.running)     return { status: 'success', label: tr('status.running') }
    return { status: 'warning', label: tr('status.stopped') }
  }

  function activityFor(row: ChannelRow): string {
    if (row.issue) return row.issue
    if (row.running) return tr('channels.activity_running')
    if (row.enabled) return tr('channels.activity_enabled')
    return tr('status.disabled')
  }

  function labelFor(platform: string): string {
    const labels: Record<string, string> = {
      telegram: 'Telegram',
      discord:  'Discord',
      feishu:   'Feishu (飞书)',
      dingtalk: 'DingTalk (钉钉)',
      wecom:    'WeCom (企业微信)',
      weixin:   'WeChat (微信)',
    }
    return labels[platform.toLowerCase()] ?? platform
  }

  function handleFor(row: ChannelRow): string {
    const f = row.fields ?? {}
    if (f.bot_id) return f.bot_id
    if (f.app_id) return f.app_id
    if (f.client_id) return f.client_id
    if (f.bot_token) return f.bot_token.slice(0, 8) + '…'
    return ''  // unconfigured — no handle to show
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <div class="title-block">
        <h2>{$t('channels.title')}</h2>
        <p>{$t('channels.subtitle')}</p>
      </div>
    </div>

    {#if isNative}
      <div class="hub-toggle" class:off={!hubChannelsOn}>
        <div class="hub-toggle-text">
          <strong>{$t('channels.hub_run_here')}</strong>
          <span>{hubChannelsOn ? $t('channels.hub_on_hint') : $t('channels.hub_off_hint')}</span>
        </div>
        <Switch checked={hubChannelsOn} onchange={(v: boolean) => toggleHubChannels(v)} />
      </div>
    {/if}

    {#if loading}
      <div class="empty-state">{$t('channels.loading')}</div>
    {:else}
      <div class="grid">
        {#each rows as row (row.platform)}
          {@const tag = tagFor(row)}
          <div class="channel-card">
            <div class="channel-top">
              <span class="ch-icon">
                <iconify-icon icon={iconFor(row.platform)} width="15"></iconify-icon>
              </span>
              <div class="ch-meta">
                <span class="ch-name">{labelFor(row.platform)}</span>
                <span class="ch-handle mono">{handleFor(row)}</span>
              </div>
              <StatusTag status={tag.status as any}>{tag.label}</StatusTag>
              <Switch
                checked={row.enabled}
                onchange={(v: boolean) => handleToggle(row.platform, v)}
              />
            </div>
            <span class="ch-activity">{activityFor(row)}</span>
            <div class="ch-actions">
              {#if row.has_config}
                <button
                  class="btn-outline"
                  disabled={busyPlatform === row.platform}
                  onclick={() => handleTest(row.platform)}
                >
                  <iconify-icon icon="ant-design:check-circle-outlined" width="13"></iconify-icon>
                  {busyPlatform === row.platform ? $t('channels.testing') : $t('channels.diagnostics')}
                </button>
                <button
                  class="btn-outline del"
                  disabled={busyPlatform === row.platform}
                  onclick={() => handleUnconfigure(row.platform)}
                >
                  {$t('channels.unconfigure')}
                </button>
              {/if}
              <button class="btn-primary-sm" onclick={() => openSetup(row.platform)}>
                <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
                {row.has_config ? $t('channels.reconfigure') : $t('channels.setup')}
              </button>
            </div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 1080px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }
.page-header { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; }
.title-block { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.hub-toggle {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  background: var(--bg-container); border-radius: 12px; box-shadow: var(--card-shadow);
  padding: 14px 18px; border: 1px solid transparent;
}
.hub-toggle.off { border-color: var(--warning-border, var(--blue-6)); background: var(--bg-elevated, var(--bg-container)); }
.hub-toggle-text { display: flex; flex-direction: column; gap: 3px; min-width: 0; }
.hub-toggle-text strong { font-size: 14px; font-weight: 600; color: var(--text-heading); }
.hub-toggle-text span { font-size: 13px; color: var(--text-secondary); }
.grid { display: grid; grid-template-columns: repeat(2, minmax(0,1fr)); gap: 16px; }
.channel-card {
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
  padding: 24px; display: flex; flex-direction: column; gap: 12px;
}
.channel-top { display: flex; align-items: center; gap: 10px; }
.ch-icon {
  width: 32px; height: 32px; flex: 0 0 32px; border-radius: 9999px;
  background: var(--blue-1); color: var(--blue-6); display: flex; align-items: center; justify-content: center;
}
.ch-meta { display: flex; flex-direction: column; gap: 1px; flex: 1; min-width: 0; }
.ch-name { font-size: 14px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ch-handle { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.ch-activity { font-size: 12px; color: var(--text-tertiary); }
.ch-actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; padding-top: 4px; }
.btn-outline {
  height: 28px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container); border-radius: 6px;
  display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-outline:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-outline:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-outline.del:hover:not(:disabled) { border-color: var(--error); color: var(--error); }
.btn-primary-sm { height: 28px; padding: 0 12px; border: none; background: var(--blue-6); border-radius: 6px; display: flex; align-items: center; gap: 8px; font-size: 13px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary-sm:hover { background: var(--blue-5); }
.empty-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

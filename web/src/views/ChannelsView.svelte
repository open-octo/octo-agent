<script lang="ts">
  import { onMount } from 'svelte'
  import { channels, showToast, view, sessions, activeSessionId } from '../lib/stores'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import * as api from '../lib/api'
  import { t } from '../lib/i18n'

  // platform-to-icon mapping for well-known channels
  const platformIcons: Record<string, string> = {
    telegram: 'logos:telegram',
    discord:  'logos:discord-icon',
    feishu:   'simple-icons:lark',
    dingtalk: 'simple-icons:dingtalk',
    wecom:    'simple-icons:wechat',
    weixin:   'simple-icons:wechat',
  }

  interface ChannelRow {
    platform: string
    enabled: boolean
    running: boolean
    has_config: boolean
    fields: Record<string, string>
  }

  let rows = $state<ChannelRow[]>([])
  let loading = $state(true)
  let busyPlatform = $state<string | null>(null)

  onMount(async () => {
    await reload()
  })

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

  async function handleDelete(platform: string) {
    busyPlatform = platform
    try {
      await api.deleteChannel(platform)
      rows = rows.filter(r => r.platform !== platform)
      showToast(`${platform} removed`, 'success')
    } catch (e: any) {
      showToast(`Delete failed: ${e.message}`, 'error')
    } finally {
      busyPlatform = null
    }
  }

  async function openNewSession() {
    try {
      const sess = await api.createSession({ name: 'Channel Setup' })
      sessions.update(s => [sess, ...s])
      activeSessionId.set(sess.id)
      view.set('chat')
    } catch (e: any) {
      showToast(`Could not open session: ${e.message}`, 'error')
    }
  }

  function iconFor(platform: string) {
    return platformIcons[platform.toLowerCase()] ?? 'ant-design:message-outlined'
  }

  function tagFor(row: ChannelRow): { status: string; label: string } {
    if (!row.has_config) return { status: 'default', label: 'Not configured' }
    if (!row.enabled)    return { status: 'default', label: 'Disabled' }
    if (row.running)     return { status: 'success', label: 'Running' }
    return { status: 'warning', label: 'Stopped' }
  }

  function activityFor(row: ChannelRow): string {
    if (row.running) return 'Adapter is running and accepting messages'
    if (row.enabled) return 'Enabled but not yet started — restart the server to activate'
    return 'Disabled'
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
      <button class="btn-primary" onclick={openNewSession}>{$t('channels.connect')}</button>
    </div>

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
                onclick={() => handleDelete(row.platform)}
              >
                <iconify-icon icon="ant-design:delete-outlined" width="13"></iconify-icon>
              </button>
              <button class="btn-primary-sm" onclick={openNewSession}>
                <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
                {$t('channels.setup')}
              </button>
            </div>
          </div>
        {/each}

        <!-- Add tile -->
        <div class="add-tile" onclick={openNewSession} role="button" tabindex="0">
          <iconify-icon icon="ant-design:plus-outlined" width="18"></iconify-icon>
          <span>{$t('channels.add_tile')}</span>
        </div>
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
.btn-primary { height: 32px; padding: 0 14px; border: none; background: var(--blue-6); border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary:hover { background: var(--blue-5); }
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
.add-tile {
  border: 1px dashed var(--border); border-radius: 16px; min-height: 148px;
  display: flex; flex-direction: column; align-items: center; justify-content: center;
  gap: 8px; cursor: pointer; color: var(--text-tertiary);
  font-size: 13px;
}
.add-tile:hover { border-color: var(--blue-6); color: var(--blue-6); background: var(--active-blue-bg); }
.empty-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

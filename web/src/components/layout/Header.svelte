<script lang="ts">
  import { view, cmdkOpen, sidebar } from '../../lib/stores'
  import { t } from '../../lib/i18n'
  import { ws, wsState } from '../../lib/ws'
  import { notificationsEnabled, setNotificationsEnabled } from '../../lib/notifications'
  import OctoLogo from './OctoLogo.svelte'

  function cycleSidebar() {
    sidebar.update(s => s === 'full' ? 'rail' : s === 'rail' ? 'hidden' : 'full')
  }

  // The bell toggles desktop notifications on/off — the same preference the
  // "Desktop Notifications" switch in Settings drives. There is no feed.
  function toggleNotifications() {
    setNotificationsEnabled(!$notificationsEnabled)
  }
</script>

<header>
  <div class="left">
    <button class="icon-btn" title={$t('header.toggle_sidebar')} onclick={cycleSidebar}>
      <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
    </button>
    <div class="brand">
      <OctoLogo class="logo" size={26} />
      <span class="name">Octo</span>
      <span class="divider"></span>
      <span class="sub">{$t('nav.workbench')}</span>
    </div>
  </div>

  <div class="right">
    <!-- Visible on every view, not just ChatView, whose own inline banner only
         renders while a chat session is open — Settings/MCP/Skills/Tasks/etc.
         otherwise had no indication a dropped socket was silently failing
         their actions. -->
    {#if $wsState !== 'connected'}
      <button class="icon-btn" title={$t('chat.connection_lost')} onclick={() => ws.connect()}>
        <iconify-icon icon="ant-design:loading-outlined" width="16" style="color:var(--warning);animation:octo-spin 0.8s linear infinite"></iconify-icon>
      </button>
    {/if}
    <button class="search-pill" onclick={() => cmdkOpen.set(true)}>
      <iconify-icon icon="ant-design:search-outlined" width="14"></iconify-icon>
      <span>{$t('header.search_sessions')}</span>
      <kbd>⌘K</kbd>
    </button>
    <button class="icon-btn" class:active={$notificationsEnabled} title={$t('header.notifications')} aria-pressed={$notificationsEnabled} onclick={toggleNotifications}>
      <iconify-icon icon={$notificationsEnabled ? 'ant-design:bell-filled' : 'ant-design:bell-outlined'} width="16"></iconify-icon>
    </button>
    <button class="icon-btn" title={$t('nav.settings')} onclick={() => view.set('settings')}>
      <iconify-icon icon="ant-design:setting-outlined" width="16"></iconify-icon>
    </button>
  </div>
</header>

<style>
header {
  height: 56px;
  flex: 0 0 56px;
  background: var(--bg-container);
  border-bottom: 1px solid var(--border-secondary);
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 0 16px;
  z-index: 100;
}
.left, .right { display: flex; align-items: center; gap: 8px; }
.brand { display: flex; align-items: center; gap: 10px; padding-left: 4px; }
.brand :global(.logo) {
  color: var(--blue-6);
  flex: 0 0 auto;
}
.name { font-size: 15px; font-weight: 600; color: var(--text-heading); }
.divider { width: 1px; height: 16px; background: var(--border-secondary); }
.sub { font-size: 12px; color: var(--text-tertiary); }
.icon-btn {
  width: 32px; height: 32px; border: none; background: transparent;
  border-radius: 9999px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: var(--text-secondary);
}
.icon-btn:hover { background: var(--hover-neutral); }
.icon-btn.active { color: var(--blue-6); }
.search-pill {
  display: flex; align-items: center; gap: 8px;
  height: 32px; padding: 0 6px 0 12px; width: 240px;
  background: var(--search-bg); border-radius: 9999px; cursor: pointer;
  color: var(--text-tertiary); border: none; font-family: inherit; font-size: 13px;
}
.search-pill:hover { background: var(--search-hover); }
.search-pill span { flex: 1; text-align: left; }
kbd {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: var(--bg-container); border: 1px solid var(--border-secondary); border-radius: 4px;
  padding: 1px 5px; color: var(--text-tertiary);
}
</style>

<script lang="ts">
  import { view, cmdkOpen, sidebar, showToast } from '../../lib/stores'
  import { t, tr } from '../../lib/i18n'

  function cycleSidebar() {
    sidebar.update(s => s === 'full' ? 'rail' : s === 'rail' ? 'hidden' : 'full')
  }

  // The bell toggles desktop notifications (browser permission) — the backing
  // for the "Desktop Notifications" setting. There is no notification feed.
  async function toggleNotifications() {
    if (!('Notification' in window)) {
      showToast(tr('header.notif_unsupported'), 'error')
      return
    }
    if (Notification.permission === 'granted') {
      showToast(tr('header.notif_enabled'))
      return
    }
    if (Notification.permission === 'denied') {
      showToast(tr('header.notif_blocked'), 'error')
      return
    }
    const perm = await Notification.requestPermission()
    showToast(
      perm === 'granted' ? tr('header.notif_enabled') : tr('header.notif_not_enabled'),
      perm === 'granted' ? 'success' : 'error',
    )
  }
</script>

<header>
  <div class="left">
    <button class="icon-btn" title={$t('header.toggle_sidebar')} onclick={cycleSidebar}>
      <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
    </button>
    <div class="brand">
      <div class="logo">O</div>
      <span class="name">Octo</span>
      <span class="divider"></span>
      <span class="sub">{$t('nav.workbench')}</span>
    </div>
  </div>

  <div class="right">
    <button class="search-pill" onclick={() => cmdkOpen.set(true)}>
      <iconify-icon icon="ant-design:search-outlined" width="14"></iconify-icon>
      <span>{$t('header.search_sessions')}</span>
      <kbd>⌘K</kbd>
    </button>
    <button class="icon-btn" title={$t('header.notifications')} onclick={toggleNotifications}>
      <iconify-icon icon="ant-design:bell-outlined" width="16"></iconify-icon>
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
.logo {
  width: 26px; height: 26px; border-radius: 8px;
  background: var(--blue-6); color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-size: 15px; font-weight: 600;
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

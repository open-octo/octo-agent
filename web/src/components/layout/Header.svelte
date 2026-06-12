<script lang="ts">
  import { view, cmdkOpen, sidebar } from '../../lib/stores'
  import { wsState, ws } from '../../lib/ws'

  function cycleSidebar() {
    sidebar.update(s => s === 'full' ? 'rail' : s === 'rail' ? 'hidden' : 'full')
  }
</script>

<header>
  <div class="left">
    <button class="icon-btn" title="Toggle sidebar" onclick={cycleSidebar}>
      <iconify-icon icon="lucide:panel-left" width="16"></iconify-icon>
    </button>
    <div class="brand">
      <div class="logo">O</div>
      <span class="name">Octo</span>
      <span class="divider"></span>
      <span class="sub">Agent Workbench</span>
    </div>
  </div>

  <div class="right">
    <button class="search-pill" onclick={() => cmdkOpen.set(true)}>
      <iconify-icon icon="ant-design:search-outlined" width="14"></iconify-icon>
      <span>Search sessions…</span>
      <kbd>⌘K</kbd>
    </button>
    <button class="icon-btn" title="Notifications">
      <iconify-icon icon="ant-design:bell-outlined" width="16"></iconify-icon>
    </button>
    <button class="icon-btn" title="Settings" onclick={() => view.set('settings')}>
      <iconify-icon icon="ant-design:setting-outlined" width="16"></iconify-icon>
    </button>
    <div class="avatar">R</div>
  </div>
</header>

<style>
header {
  height: 56px;
  flex: 0 0 56px;
  background: #FFFFFF;
  border-bottom: 1px solid #EEEFF1;
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
  background: #1677FF; color: #fff;
  display: flex; align-items: center; justify-content: center;
  font-size: 15px; font-weight: 600;
}
.name { font-size: 15px; font-weight: 600; color: #1F1F1F; }
.divider { width: 1px; height: 16px; background: #EEEFF1; }
.sub { font-size: 12px; color: rgba(0,0,0,0.45); }
.icon-btn {
  width: 32px; height: 32px; border: none; background: transparent;
  border-radius: 9999px; display: flex; align-items: center; justify-content: center;
  cursor: pointer; color: rgba(0,0,0,0.65);
}
.icon-btn:hover { background: rgba(0,0,0,0.04); }
.search-pill {
  display: flex; align-items: center; gap: 8px;
  height: 32px; padding: 0 6px 0 12px; width: 240px;
  background: #E9EEF6; border-radius: 9999px; cursor: pointer;
  color: rgba(0,0,0,0.45); border: none; font-family: inherit; font-size: 13px;
}
.search-pill:hover { background: #DBE2EE; }
.search-pill span { flex: 1; text-align: left; }
kbd {
  font-size: 11px; font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  background: #FFFFFF; border: 1px solid #EEEFF1; border-radius: 4px;
  padding: 1px 5px; color: rgba(0,0,0,0.45);
}
.avatar {
  width: 28px; height: 28px; border-radius: 9999px;
  background: #E6F4FF; color: #1677FF;
  display: flex; align-items: center; justify-content: center;
  font-size: 13px; font-weight: 600; cursor: pointer;
}
</style>

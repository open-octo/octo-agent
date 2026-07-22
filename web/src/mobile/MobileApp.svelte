<script lang="ts">
  // octo-mobile root view tree. Rendered instead of the desktop App layout when
  // `mobileShell` is true (see App.svelte). Batch 0 is the shell only: bottom
  // tab bar + neutral tokens. The data-bound feed, typed detail views, and the
  // other tabs are filled in by later batches (see
  // dev-docs/mobile-ui-implementation.md).
  import './theme.css'
  import Feed from './Feed.svelte'
  import { setActiveSession } from '../lib/stores'

  type Tab = 'chat' | 'tasks' | 'config' | 'settings'
  let tab = $state<Tab>('chat')
  // The session opened into a detail view (null = show the feed).
  let openId = $state<string | null>(null)

  function openSession(id: string) {
    setActiveSession(id)
    openId = id
  }
</script>

<div class="m-root">
  <main class="m-view">
    {#if tab === 'chat'}
      {#if openId}
        <header class="m-dhead">
          <button class="m-back" onclick={() => (openId = null)} aria-label="返回">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
          </button>
          <span class="m-dtitle">对话详情</span>
        </header>
        <div class="m-scroll">
          <div class="m-ph">ChatDetail · 批 1b 接入 Composer + 消息流</div>
        </div>
      {:else}
        <Feed onOpen={openSession} />
      {/if}
    {:else if tab === 'tasks'}
      <header class="m-head"><h1>任务</h1></header>
      <div class="m-scroll"><div class="m-ph">定时与自动化 · 批 3 接入</div></div>
    {:else if tab === 'config'}
      <header class="m-head"><h1>配置</h1></header>
      <div class="m-scroll"><div class="m-ph">技能 / MCP / 工作流 / 数据 · 批 3 接入</div></div>
    {:else}
      <header class="m-head"><h1>设置</h1></header>
      <div class="m-scroll"><div class="m-ph">设备 / 通知 / 外观 · 批 3 接入</div></div>
    {/if}
  </main>

  <nav class="m-tabbar">
    <button class="m-tab" class:on={tab === 'chat'} onclick={() => { tab = 'chat'; openId = null }}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 11.5a8.5 8.5 0 0 1-12.3 7.6L3 21l1.9-5.7A8.5 8.5 0 1 1 21 11.5z"/></svg>
      <span>会话</span>
    </button>
    <button class="m-tab" class:on={tab === 'tasks'} onclick={() => (tab = 'tasks')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M9 6h11M9 12h11M9 18h11M4 6h.01M4 12h.01M4 18h.01"/></svg>
      <span>任务</span>
    </button>
    <button class="m-tab" class:on={tab === 'config'} onclick={() => (tab = 'config')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6h10M18 6h2M4 12h2M10 12h10M4 18h6M14 18h6"/><circle cx="16" cy="6" r="2"/><circle cx="8" cy="12" r="2"/><circle cx="12" cy="18" r="2"/></svg>
      <span>配置</span>
    </button>
    <button class="m-tab" class:on={tab === 'settings'} onclick={() => (tab = 'settings')}>
      <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-2.7 1.1V21a2 2 0 1 1-4 0v-.1A1.6 1.6 0 0 0 7 19.4a1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0-1.1-2.7H3a2 2 0 1 1 0-4h.1A1.6 1.6 0 0 0 4.6 7"/></svg>
      <span>设置</span>
    </button>
  </nav>
</div>

<style>
  .m-root {
    position: fixed;
    inset: 0;
    display: flex;
    flex-direction: column;
    background: var(--m-bg);
    color: var(--m-text);
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", sans-serif;
    -webkit-font-smoothing: antialiased;
  }
  .m-view {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
    padding-top: env(safe-area-inset-top);
  }
  .m-head {
    flex: none;
    padding: 8px 18px 12px;
  }
  .m-head h1 {
    margin: 0;
    font-size: 24px;
    font-weight: 600;
    color: var(--m-text-strong);
  }
  .m-dhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 14px 12px;
  }
  .m-back {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    border: none;
    background: var(--m-surface);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
    box-shadow: var(--m-shadow-card);
  }
  .m-dtitle {
    font-size: 15px;
    font-weight: 600;
    color: var(--m-text);
  }
  .m-scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 0 16px 20px;
  }
  .m-ph {
    background: var(--m-surface);
    border: 1px dashed var(--m-border);
    border-radius: 14px;
    padding: 18px 16px;
    font-size: 13px;
    color: var(--m-text-3);
    box-shadow: var(--m-shadow-card);
  }
  .m-tabbar {
    flex: none;
    display: flex;
    padding: 8px 4px calc(8px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border-top: 1px solid var(--m-border-2);
  }
  .m-tab {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 4px;
    padding: 4px 0;
    font-size: 10px;
    color: var(--m-text-3);
    background: none;
    border: none;
    font-family: inherit;
    cursor: pointer;
  }
  .m-tab.on {
    color: var(--m-accent);
  }
</style>

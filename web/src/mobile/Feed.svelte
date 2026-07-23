<script lang="ts">
  import { get } from 'svelte/store'
  import { feedGroups, type FeedKind } from './feedGroups'
  import { createNewSession, activeSessionId } from '../lib/stores'
  import SessionCard from './SessionCard.svelte'
  import DeviceBanner from './DeviceBanner.svelte'

  let { onOpen }: { onOpen: (id: string, kind: FeedKind) => void } = $props()

  const groups = feedGroups
  const empty = $derived($groups.todo.length + $groups.active.length + $groups.recent.length === 0)

  async function newSession() {
    await createNewSession()
    const id = get(activeSessionId)
    if (id) onOpen(id, 'done')
  }
</script>

<header class="head">
  <h1>会话</h1>
  <button class="search" aria-label="搜索">
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-2)" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
  </button>
</header>

<div class="scroll">
  <DeviceBanner />

  {#if empty}
    <div class="empty">还没有会话 · 点右下角 + 新建</div>
  {/if}

  {#if $groups.todo.length}
    <p class="lbl">待办</p>
    {#each $groups.todo as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}

  {#if $groups.active.length}
    <p class="lbl">进行中</p>
    {#each $groups.active as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}

  {#if $groups.recent.length}
    <p class="lbl">最近完成</p>
    {#each $groups.recent as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}
</div>

<button class="fab" onclick={newSession} aria-label="新建会话">
  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.4"><path d="M12 5v14M5 12h14"/></svg>
</button>

<style>
  .head {
    flex: none;
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 8px 18px 12px;
  }
  .head h1 { margin: 0; font-size: 24px; font-weight: 600; color: var(--m-text-strong); }
  .search {
    width: 34px;
    height: 34px;
    border-radius: 50%;
    border: none;
    background: var(--m-surface-2);
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 0 16px 20px;
  }
  .lbl {
    margin: 14px 2px 8px;
    font: 600 12px/1 system-ui;
    letter-spacing: .5px;
    text-transform: uppercase;
    color: var(--m-text-3);
  }
  .empty {
    padding: 40px 16px;
    text-align: center;
    font-size: 13px;
    color: var(--m-text-3);
  }
  .fab {
    position: absolute;
    right: 18px;
    bottom: calc(88px + env(safe-area-inset-bottom));
    width: 56px;
    height: 56px;
    border-radius: 50%;
    background: var(--m-accent);
    border: none;
    display: flex;
    align-items: center;
    justify-content: center;
    box-shadow: var(--m-shadow-fab);
    cursor: pointer;
    z-index: 30;
  }
</style>

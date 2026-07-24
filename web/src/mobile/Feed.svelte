<script lang="ts">
  import { feedGroups, type FeedKind } from './feedGroups'
  import SessionCard from './SessionCard.svelte'
  import DeviceBanner from './DeviceBanner.svelte'
  import { t } from '../lib/i18n'

  let { onOpen, onNew }: { onOpen: (id: string, kind: FeedKind) => void; onNew: () => void } = $props()

  const groups = feedGroups
  const empty = $derived($groups.todo.length + $groups.active.length + $groups.recent.length === 0)
</script>

<header class="head">
  <h1>{$t('m.tab_sessions')}</h1>
  <button class="search" aria-label={$t('m.search')}>
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-2)" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
  </button>
</header>

<div class="scroll">
  <DeviceBanner />

  {#if empty}
    <div class="empty">{$t('m.feed_empty')}</div>
  {/if}

  {#if $groups.todo.length}
    <p class="lbl">{$t('m.sec_todo')}</p>
    {#each $groups.todo as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}

  {#if $groups.active.length}
    <p class="lbl">{$t('m.sec_active')}</p>
    {#each $groups.active as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}

  {#if $groups.recent.length}
    <p class="lbl">{$t('m.sec_recent')}</p>
    {#each $groups.recent as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}
</div>

<button class="fab" onclick={onNew} aria-label={$t('m.new_task')}>
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

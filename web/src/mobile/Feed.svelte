<script lang="ts">
  import { feedGroups, type FeedKind } from './feedGroups'
  import SessionCard from './SessionCard.svelte'
  import DeviceBanner from './DeviceBanner.svelte'
  import { t } from '../lib/i18n'

  let { onOpen, onNew, onHistory, onProject }: {
    onOpen: (id: string, kind: FeedKind) => void
    onNew: () => void
    onProject: (groupId: string) => void
    // Opens the history page: focused for search, unfocused for "view all".
    onHistory: (focus: boolean) => void
  } = $props()

  const groups = feedGroups
  const empty = $derived($groups.todo.length + $groups.active.length + $groups.projects.length + $groups.recent.length === 0)
</script>

<header class="head">
  <h1>{$t('m.tab_sessions')}</h1>
  <button class="search" aria-label={$t('m.search')} onclick={() => onHistory(true)}>
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

  {#if $groups.projects.length}
    <p class="lbl">{$t('m.sec_projects')}</p>
    <div class="plist">
      {#each $groups.projects as p (p.group.id)}
        <button class="prow" onclick={() => onProject(p.group.id)}>
          <svg class="picon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          <span class="pname">{p.group.name}</span>
          <span class="pcount">{$t('m.count_sessions').replace('{n}', String(p.items.length))}</span>
          <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-4)" stroke-width="2"><path d="M9 6l6 6-6 6"/></svg>
        </button>
      {/each}
    </div>
  {/if}

  {#if $groups.recent.length}
    <p class="lbl">{$t('m.sec_recent')}</p>
    {#each $groups.recent as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
    {#if $groups.recentTotal > $groups.recent.length}
      <button class="all" onclick={() => onHistory(false)}>
        {$t('m.view_all').replace('{n}', String($groups.recentTotal))}
      </button>
    {/if}
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
  .plist {
    background: var(--m-surface);
    border-radius: 14px;
    box-shadow: var(--m-shadow-card);
    overflow: hidden;
    margin-bottom: 12px;
  }
  .prow {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 48px;
    padding: 12px 16px;
    border: none;
    background: none;
    font: inherit;
    text-align: left;
    cursor: pointer;
  }
  .prow + .prow { border-top: 1px solid var(--m-divider); }
  .picon { flex: none; color: var(--m-text-3); }
  .pname {
    flex: 1;
    min-width: 0;
    font-size: 15px;
    color: var(--m-text);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .pcount { flex: none; font-size: 12px; color: var(--m-text-3); }
  .all {
    display: block;
    margin: 4px auto 0;
    padding: 10px 18px;
    border: none;
    background: none;
    font: inherit;
    font-size: 13px;
    color: var(--m-accent);
    cursor: pointer;
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

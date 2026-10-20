<script lang="ts">
  // One project's sessions, any state, pinned first then newest — the phone's
  // counterpart of a project's block in the desktop sidebar. Reached from the
  // feed's Projects section; its "+" starts a session inside this project.
  import { feedGroups, type FeedKind } from './feedGroups'
  import SessionCard from './SessionCard.svelte'
  import { t } from '../lib/i18n'

  let { groupId, onBack, onOpen, onNew }: {
    groupId: string
    onBack: () => void
    onOpen: (id: string, kind: FeedKind) => void
    onNew: (groupId: string) => void
  } = $props()

  const project = $derived($feedGroups.projects.find((p) => p.group.id === groupId))
</script>

<header class="dhead">
  <button class="back" onclick={onBack} aria-label={$t('m.back')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
  </button>
  <h1>{project?.group.name ?? ''}</h1>
</header>

<div class="scroll">
  {#if !project || project.items.length === 0}
    <div class="empty">{$t('m.project_empty')}</div>
  {:else}
    {#each project.items as item (item.session.id)}
      <SessionCard {item} {onOpen} showProject={false} />
    {/each}
  {/if}
</div>

{#if project}
  <button class="fab" onclick={() => onNew(groupId)} aria-label={$t('m.new_task')}>
    <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="2.4"><path d="M12 5v14M5 12h14"/></svg>
  </button>
{/if}

<style>
  .dhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 14px 12px;
  }
  .dhead h1 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 20px;
    font-weight: 600;
    color: var(--m-text-strong);
    overflow: hidden;
    white-space: nowrap;
    text-overflow: ellipsis;
  }
  .back {
    flex: none;
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
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 0 16px 20px;
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

<script lang="ts">
  // History / search: every session, pinned first then newest, filtered by
  // title as you type. Reached from the feed's search button (focused, ready to
  // type) or its "view all" link (unfocused, to browse without the keyboard).
  import { allSessions, matchesQuery, type FeedKind } from './feedGroups'
  import SessionCard from './SessionCard.svelte'
  import { t } from '../lib/i18n'

  // query is bound from the shell, so it survives opening a result and
  // coming back to the list.
  let { onBack, onOpen, focus = false, query = $bindable('') }: {
    onBack: () => void
    onOpen: (id: string, kind: FeedKind) => void
    focus?: boolean
    query?: string
  } = $props()

  let input: HTMLInputElement | undefined = $state()
  const items = $derived($allSessions.filter((it) => matchesQuery(it.session, query)))

  $effect(() => {
    if (focus) input?.focus()
  })
</script>

<header class="dhead">
  <button class="back" onclick={onBack} aria-label={$t('m.back')}>
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
  </button>
  <div class="field">
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="var(--m-text-3)" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
    <input
      bind:this={input}
      bind:value={query}
      type="search"
      placeholder={$t('m.search_ph')}
      aria-label={$t('m.search')}
      autocapitalize="off"
      spellcheck="false"
    />
  </div>
</header>

<div class="scroll">
  {#if items.length === 0}
    <div class="empty">{query.trim() ? $t('m.search_empty') : $t('m.feed_empty')}</div>
  {:else}
    <p class="lbl">{query.trim() ? $t('m.search_results').replace('{n}', String(items.length)) : $t('m.history_title')}</p>
    {#each items as item (item.session.id)}
      <SessionCard {item} {onOpen} />
    {/each}
  {/if}
</div>

<style>
  .dhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 6px 14px 12px;
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
  .field {
    flex: 1;
    min-width: 0;
    display: flex;
    align-items: center;
    gap: 8px;
    height: 36px;
    padding: 0 12px;
    border-radius: 18px;
    background: var(--m-surface-2);
  }
  .field input {
    flex: 1;
    min-width: 0;
    border: none;
    outline: none;
    background: none;
    font: inherit;
    font-size: 14px;
    color: var(--m-text);
    -webkit-appearance: none;
    appearance: none;
  }
  .field input::placeholder { color: var(--m-text-4); }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    -webkit-overflow-scrolling: touch;
    padding: 0 16px 20px;
  }
  .lbl {
    margin: 4px 2px 8px;
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
</style>

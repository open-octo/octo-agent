<script lang="ts">
  // The open session's artifacts — the pages, Markdown and images the agent
  // wrote, collected by the shared data layer (lib/artifacts.ts) from the
  // transcript and the live stream (chatWiring). A list, and a full-screen
  // viewer for the one tapped.
  import { artifacts } from '../lib/stores'
  import { hydrateArtifact } from '../lib/artifacts'
  import { t } from '../lib/i18n'
  import PageViewer from './PageViewer.svelte'
  import { pageSrc } from './pageURL'

  let { onClose }: { onClose: () => void } = $props()

  // Track the open artifact by path and read it back from the store, so a live
  // rewrite (a new entry object) or the lazy build landing shows up here.
  let openPath = $state<string | null>(null)
  const open = $derived(openPath ? $artifacts.find((a) => a.path === openPath) ?? null : null)

  $effect(() => {
    if (open && !open.loaded) void hydrateArtifact(open)
  })

  // Newest first: the list grows at the end as the agent writes.
  const items = $derived([...$artifacts].reverse())
</script>

<div class="sheet">
  <header class="shead">
    <button class="back" onclick={onClose} aria-label={$t('m.back')}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-text)" stroke-width="2"><path d="m15 18-6-6 6-6"/></svg>
    </button>
    <span class="stitle">{$t('m.artifacts')}</span>
  </header>
  <div class="scroll">
    {#if items.length === 0}
      <div class="empty">{$t('m.no_artifacts')}</div>
    {:else}
      <div class="card group">
        {#each items as a (a.path)}
          <button class="row" onclick={() => (openPath = a.path)}>
            <span class="kind">{a.type}</span>
            <div class="names">
              <span class="name">{a.name}</span>
              <span class="sub">{a.path}</span>
            </div>
          </button>
        {/each}
      </div>
    {/if}
  </div>
</div>

{#if open}
  <PageViewer
    title={open.name}
    src={open.originURL ? pageSrc(open.originURL, open.rev ?? 0) : ''}
    srcdoc={open.originURL ? '' : open.preview}
    image={open.src ?? ''}
    loading={!open.loaded}
    failed={!!open.loadFailed || !!open.originUnavailable}
    onClose={() => (openPath = null)}
  />
{/if}

<style>
  .sheet {
    position: fixed;
    inset: 0;
    z-index: 40;
    display: flex;
    flex-direction: column;
    background: var(--m-bg);
    padding-top: env(safe-area-inset-top);
  }
  .shead { flex: none; display: flex; align-items: center; gap: 10px; padding: 6px 14px 12px; }
  .back {
    flex: none; width: 34px; height: 34px; border-radius: 50%; border: none;
    background: var(--m-surface); display: flex; align-items: center; justify-content: center;
    cursor: pointer; box-shadow: var(--m-shadow-card);
  }
  .stitle { font-size: 15px; font-weight: 600; color: var(--m-text); }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 0 16px calc(20px + env(safe-area-inset-bottom)); }
  .card { background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card); overflow: hidden; }
  .row {
    display: flex; align-items: center; gap: 12px; width: 100%; padding: 13px 16px;
    background: none; border: none; font-family: inherit; text-align: left; cursor: pointer;
  }
  .row + .row { border-top: 1px solid var(--m-divider); }
  .kind {
    flex: none; font-size: 10px; padding: 2px 7px; border-radius: 9999px;
    background: var(--m-surface-2); color: var(--m-text-3);
  }
  .names { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
  .name { font-size: 14.5px; color: var(--m-text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .sub { font-size: 11.5px; color: var(--m-text-4); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .empty { padding: 40px 16px; text-align: center; font-size: 13px; color: var(--m-text-3); }
</style>

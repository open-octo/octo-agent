<script lang="ts">
  // Full-screen artifact viewer. The artifacts store entries already carry a
  // self-contained preview document (built in lib/artifacts.ts for the desktop
  // panel's sandboxed iframe) and the raw source — this just presents them
  // phone-sized: preview in the same sandboxed iframe, code as scrollable text.
  import type { Artifact } from '../lib/types'
  import { t } from '../lib/i18n'

  let { artifact, onClose }: { artifact: Artifact; onClose: () => void } = $props()

  let view = $state<'preview' | 'code'>('preview')
  // Images have no meaningful source view (code is just the fetch URL).
  const isImage = $derived(artifact.type === 'Image')
  // Reset to preview only when a DIFFERENT file opens — a live re-write of the
  // same path swaps the entry object but must not kick the user out of Code.
  // svelte-ignore state_referenced_locally -- the initial path is exactly what we want captured
  let lastPath = $state(artifact.path)
  $effect(() => {
    if (artifact.path !== lastPath) {
      lastPath = artifact.path
      view = 'preview'
    }
  })
</script>

<div class="viewer">
  <header class="vhead">
    <button class="back" aria-label={$t('m.back')} onclick={onClose}>
      <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M15 18l-6-6 6-6"/></svg>
    </button>
    <div class="names">
      <span class="vname">{artifact.name}</span>
      <span class="vtype">{artifact.type}</span>
    </div>
    {#if !isImage}
      <div class="seg">
        <button class="segi" class:on={view === 'preview'} aria-pressed={view === 'preview'} onclick={() => (view = 'preview')}>{$t('m.preview')}</button>
        <button class="segi" class:on={view === 'code'} aria-pressed={view === 'code'} onclick={() => (view = 'code')}>{$t('m.code')}</button>
      </div>
    {/if}
  </header>

  <div class="vbody">
    {#if view === 'preview' || isImage}
      <iframe srcdoc={artifact.preview} sandbox="allow-scripts clipboard-write" title={artifact.name}></iframe>
    {:else}
      <pre class="code-view">{artifact.code}</pre>
    {/if}
  </div>
</div>

<style>
  .viewer {
    position: fixed;
    inset: 0;
    z-index: 50;
    display: flex;
    flex-direction: column;
    background: var(--m-bg);
  }
  .vhead {
    flex: none;
    display: flex;
    align-items: center;
    gap: 10px;
    padding: calc(8px + env(safe-area-inset-top)) 14px 10px;
    background: var(--m-surface);
    border-bottom: 1px solid var(--m-border-2);
  }
  .back {
    width: 34px; height: 34px; border-radius: 50%; border: none; flex: none;
    background: var(--m-surface-2); color: var(--m-text); cursor: pointer;
    display: flex; align-items: center; justify-content: center;
  }
  .names { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 1px; }
  .vname { font-size: 14px; font-weight: 600; color: var(--m-text-strong); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .vtype { font-size: 11px; color: var(--m-text-3); }
  .seg { flex: none; display: flex; gap: 3px; background: var(--m-bg); border-radius: 8px; padding: 3px; }
  .segi {
    padding: 6px 12px; border-radius: 6px; border: none; background: none;
    font-size: 12px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .segi.on { background: var(--m-accent); color: #fff; font-weight: 600; }

  .vbody { flex: 1; min-height: 0; background: var(--m-surface); }
  iframe { border: 0; width: 100%; height: 100%; display: block; }
  .code-view {
    margin: 0; height: 100%; box-sizing: border-box; overflow: auto;
    -webkit-overflow-scrolling: touch;
    padding: 14px 16px calc(14px + env(safe-area-inset-bottom));
    font: 12px/1.7 ui-monospace, SFMono-Regular, Menlo, monospace;
    color: var(--m-text); white-space: pre;
  }
</style>

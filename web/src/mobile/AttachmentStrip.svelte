<script lang="ts">
  // The staged-attachment row of a mobile composer: image thumbnails and file
  // name chips, each removable once it has finished staging.
  import { t } from '../lib/i18n'
  import AuthedImage from './AuthedImage.svelte'
  import type { AttachmentTray } from './attachments.svelte'

  let { tray }: { tray: AttachmentTray } = $props()
</script>

<div class="strip">
  {#each tray.items as a (a.id)}
    {#if a.data_url}
      <span class="thumb">
        <AuthedImage src={a.data_url} alt={a.name} />
        <button class="rm" onclick={() => tray.remove(a.id)} aria-label={$t('chat.remove')}>×</button>
      </span>
    {:else}
      <span class="chip" class:busy={a.uploading}>
        {a.name}
        {#if !a.uploading}
          <button class="rm inline" onclick={() => tray.remove(a.id)} aria-label={$t('chat.remove')}>×</button>
        {/if}
      </span>
    {/if}
  {/each}
</div>

<style>
  .strip {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
  }
  .thumb {
    position: relative;
    width: 64px;
    height: 64px;
    border-radius: 10px;
    overflow: hidden;
    flex: none;
  }
  .chip {
    display: inline-flex;
    align-items: center;
    gap: 4px;
    max-width: 200px;
    padding: 6px 10px;
    border-radius: 10px;
    background: var(--m-surface-2);
    border: 1px solid var(--m-border);
    color: var(--m-text);
    font-size: 12.5px;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .chip.busy { opacity: .6; }
  .rm {
    position: absolute;
    top: 2px;
    right: 2px;
    width: 20px;
    height: 20px;
    border-radius: 50%;
    border: none;
    background: rgba(0, 0, 0, .55);
    color: #fff;
    font-size: 14px;
    line-height: 1;
    display: flex;
    align-items: center;
    justify-content: center;
    cursor: pointer;
  }
  .rm.inline {
    position: static;
    flex: none;
    background: none;
    color: var(--m-text-3);
  }
</style>

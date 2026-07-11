<script lang="ts">
  import { onMount } from 'svelte'
  import * as api from '../../lib/api'
  import type { FsListing } from '../../lib/api'
  import { t } from '../../lib/i18n'

  // Controlled by the composer: initialPath seeds the first listing (the
  // session's current working dir, or '' to start at home). onSelect receives
  // the absolute directory the user confirms; onClose dismisses.
  // mode 'folder' (default): navigate dirs, confirm the current dir with "Use
  // this folder"; files are shown greyed for context. mode 'file': click a file
  // to select it (onSelect gets the file path); dirs still navigate.
  let { initialPath = '', mode = 'folder', onSelect, onClose }: {
    initialPath?: string
    mode?: 'folder' | 'file'
    onSelect: (path: string) => void
    onClose: () => void
  } = $props()

  function join(name: string): string {
    const base = listing?.path ?? ''
    return base + (base.endsWith('/') ? '' : '/') + name
  }

  let listing = $state<FsListing | null>(null)
  let loading = $state(false)
  let error = $state('')
  let showHidden = $state(false)
  let modalEl = $state<HTMLDivElement | null>(null)

  async function load(path?: string) {
    loading = true
    error = ''
    try {
      listing = await api.fsList(path)
    } catch (e: any) {
      // A 403 lands here with the server's "local machine only" message; any
      // other failure (bad path, permission) shows its message too. Keep the
      // previous listing so the user can still navigate elsewhere.
      error = e?.message ?? 'Failed to list directory'
    } finally {
      loading = false
    }
  }

  // Focus the modal so Esc works without stealing the composer's focus
  // underneath.
  $effect(() => {
    if (modalEl) modalEl.focus()
  })
  // Seed the first listing from the session's current dir (or home when empty).
  onMount(() => load(initialPath || undefined))

  let visibleEntries = $derived(
    (listing?.entries ?? []).filter((e) => showHidden || !e.name.startsWith('.'))
  )

  // Show the last few path segments so a long or non-ASCII path (e.g. a CJK
  // folder name) stays readable without a `direction: rtl` hack, which can
  // reorder bidirectional text. Full path is in the title tooltip.
  function shortPath(p: string): string {
    if (!p) return ''
    const parts = p.split('/').filter(Boolean)
    return parts.length <= 3 ? p : '…/' + parts.slice(-3).join('/')
  }

  function enter(name: string) {
    if (!listing) return
    load(join(name))
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }
</script>

<div class="backdrop" role="presentation" onclick={onClose}>
  <div
    class="modal"
    role="dialog"
    aria-modal="true"
    tabindex="-1"
    bind:this={modalEl}
    onkeydown={onKeydown}
    onclick={(e) => e.stopPropagation()}
  >
    <div class="modal-header">
      <iconify-icon icon="ant-design:folder-open-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
      <span class="modal-title">{mode === 'file' ? $t('folder.title_file') : $t('folder.title')}</span>
      <label class="hidden-toggle">
        <input type="checkbox" bind:checked={showHidden} />
        {$t('folder.show_hidden')}
      </label>
    </div>

    <div class="path-bar">
      <button
        class="up-btn"
        disabled={!listing?.parent || loading}
        title={$t('folder.up')}
        onclick={() => listing?.parent && load(listing.parent)}
      >
        <iconify-icon icon="lucide:corner-left-up" width="14"></iconify-icon>
      </button>
      <span class="cur-path mono" title={listing?.path ?? ''}>{shortPath(listing?.path ?? '')}</span>
    </div>

    <div class="modal-body">
      {#if error}
        <p class="error-msg">{error}</p>
      {/if}
      {#if loading}
        <p class="muted">{$t('folder.loading')}</p>
      {:else if listing}
        {#if visibleEntries.length === 0 && !error}
          <p class="muted">{$t('folder.empty')}</p>
        {/if}
        <ul class="entries">
          {#each visibleEntries as e (e.name)}
            {#if e.is_dir}
              <li>
                <button class="entry dir" onclick={() => enter(e.name)}>
                  <iconify-icon icon="ant-design:folder-outlined" width="14" style="color:var(--blue-6)"></iconify-icon>
                  <span class="mono name">{e.name}</span>
                  {#if e.is_symlink}
                    <iconify-icon icon="lucide:link" width="11" style="color:var(--text-tertiary)"></iconify-icon>
                  {/if}
                </button>
              </li>
            {:else if mode === 'file'}
              <li>
                <button class="entry dir" onclick={() => onSelect(join(e.name))}>
                  <iconify-icon icon="ant-design:file-outlined" width="14" style="color:var(--text-secondary)"></iconify-icon>
                  <span class="mono name">{e.name}</span>
                </button>
              </li>
            {:else}
              <li class="entry file" aria-disabled="true">
                <iconify-icon icon="ant-design:file-outlined" width="14" style="color:var(--text-tertiary)"></iconify-icon>
                <span class="mono name">{e.name}</span>
              </li>
            {/if}
          {/each}
        </ul>
        {#if listing.truncated}
          <p class="muted truncated">{$t('folder.truncated')}</p>
        {/if}
      {/if}
    </div>

    <div class="modal-footer">
      <button class="btn-secondary" onclick={onClose}>{$t('folder.cancel')}</button>
      <span class="spacer"></span>
      {#if mode === 'folder'}
        <button
          class="btn-primary"
          disabled={!listing || loading}
          onclick={() => listing && onSelect(listing.path)}
        >
          <iconify-icon icon="ant-design:check-outlined" width="12"></iconify-icon>
          {$t('folder.select')}
        </button>
      {/if}
    </div>
  </div>
</div>

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 1100;
  background: var(--text-tertiary);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 520px;
  display: flex; flex-direction: column;
  max-height: 70vh;
  background: var(--bg-container);
  border: 1px solid var(--border);
  border-radius: 12px;
  overflow: hidden;
  box-shadow: 0 16px 48px rgba(0,0,0,0.18);
  animation: octo-fadein 0.16s ease;
}
.modal:focus { outline: none; }
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-bottom: 1px solid var(--border);
}
.modal-title { font-size: 14px; font-weight: 600; color: var(--text-heading); flex: 1; }
.hidden-toggle {
  display: flex; align-items: center; gap: 5px;
  font-size: 12px; color: var(--text-secondary); cursor: pointer;
}
.path-bar {
  display: flex; align-items: center; gap: 8px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-layout);
}
.up-btn {
  flex-shrink: 0;
  height: 26px; width: 26px;
  display: flex; align-items: center; justify-content: center;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; cursor: pointer; color: var(--text-secondary);
}
.up-btn:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.up-btn:disabled { opacity: 0.4; cursor: default; }
.cur-path {
  font-size: 12px; color: var(--text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.modal-body {
  padding: 8px 10px;
  overflow-y: auto;
  flex: 1;
}
.entries { list-style: none; margin: 0; padding: 0; }
.entry {
  display: flex; align-items: center; gap: 8px;
  width: 100%;
  padding: 6px 8px;
  border-radius: 6px;
  font-size: 13px;
  text-align: left;
}
.entry.dir {
  border: none; background: none; cursor: pointer; color: var(--text-primary);
  font-family: inherit;
}
.entry.dir:hover { background: var(--bg-hover, var(--bg-layout)); }
.entry.file { color: var(--text-tertiary); cursor: default; }
.entry .name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.muted { margin: 8px; font-size: 12px; color: var(--text-tertiary); }
.truncated { color: var(--warning, var(--text-tertiary)); }
.error-msg {
  margin: 8px; padding: 8px 10px;
  font-size: 12px; line-height: 1.5;
  color: var(--error-dark, var(--error)); background: var(--error-bg);
  border-radius: 6px;
}
.modal-footer {
  padding: 12px 18px;
  border-top: 1px solid var(--border);
  display: flex; align-items: center; gap: 8px;
}
.spacer { flex: 1; }
.btn-secondary {
  height: 32px; padding: 0 12px;
  border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary);
  cursor: pointer; font-family: inherit;
}
.btn-secondary:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-primary {
  height: 32px; padding: 0 14px;
  border: none; background: var(--blue-6);
  border-radius: 6px;
  display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: #fff; cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: default; }
</style>

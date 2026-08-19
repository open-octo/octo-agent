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
  // The path bar is editable: typing or pasting a path and pressing Enter
  // jumps straight there. Clicking through a tree is the slow way to reach a
  // path you already know, and this dialog is now the only way to set a
  // working directory (the composer's typed-path popover was folded into it),
  // so it has to carry both. Mirrors an OS file dialog's address bar.
  let pathDraft = $state('')

  async function load(path?: string) {
    loading = true
    error = ''
    try {
      listing = await api.fsList(path)
      // Follow the listing: navigating the tree updates the field, and a
      // rejected hand-typed path is replaced by where we actually are.
      pathDraft = listing.is_this_pc ? '' : listing.path
    } catch (e: any) {
      // A 403 lands here with the server's "local machine only" message; any
      // other failure (bad path, permission) shows its message too. Keep the
      // previous listing so the user can still navigate elsewhere.
      error = e?.message ?? 'Failed to list directory'
    } finally {
      loading = false
    }
  }

  // Navigate to whatever is typed in the path bar. A bad path surfaces the
  // server's error and leaves the current listing in place.
  async function gotoDraft() {
    const p = pathDraft.trim()
    if (!p || p === listing?.path) return
    await load(p)
  }

  // Confirm the current directory. A path typed but not yet entered would
  // otherwise silently confirm the OLD directory, so resolve it first and
  // only confirm if it actually loaded.
  async function chooseCurrent() {
    if (pathDraft.trim() && pathDraft.trim() !== listing?.path) {
      await gotoDraft()
      if (error) return
    }
    if (listing && !listing.is_this_pc) onSelect(listing.path)
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
      {#if listing?.is_this_pc}
        <span class="cur-path mono">{$t('folder.this_pc')}</span>
      {:else}
        <input
          class="cur-path mono"
          bind:value={pathDraft}
          spellcheck="false"
          autocomplete="off"
          placeholder={$t('folder.path_placeholder')}
          title={listing?.path ?? ''}
          aria-label={$t('folder.path_placeholder')}
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); gotoDraft() } }}
        />
      {/if}
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
          disabled={!listing || loading || listing.is_this_pc}
          onclick={() => chooseCurrent()}
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
  background: var(--scrim);
  display: flex; align-items: center; justify-content: center;
  padding: 24px;
}
.modal {
  width: 100%; max-width: 520px;
  display: flex; flex-direction: column;
  max-height: calc(70vh / var(--font-zoom));
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
  flex: 1; min-width: 0;
  font-size: 12px; color: var(--text-secondary);
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
input.cur-path {
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-container);
  color: var(--text-primary);
}
input.cur-path:focus { outline: none; border-color: var(--blue-5); }
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
  border: none; background: none; cursor: pointer; color: var(--text);
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

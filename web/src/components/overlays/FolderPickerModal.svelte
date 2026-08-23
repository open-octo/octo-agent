<script lang="ts">
  import { onMount, tick } from 'svelte'
  import * as api from '../../lib/api'
  import type { FsListing } from '../../lib/api'
  import { t } from '../../lib/i18n'

  // Controlled by the composer: initialPath seeds the first listing (the
  // session's current working dir, or '' to start at home). onSelect receives
  // the absolute directory the user confirms; onClose dismisses.
  // mode 'folder' (default): navigate dirs, confirm the current dir with "Use
  // this folder"; files are shown greyed for context. mode 'file': click a file
  // to select it (onSelect gets the file path); dirs still navigate.
  //
  // shortcuts are one-click jumps shown above the listing — for a project
  // session, its workspace and every folder it mounts. They exist because this
  // dialog always opens cold at one directory, and the one it opens at is
  // rarely where the file lives: a project's material is in the folders it
  // mounts, while the workspace it starts in is scratch. Jumps, not a ranking:
  // every place the project has is offered and none is privileged. Empty for
  // callers with nowhere particular to offer, which hides the row.
  let { initialPath = '', mode = 'folder', shortcuts = [], onSelect, onClose }: {
    initialPath?: string
    mode?: 'folder' | 'file'
    shortcuts?: { label: string; path: string }[]
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
  let pathEl = $state<HTMLInputElement | null>(null)
  let selectOnClick = false
  // The path bar is editable: typing or pasting a path and pressing Enter
  // jumps straight there. Clicking through a tree is the slow way to reach a
  // path you already know, and this dialog is now the only way to set a
  // working directory (the composer's typed-path popover was folded into it),
  // so it has to carry both. Mirrors an OS file dialog's address bar.
  let pathDraft = $state('')

  // Generation guard (like Composer's modelsFetchSeq): the path input stays
  // enabled while a listing loads — disabling a field mid-type is worse than
  // the race — so two Enters, or an edit before the first returns, can overlap.
  // Without this a slow failure landing after a fast success would hang a stale
  // error banner over the new listing and clear `loading` early.
  let loadSeq = 0

  async function load(path?: string) {
    const seq = ++loadSeq
    loading = true
    error = ''
    try {
      const next = await api.fsList(path)
      if (seq !== loadSeq) return // a newer navigation already won
      listing = next
      // Follow the listing: navigating the tree updates the field. A rejected
      // path is deliberately NOT reverted (see the catch) so the typo stays
      // there to be fixed; Escape restores it.
      pathDraft = next.is_this_pc ? '' : next.path
      // A long path overflows the field from the left, hiding the tail — but
      // the tail (which folder am I in?) is the part worth reading. The
      // read-only bar this replaced elided the head for the same reason.
      // Scroll to the end unless the user is mid-edit.
      await tick()
      if (pathEl && document.activeElement !== pathEl) pathEl.scrollLeft = pathEl.scrollWidth
    } catch (e: any) {
      if (seq !== loadSeq) return
      // A 403 lands here with the server's "local machine only" message; any
      // other failure (bad path, permission) shows its message too. Keep the
      // previous listing — and the text the user typed — so the path can be
      // corrected rather than retyped from scratch.
      error = e?.message ?? 'Failed to list directory'
    } finally {
      if (seq === loadSeq) loading = false
    }
  }

  // Escape in the path field restores it to the directory actually shown
  // instead of closing the dialog. Without this a rejected path strands the
  // user: chooseCurrent re-resolves the draft, so the confirm button keeps
  // refusing, and nothing offers a way back to where they are.
  function revertDraft(): boolean {
    const shown = listing?.is_this_pc ? '' : (listing?.path ?? '')
    if (pathDraft === shown) return false
    pathDraft = shown
    error = ''
    return true
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
          bind:this={pathEl}
          bind:value={pathDraft}
          spellcheck="false"
          autocomplete="off"
          placeholder={$t('folder.path_placeholder')}
          title={listing?.path ?? ''}
          aria-label={$t('folder.path_label')}
          onfocus={() => { selectOnClick = true; pathEl?.select() }}
          onblur={() => (selectOnClick = false)}
          onmouseup={(e) => {
            // focus() selects the whole path, then mouseup collapses the
            // selection to the click point — so select-all only survives if
            // we suppress that. A drag (selection already a range) is left
            // alone: the user picked their own span deliberately.
            if (!selectOnClick) return
            selectOnClick = false
            const el = e.currentTarget as HTMLInputElement
            if (el.selectionStart === el.selectionEnd) { e.preventDefault(); el.select() }
          }}
          onkeydown={(e) => {
            if (e.key === 'Enter') { e.preventDefault(); gotoDraft() }
            // Only swallow Escape when there is an edit to undo; otherwise let
            // it bubble and close the dialog, as it does everywhere else.
            else if (e.key === 'Escape' && revertDraft()) { e.preventDefault(); e.stopPropagation() }
          }}
        />
      {/if}
    </div>

    {#if shortcuts.length > 0}
      <div class="shortcuts">
        {#each shortcuts as s (s.path)}
          <button
            class="shortcut"
            class:here={listing?.path === s.path}
            title={s.path}
            disabled={loading}
            onclick={() => load(s.path)}
          >
            <iconify-icon icon="ant-design:folder-outlined" width="12"></iconify-icon>
            <span class="shortcut-label">{s.label}</span>
          </button>
        {/each}
      </div>
    {/if}

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
  /* An input scrolls; the inherited ellipsis fights scrollLeft and paints a
     "…" mid-path, hiding both ends at once. */
  text-overflow: clip;
  height: 26px;
  padding: 0 8px;
  border: 1px solid var(--border);
  border-radius: 6px;
  background: var(--bg-container);
  color: var(--text);
}
input.cur-path:focus { outline: none; border-color: var(--blue-5); }
/* Wraps rather than scrolls: a project with several mounts must show all of
   them at once — a hidden shortcut is no shortcut. */
.shortcuts {
  display: flex; flex-wrap: wrap; gap: 6px;
  padding: 8px 14px;
  border-bottom: 1px solid var(--border);
  background: var(--bg-layout);
}
.shortcut {
  display: flex; align-items: center; gap: 5px;
  max-width: 100%; min-width: 0;
  height: 24px; padding: 0 9px;
  border: 1px solid var(--border); border-radius: 12px;
  background: var(--bg-container); color: var(--text-secondary);
  font-size: 12px; cursor: pointer;
}
.shortcut:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.shortcut:disabled { opacity: 0.5; cursor: default; }
/* The one you are already in — so the row doubles as "where am I". */
.shortcut.here { border-color: var(--blue-5); color: var(--blue-6); background: var(--blue-1); }
.shortcut-label { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
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

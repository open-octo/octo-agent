<script lang="ts">
  // Project settings. A project is a directory plus the sessions working in it:
  // every session in it runs its tools there, and its memory is scoped to the
  // project. The directory cannot be emptied — that used to demote the project
  // to a plain group, a concept that no longer exists — so Save refuses an empty
  // one here rather than sending a request the server will reject.
  import { get } from 'svelte/store'
  import { untrack } from 'svelte'
  import * as api from '../../lib/api'
  import { sessionGroups, nativeShell, showToast } from '../../lib/stores'
  import { t, tr } from '../../lib/i18n'
  import type { SessionGroup } from '../../lib/types'
  import FolderPickerModal from './FolderPickerModal.svelte'

  let { group, onClose }: { group: SessionGroup; onClose: () => void } = $props()

  // Editing drafts: seeded from the group once, on open. Deliberately NOT
  // derived — a store refresh mid-edit must not overwrite what the user typed.
  let dir = $state(untrack(() => group.working_dir ?? ''))
  let notes = $state(untrack(() => group.notes ?? ''))
  let saving = $state(false)
  let pickerOpen = $state(false)
  let modalEl = $state<HTMLDivElement | null>(null)

  // Focus the modal so Esc works without the user having to click into a
  // field first (same pattern as FolderPickerModal).
  $effect(() => {
    if (modalEl && !pickerOpen) modalEl.focus()
  })

  async function browse() {
    if (get(nativeShell)) {
      try {
        const res = await api.nativePickFolder(dir)
        if (!res.cancelled && res.path) dir = res.path
      } catch (e: any) {
        showToast(e?.message ?? tr('project.browse_fail'), 'error')
      }
      return
    }
    pickerOpen = true
  }

  async function save() {
    if (saving) return
    // Only submit what actually changed: sending an unchanged working_dir
    // would re-validate it, so a project whose directory was deleted on disk
    // couldn't even edit its notes.
    if (dir.trim() === '') {
      showToast(tr('project.dir_required'), 'error')
      return
    }
    const patch: { working_dir?: string; notes?: string } = {}
    if (dir.trim() !== (group.working_dir ?? '')) patch.working_dir = dir.trim()
    if (notes.trim() !== (group.notes ?? '')) patch.notes = notes.trim()
    if (Object.keys(patch).length === 0) { onClose(); return }
    saving = true
    try {
      const updated = await api.updateSessionGroup(group.id, patch)
      // Patch the store in place so the sidebar header and every composer dir
      // chip in the project reflect the change without a refetch.
      sessionGroups.update(gs => gs.map(g => (g.id === group.id ? { ...g, ...updated } : g)))
      showToast(tr('project.saved').replace('{dir}', updated.working_dir ?? dir.trim()), 'success')
      onClose()
    } catch (e: any) {
      showToast(e?.message ?? tr('project.save_fail'), 'error')
      saving = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') { e.preventDefault(); onClose() }
  }
</script>

<div class="backdrop" role="presentation" onclick={onClose}>
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" bind:this={modalEl} onkeydown={onKeydown} onclick={(e) => e.stopPropagation()}>
    <div class="modal-header">
      <iconify-icon icon="ant-design:setting-outlined" width="16" style="color:var(--blue-6);flex-shrink:0"></iconify-icon>
      <span class="modal-title">{$t('project.title').replace('{name}', group.name)}</span>
    </div>

    <div class="modal-body">
      <label class="lbl" for="proj-dir">{$t('project.working_dir')}</label>
      <div class="dir-row">
        <input
          id="proj-dir"
          class="input mono"
          bind:value={dir}
          placeholder="~/code/my-project"
          spellcheck="false"
          onkeydown={(e) => { if (e.key === 'Enter') { e.preventDefault(); save() } }}
        />
        <button class="btn-secondary browse" onclick={browse}>
          <iconify-icon icon="ant-design:folder-open-outlined" width="12"></iconify-icon>
          {$t('chat.dir_browse')}
        </button>
      </div>
      <p class="hint">{$t('project.working_dir_hint').replace('{n}', String(group.session_ids.length))}</p>

      <label class="lbl" for="proj-notes">{$t('project.notes')}</label>
      <textarea
        id="proj-notes"
        class="input notes"
        rows="4"
        bind:value={notes}
        placeholder={$t('project.notes_ph')}
      ></textarea>
      <p class="hint">{$t('project.notes_hint')}</p>
    </div>

    <div class="modal-footer">
      <button class="btn-secondary" onclick={onClose}>{$t('folder.cancel')}</button>
      <span class="spacer"></span>
      <button class="btn-primary" disabled={saving} onclick={save}>
        <iconify-icon icon="ant-design:check-outlined" width="12"></iconify-icon>
        {saving ? $t('project.saving') : $t('project.save')}
      </button>
    </div>
  </div>
</div>

{#if pickerOpen}
  <FolderPickerModal
    initialPath={dir}
    mode="folder"
    onSelect={(p) => { dir = p; pickerOpen = false }}
    onClose={() => (pickerOpen = false)}
  />
{/if}

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
  max-height: calc(80vh / var(--font-zoom));
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
.modal-body { padding: 16px 18px; overflow-y: auto; }
.lbl {
  display: block; margin-bottom: 6px;
  font-size: 12px; font-weight: 600; color: var(--text-secondary);
}
.dir-row { display: flex; gap: 8px; align-items: stretch; }
.input {
  width: 100%; box-sizing: border-box;
  padding: 7px 10px;
  border: 1px solid var(--border); border-radius: 7px;
  background: var(--bg-container); color: var(--text);
  font-size: 13px; font-family: inherit;
  outline: none;
}
.input:focus { border-color: var(--blue-5); }
.notes { resize: vertical; line-height: 1.5; }
.browse { flex-shrink: 0; white-space: nowrap; }
.hint { margin: 6px 2px 16px; font-size: 12px; color: var(--text-tertiary); }
.modal-footer {
  display: flex; align-items: center; gap: 8px;
  padding: 12px 18px;
  border-top: 1px solid var(--border);
}
.spacer { flex: 1; }
</style>

<script lang="ts">
  import { get } from 'svelte/store'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import { sessionGroups, nativeShell, showToast, normalizeDir } from '../../lib/stores'
  import FolderPickerModal from './FolderPickerModal.svelte'
  import type { SessionGroup } from '../../lib/types'

  // One modal, two modes: group == null creates a project (name + source
  // folders, zero folders is a legitimate shape), otherwise it edits one
  // (rename, mount/unmount folders, mark the output folder). The workspace is
  // the server's business — it never appears as an input here.
  let { group = null, onClose, onSaved }: {
    group?: SessionGroup | null
    onClose: () => void
    onSaved?: (g: SessionGroup) => void
  } = $props()

  // Initial-value captures are the point: the modal is {#if}-gated and mounts
  // fresh per open, so the draft must NOT follow live store updates while the
  // user edits.
  // svelte-ignore state_referenced_locally
  let name = $state(group?.name ?? '')
  // svelte-ignore state_referenced_locally
  let sourceDirs = $state<string[]>([...(group?.source_dirs ?? [])])
  // svelte-ignore state_referenced_locally
  let outputDir = $state(group?.output_dir ?? '')
  let saving = $state(false)
  let pickerOpen = $state(false)
  let modalEl = $state<HTMLDivElement | null>(null)

  // No modal-level focus steal: the name input autofocuses, and Escape still
  // reaches the modal div's onkeydown by bubbling from whatever child has focus.
  function addFolder(path: string) {
    const norm = normalizeDir(path)
    if (!sourceDirs.some(d => normalizeDir(d) === norm)) sourceDirs = [...sourceDirs, path]
    pickerOpen = false
  }

  async function openFolderPicker() {
    if (get(nativeShell)) {
      try {
        const res = await api.nativePickFolder('')
        if (!res.cancelled && res.path) addFolder(res.path)
      } catch (e: any) {
        showToast(e.message ?? 'Failed to open folder dialog', 'error')
      }
      return
    }
    pickerOpen = true
  }

  function removeFolder(dir: string) {
    sourceDirs = sourceDirs.filter(d => d !== dir)
    if (outputDir === dir) outputDir = ''
  }

  function toggleOutput(dir: string) {
    outputDir = outputDir === dir ? '' : dir
  }

  async function save() {
    const trimmed = name.trim()
    if (!trimmed) return
    saving = true
    try {
      let g: SessionGroup
      if (group) {
        g = await api.updateSessionGroup(group.id, { name: trimmed, source_dirs: sourceDirs, output_dir: outputDir })
        sessionGroups.update(gs => gs.map(x => (x.id === g.id ? g : x)))
      } else {
        // One request: the server accepts output_dir at creation, so there is
        // no created-but-unmarked window for a failure to strand us in.
        g = await api.createSessionGroup(trimmed, { source_dirs: sourceDirs, ...(outputDir ? { output_dir: outputDir } : {}) })
        sessionGroups.update(gs => [...gs, g])
      }
      onSaved?.(g)
    } catch (e: any) {
      showToast(e.message ?? 'Failed to save the project', 'error')
    } finally {
      saving = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      onClose()
    }
  }
</script>

<div class="backdrop" role="presentation">
  <div class="modal" role="dialog" aria-modal="true" tabindex="-1" bind:this={modalEl} onkeydown={onKeydown}>
    <div class="modal-header">
      <span class="modal-title">{group ? $t('project.modal_settings') : $t('project.modal_create')}</span>
      <button class="close" onclick={onClose} aria-label="close">
        <iconify-icon icon="lucide:x" width="16"></iconify-icon>
      </button>
    </div>

    <div class="modal-body">
      <!-- svelte-ignore a11y_autofocus -->
      <input class="name-input" placeholder={$t('project.name_ph')} bind:value={name} autofocus />

      <div class="section-label">{$t('project.source_dirs')}</div>
      {#each sourceDirs as dir (dir)}
        <div class="folder-row">
          <iconify-icon icon="ant-design:folder-outlined" width="14"></iconify-icon>
          <span class="mono folder-path" title={dir}>{dir}</span>
          <button
            class="row-btn"
            class:active={outputDir === dir}
            title={$t('project.output_mark')}
            onclick={() => toggleOutput(dir)}
          >
            <iconify-icon icon={outputDir === dir ? 'ant-design:star-filled' : 'ant-design:star-outlined'} width="14"></iconify-icon>
          </button>
          <button class="row-btn" title={$t('project.remove_folder')} onclick={() => removeFolder(dir)}>
            <iconify-icon icon="lucide:x" width="14"></iconify-icon>
          </button>
        </div>
      {/each}
      <button class="add-folder" onclick={openFolderPicker}>
        <iconify-icon icon="ant-design:folder-add-outlined" width="18"></iconify-icon>
        <span>{$t('project.add_folder')}</span>
      </button>
      {#if outputDir}
        <div class="hint">{$t('project.output_hint')}</div>
      {/if}
    </div>

    <div class="modal-footer">
      <button class="btn" onclick={onClose}>{$t('common.cancel')}</button>
      <button class="btn primary" disabled={saving || !name.trim()} onclick={save}>
        {group ? $t('project.save') : $t('project.create')}
      </button>
    </div>
  </div>
</div>

{#if pickerOpen}
  <FolderPickerModal initialPath="" mode="folder" onSelect={addFolder} onClose={() => (pickerOpen = false)} />
{/if}

<style>
.backdrop {
  position: fixed; inset: 0; z-index: 200;
  background: var(--overlay-bg, rgba(0, 0, 0, 0.45));
  display: flex; align-items: center; justify-content: center;
}
.modal {
  width: min(520px, calc(100vw - 48px));
  background: var(--bg-container); border: 1px solid var(--border-secondary);
  border-radius: 14px; outline: none;
  display: flex; flex-direction: column; max-height: 80vh;
}
.modal-header {
  display: flex; align-items: center; gap: 8px;
  padding: 14px 16px 8px; font-weight: 600; font-size: 15px;
}
.modal-title { flex: 1; }
.close { background: none; border: none; color: var(--text-tertiary); cursor: pointer; padding: 2px; }
.close:hover { color: var(--text); }
.modal-body { padding: 4px 16px 8px; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; }
.name-input {
  width: 100%; padding: 9px 12px; font-size: 14px; color: var(--text);
  background: var(--bg-layout); border: 1px solid var(--border-secondary); border-radius: 10px;
}
.name-input:focus { outline: none; border-color: var(--blue-6); box-shadow: 0 0 0 2px var(--focus-ring); }
.section-label { margin-top: 6px; font-size: 12px; color: var(--text-tertiary); }
.folder-row {
  display: flex; align-items: center; gap: 8px;
  padding: 7px 10px; border: 1px solid var(--border-secondary); border-radius: 10px;
  font-size: 12px;
}
.folder-path { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; direction: rtl; text-align: left; }
.row-btn { background: none; border: none; color: var(--text-tertiary); cursor: pointer; padding: 2px; }
.row-btn:hover, .row-btn.active { color: var(--blue-6); }
.add-folder {
  display: flex; flex-direction: column; align-items: center; gap: 4px;
  padding: 18px; border: 1px dashed var(--border-secondary); border-radius: 10px;
  background: none; color: var(--text-tertiary); font-size: 12px; cursor: pointer;
}
.add-folder:hover { border-color: var(--blue-6); color: var(--text); }
.hint { font-size: 11px; color: var(--text-tertiary); }
.modal-footer { display: flex; justify-content: flex-end; gap: 8px; padding: 10px 16px 14px; }
.btn {
  padding: 7px 16px; border-radius: 10px; font-size: 13px; cursor: pointer;
  background: var(--bg-container); border: 1px solid var(--border-secondary); color: var(--text);
}
.btn.primary { background: var(--blue-6); border-color: var(--blue-6); color: #fff; }
.btn:disabled { opacity: 0.5; cursor: default; }
</style>

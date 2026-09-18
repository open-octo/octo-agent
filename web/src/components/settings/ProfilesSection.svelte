<script lang="ts">
  import { onMount } from 'svelte'
  import * as api from '../../lib/api'
  import { t } from '../../lib/i18n'
  import { showToast } from '../../lib/stores'

  // Profiles are data roots (~/.octo, ~/.octo-<name>), not per-profile data:
  // this backend runs under exactly one of them, so the panel can list, create
  // and delete roots but never switch — switching means restarting the backend
  // (the desktop tray menu does that; a plain `octo serve` needs a new launch).

  let profiles = $state<api.ProfileInfo[]>([])
  let current = $state('')
  let loading = $state(true)
  let newName = $state('')
  let creating = $state(false)
  // Deleting is final and the root can hold a lot, so the confirmation is a
  // typed name rather than a yes/no dialog.
  let deleting = $state<api.ProfileInfo | null>(null)
  let confirmText = $state('')
  let removing = $state(false)

  const NAME_RE = /^[A-Za-z0-9][A-Za-z0-9_-]*$/
  const nameValid = $derived(NAME_RE.test(newName.trim()))
  const confirmMatches = $derived(deleting !== null && confirmText.trim() === deleting.name)

  onMount(reload)

  async function reload() {
    loading = true
    try {
      const res = await api.listProfiles()
      profiles = res.profiles
      current = res.current
    } catch (e: any) {
      showToast(e.message ?? 'Failed to load profiles', 'error')
    } finally {
      loading = false
    }
  }

  async function create() {
    const name = newName.trim()
    if (!nameValid || creating) return
    creating = true
    try {
      await api.createProfile(name)
      newName = ''
      showToast($t('settings.profiles.created').replace('{name}', name), 'success')
      await reload()
    } catch (e: any) {
      showToast(e.message ?? 'Failed to create profile', 'error')
    } finally {
      creating = false
    }
  }

  function startDelete(p: api.ProfileInfo) {
    deleting = p
    confirmText = ''
  }

  async function confirmDelete() {
    if (!deleting || !confirmMatches || removing) return
    const name = deleting.name
    removing = true
    try {
      await api.deleteProfile(name)
      deleting = null
      showToast($t('settings.profiles.deleted').replace('{name}', name), 'success')
      await reload()
    } catch (e: any) {
      showToast(e.message ?? 'Failed to delete profile', 'error')
    } finally {
      removing = false
    }
  }

  function label(p: api.ProfileInfo): string {
    return p.name === '' ? $t('settings.profiles.default') : p.name
  }

  function fmtSize(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
    if (bytes < 1024 * 1024 * 1024) return `${(bytes / (1024 * 1024)).toFixed(1)} MB`
    return `${(bytes / (1024 * 1024 * 1024)).toFixed(2)} GB`
  }

  // The default root and the one this backend runs under are refused by the
  // server too; hiding the button just avoids a click that can only fail.
  function deletable(p: api.ProfileInfo): boolean {
    return p.name !== '' && !p.current && !p.running
  }
</script>

<div class="profiles">
  <p class="hint">{$t('settings.profiles.hint')}</p>

  {#if loading && profiles.length === 0}
    <div class="empty">{$t('common.loading')}</div>
  {:else}
    <div class="list">
      {#each profiles as p (p.name)}
        <div class="row" class:is-current={p.current}>
          <div class="info">
            <div class="head">
              <span class="name" class:mono={p.name !== ''}>{label(p)}</span>
              {#if p.current}<span class="tag tag-current">{$t('settings.profiles.current')}</span>{/if}
              {#if p.running}<span class="tag tag-running">{$t('settings.profiles.running')}{#if p.pid} · pid {p.pid}{/if}</span>{/if}
            </div>
            <span class="meta mono" title={p.path}>{p.path} · {fmtSize(p.size_bytes)}</span>
          </div>
          {#if deletable(p)}
            <button class="btns danger" onclick={() => startDelete(p)}>{$t('common.delete')}</button>
          {/if}
        </div>
      {/each}
    </div>
  {/if}

  <div class="create">
    <input
      class="sinput mono"
      type="text"
      placeholder={$t('settings.profiles.new_placeholder')}
      bind:value={newName}
      onkeydown={(e) => { if (e.key === 'Enter') create() }}
    />
    <button class="btns" disabled={!nameValid || creating} onclick={create}>{$t('settings.profiles.create')}</button>
  </div>
  {#if newName.trim() && !nameValid}
    <div class="warn">{$t('settings.profiles.name_rule')}</div>
  {/if}
  <p class="hint small">{$t('settings.profiles.use_hint')}</p>
</div>

{#if deleting}
  <div class="backdrop" role="presentation" onclick={() => (deleting = null)}>
    <div class="dialog" role="dialog" aria-modal="true" onclick={(e) => e.stopPropagation()}>
      <div class="dialog-title">{$t('settings.profiles.delete_title').replace('{name}', deleting.name)}</div>
      <p class="dialog-body">{$t('settings.profiles.delete_body')}</p>
      <p class="dialog-body mono small">{deleting.path}</p>
      <p class="dialog-body">{$t('settings.profiles.delete_confirm').replace('{name}', deleting.name)}</p>
      <!-- svelte-ignore a11y_autofocus -->
      <input
        class="sinput mono"
        type="text"
        bind:value={confirmText}
        autofocus
        onkeydown={(e) => { if (e.key === 'Enter') confirmDelete(); if (e.key === 'Escape') deleting = null }}
      />
      <div class="dialog-actions">
        <button class="btns" onclick={() => (deleting = null)}>{$t('common.cancel')}</button>
        <button class="btns danger" disabled={!confirmMatches || removing} onclick={confirmDelete}>{$t('settings.profiles.delete_forever')}</button>
      </div>
    </div>
  </div>
{/if}

<style>
.profiles { display: flex; flex-direction: column; gap: 10px; }
.hint { margin: 0; font-size: 12px; color: var(--text-tertiary); line-height: 1.5; }
.hint.small { font-size: 11px; }
.empty { padding: 24px; text-align: center; font-size: 12px; color: var(--text-tertiary); }
.list { display: flex; flex-direction: column; gap: 6px; }
.row {
  display: flex; align-items: center; gap: 12px;
  padding: 10px 12px; border: 1px solid var(--border-secondary); border-radius: 10px;
}
.row.is-current { border-color: var(--blue-6); }
.info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 3px; }
.head { display: flex; align-items: center; gap: 6px; }
.name { font-size: 13px; font-weight: 600; color: var(--text); }
.meta { font-size: 11px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.tag {
  font-size: 10px; padding: 1px 6px; border-radius: 999px; line-height: 1.5;
  border: 1px solid var(--border-secondary); color: var(--text-secondary);
}
.tag-current { border-color: var(--blue-6); color: var(--blue-6); }
.tag-running { border-color: var(--success-border); color: var(--success-text); }
.create { display: flex; gap: 8px; align-items: center; }
.create .sinput { flex: 1; }
.warn { font-size: 11px; color: var(--error-text); }
.btns {
  padding: 5px 12px; border-radius: 8px; font-size: 12px; cursor: pointer;
  background: var(--bg-container); border: 1px solid var(--border-secondary); color: var(--text);
  white-space: nowrap;
}
.btns:hover { border-color: var(--blue-6); }
.btns:disabled { opacity: 0.5; cursor: default; }
.btns.danger { color: var(--error-text); }
.btns.danger:hover:not(:disabled) { border-color: var(--error-border); }
.sinput {
  padding: 7px 10px; font-size: 13px; color: var(--text);
  background: var(--bg-layout); border: 1px solid var(--border-secondary); border-radius: 8px;
  width: 100%;
}
.sinput:focus { outline: none; border-color: var(--blue-6); }
.backdrop {
  position: fixed; inset: 0; z-index: 1100; background: var(--scrim);
  display: flex; align-items: center; justify-content: center; padding: 24px;
}
.dialog {
  width: min(440px, 100%); background: var(--bg-container); border: 1px solid var(--border-secondary);
  border-radius: 14px; padding: 18px; display: flex; flex-direction: column; gap: 10px;
}
.dialog-title { font-size: 15px; font-weight: 600; color: var(--text); }
.dialog-body { margin: 0; font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.dialog-body.small { font-size: 11px; word-break: break-all; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 4px; }
</style>

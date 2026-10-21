<script lang="ts">
  // Bottom sheet for the open chat's model and permission mode, opened from
  // the chat header. Each pick applies at once, like the desktop composer's
  // menus; the next turn runs with it.
  import { get } from 'svelte/store'
  import { sessions, chatPermMode, globalPermissionMode, showToast } from '../lib/stores'
  import * as api from '../lib/api'
  import { t, tr } from '../lib/i18n'
  import { loadModelOptions, normalizePermMode, PERM_MODES, type ModelOption } from './sessionOptions'

  let { sessionId, onClose }: { sessionId: string; onClose: () => void } = $props()

  const session = $derived($sessions.find(s => s.id === sessionId) ?? null)

  let models = $state<ModelOption[]>([])
  loadModelOptions().then(m => (models = m)).catch(() => {})
  // '' is the server default; a bound id the list no longer carries (a model
  // removed from its endpoint) still shows as the current choice.
  const modelId = $derived(session?.model_id ?? '')
  const options = $derived(modelId && !models.some(m => m.id === modelId)
    ? [...models, { id: modelId, label: session?.model || modelId }]
    : models)

  const permMode = $derived(normalizePermMode(
    $chatPermMode[sessionId] || session?.permission_mode,
    normalizePermMode(get(globalPermissionMode)),
  ))
  const permNote = $derived(PERM_MODES.find(p => p.m === permMode)?.note ?? 'm.perm_note_ask')

  let busy = $state(false)

  let modelSelect: HTMLSelectElement | undefined = $state()

  async function pickModel(id: string) {
    if (busy || id === modelId) return
    busy = true
    try {
      const res = await api.updateSessionModel(sessionId, id || 'default')
      sessions.update(list => list.map(s => (s.id === sessionId ? { ...s, model: res.model, model_id: id } : s)))
    } catch (e: any) {
      showToast(e?.message ?? tr('m.model_fail'), 'error')
      // The select is one-way bound to modelId, which never changed; put the
      // control back on it.
      if (modelSelect) modelSelect.value = modelId
    } finally {
      busy = false
    }
  }

  async function pickPerm(next: string) {
    if (busy || next === permMode) return
    busy = true
    try {
      await api.updateSessionPermissionMode(sessionId, next)
      chatPermMode.update(m => ({ ...m, [sessionId]: next }))
      sessions.update(list => list.map(s => (s.id === sessionId ? { ...s, permission_mode: next } : s)))
    } catch (e: any) {
      showToast(e?.message ?? tr('m.perm_fail'), 'error')
    } finally {
      busy = false
    }
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') onClose()
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="backdrop" role="presentation" onclick={onClose}></div>
<div class="sheet" role="dialog" aria-modal="true" aria-label={session?.title || session?.name || $t('m.session')}>
  <p class="lbl">{$t('m.model')}</p>
  <select class="select" bind:this={modelSelect} value={modelId} disabled={busy} aria-label={$t('m.model')} onchange={(e) => pickModel((e.currentTarget as HTMLSelectElement).value)}>
    <option value="">{$t('m.default')}</option>
    {#each options as m (m.id)}
      <option value={m.id}>{m.label}</option>
    {/each}
  </select>

  <p class="lbl">{$t('m.perm_mode')}</p>
  <div class="seg">
    {#each PERM_MODES as p (p.m)}
      <button class="segi" class:on={permMode === p.m} aria-pressed={permMode === p.m} disabled={busy} onclick={() => pickPerm(p.m)}>{$t(p.key)}</button>
    {/each}
  </div>
  <p class="note">{$t(permNote)}</p>
</div>

<style>
  .backdrop {
    position: fixed;
    inset: 0;
    z-index: 2000;
    background: rgba(0, 0, 0, .35);
  }
  .sheet {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 2001;
    display: flex;
    flex-direction: column;
    padding: 6px 16px calc(18px + env(safe-area-inset-bottom));
    background: var(--m-surface);
    border-radius: 16px 16px 0 0;
  }
  .lbl {
    margin: 14px 2px 8px;
    font-size: 12px;
    font-weight: 600;
    letter-spacing: .04em;
    text-transform: uppercase;
    color: var(--m-text-3);
  }
  .select {
    width: 100%;
    padding: 10px 12px;
    border: 1px solid var(--m-border);
    border-radius: 10px;
    background: var(--m-bg);
    color: var(--m-text);
    font: inherit;
    font-size: 16px;
  }
  .seg { display: flex; gap: 4px; background: var(--m-bg); border-radius: 8px; padding: 3px; }
  .segi {
    flex: 1; text-align: center; padding: 8px 0; border-radius: 6px; border: none;
    background: none; font-size: 13px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .segi.on { background: var(--m-accent); color: #fff; font-weight: 600; }
  .note { margin: 10px 2px 0; font-size: 12px; color: var(--m-text-3); }
</style>

<script lang="ts">
  import { onMount } from 'svelte'
  import { t, tr } from '../../lib/i18n'
  import * as api from '../../lib/api'
  import type { EndpointConfig, ProviderPreset } from '../../lib/api'
  import { showToast, openAgentSession, settingsModalOpen } from '../../lib/stores'
  import { confirmDialog } from '../../lib/confirm'
  import StatusTag from '../ui/StatusTag.svelte'
  import ApiKeyInput from './ApiKeyInput.svelte'
  import VariantChips from './VariantChips.svelte'

  // Settings → Endpoints: direct CRUD over /api/config/endpoints. The whole
  // tab lives here (list + inline create/edit form + per-model chip actions);
  // SettingsModal just mounts it. A secondary "edit with agent" entry keeps
  // the conversational path for anything the form doesn't cover.

  let endpoints  = $state<EndpointConfig[]>([])
  let defaultCid = $state('')
  let liteCid    = $state('')
  let visionCid  = $state('')
  let providers  = $state<ProviderPreset[]>([])
  let loading    = $state(true)
  let busy       = $state(false)

  let view    = $state<'list' | 'form'>('list')
  // The endpoint being edited; null while creating.
  let editing = $state<EndpointConfig | null>(null)

  // ── form fields ──
  let fId       = $state('')
  let fName     = $state('')
  let fProvider = $state('')
  let fBaseUrl  = $state('')
  let fApiKey   = $state('')
  let fProtocol = $state<'openai' | 'anthropic'>('openai')
  let fModel    = $state('')  // create only: optional first model
  let fVision   = $state(true)
  // Raw JSON text for the custom-headers textarea; parsed on submit. Unlike
  // fApiKey, this is pre-filled on edit (headers aren't masked like the API
  // key — see design doc "兼容性" for the tradeoff).
  let fHeadersText = $state('')
  // Once the user touches the id field, stop regenerating it from provider.
  let fIdTouched = $state(false)

  // ── inline add-model row (one endpoint at a time) ──
  let addingFor      = $state<string | null>(null)
  let newModel       = $state('')
  let newModelVision = $state(true)

  // ── popover menu: the catalogue picker behind "+ Add model". Model-level
  // actions (default/Lite/vision helper) are inline icon toggles on each
  // chip — they used to hide behind a chip-click dropdown, which nobody
  // discovered. ──
  type Menu = { kind: 'add'; epId: string }
  let menu = $state<Menu | null>(null)
  // The picker is portaled to <body> and positioned from the button's rect:
  // the settings pane scrolls and the modal clips its own overflow, so an
  // absolutely positioned menu was cut off at the modal's bottom edge rather
  // than floating over it.
  let menuPos = $state({ top: 0, bottom: 0, left: 0 })
  function portalMenu(node: HTMLElement) {
    document.body.appendChild(node)
    // Downward when there is room, upward when there isn't, and capped to
    // whichever side it lands on — a long catalogue would otherwise run off
    // the screen in either direction. It scrolls internally past that.
    const below = window.innerHeight - menuPos.bottom - 12
    const above = menuPos.top - 12
    if (node.offsetHeight > below && above > below) {
      node.style.top = ''
      node.style.bottom = `${window.innerHeight - menuPos.top + 4}px`
      node.style.maxHeight = `${above}px`
    } else {
      node.style.maxHeight = `${below}px`
    }
    // The coordinates were captured when the menu opened, so any ancestor
    // scrolling away underneath it has to close it. Scroll events don't
    // bubble — listen in the capture phase.
    const onScroll = () => { menu = null }
    window.addEventListener('scroll', onScroll, true)
    return {
      destroy() {
        window.removeEventListener('scroll', onScroll, true)
        node.remove()
      },
    }
  }

  onMount(async () => {
    try {
      providers = await api.listProviders()
    } catch {
      /* non-fatal: the form still works without presets */
    }
    await reload()
    loading = false
  })

  async function reload() {
    try {
      const ep = await api.getEndpoints()
      endpoints = ep.endpoints ?? []
      defaultCid = ep.default ?? ''
      liteCid = ep.lite ?? ''
      visionCid = ep.vision_helper ?? ''
    } catch (e: any) {
      showToast(e.message ?? 'Failed to load endpoints', 'error')
    }
  }

  const isDefaultEp = (ep: EndpointConfig) => defaultCid.startsWith(`${ep.id}::`)
  const isLiteEp    = (ep: EndpointConfig) => liteCid.startsWith(`${ep.id}::`)
  const cid         = (epId: string, model: string) => `${epId}::${model}`

  function presetFor(providerId: string): ProviderPreset | null {
    return providers.find((p) => p.id === providerId) ?? null
  }

  // ── mutations (each reloads the list so badges stay truthful) ──

  async function run(fn: () => Promise<unknown>) {
    if (busy) return
    busy = true
    try {
      await fn()
      await reload()
    } catch (e: any) {
      showToast(e.message ?? 'Request failed', 'error')
    } finally {
      busy = false
    }
  }

  function setDefault(epId: string, model?: string) {
    run(() => api.setEndpointDefault(epId, model))
  }

  function setLite(epId: string, model?: string) {
    run(() => api.setEndpointLite(epId, model))
  }

  function unsetLite(epId: string) {
    run(() => api.unsetEndpointLite(epId))
  }

  // The vision helper lets text-only models "see" images: it describes them
  // before the turn is sent. Only offered on models the endpoint marks as
  // vision-capable — the server rejects the rest.
  function setVisionHelper(epId: string, model: string) {
    run(() => api.setEndpointVisionHelper(epId, model))
  }

  function unsetVisionHelper(epId: string) {
    run(() => api.unsetEndpointVisionHelper(epId))
  }

  async function removeModel(ep: EndpointConfig, model: string) {
    const msg = tr('settings.endpoints.delete_model_confirm')
      .replace('{model}', model)
      .replace('{id}', ep.id)
    if (!(await confirmDialog(msg))) return
    run(() => api.deleteEndpointModel(ep.id, model))
  }

  async function removeEndpoint(ep: EndpointConfig) {
    const msg = tr('settings.endpoints.delete_confirm').replace('{id}', ep.id)
    if (!(await confirmDialog(msg))) return
    run(() => api.deleteEndpoint(ep.id))
  }

  // ── add model ──

  // Catalogue models of the endpoint's provider not yet added to it — the
  // one-click options behind "+ Add model" for a known (preset) provider.
  function addableModels(ep: EndpointConfig): string[] {
    const catalogue = presetFor(ep.provider)?.models ?? []
    return catalogue.filter((m) => !ep.models.some((x) => x.model === m))
  }

  // "+ Add model": a known provider gets a catalogue picker menu (with a
  // manual-entry escape hatch); a provider without a catalogue goes straight
  // to the free-text row.
  function startAddModel(ep: EndpointConfig, btn: HTMLElement) {
    if (addableModels(ep).length > 0) {
      if (menu?.kind === 'add' && menu.epId === ep.id) {
        menu = null
        return
      }
      const r = btn.getBoundingClientRect()
      menuPos = { top: r.top, bottom: r.bottom, left: r.left }
      menu = { kind: 'add', epId: ep.id }
      return
    }
    openAddModelInput(ep)
  }

  function openAddModelInput(ep: EndpointConfig) {
    addingFor = ep.id
    newModel = ''
    newModelVision = true
    menu = null
  }

  function addCatalogueModel(ep: EndpointConfig, model: string) {
    const vision = presetFor(ep.provider)?.model_vision?.[model] ?? true
    menu = null
    run(() => api.addEndpointModel(ep.id, model, vision))
  }

  // Pre-fill the vision flag from the provider catalogue while typing; an
  // unknown model keeps whatever the user last chose.
  function onNewModelInput(ep: EndpointConfig) {
    const mv = presetFor(ep.provider)?.model_vision
    if (mv && Object.hasOwn(mv, newModel)) newModelVision = mv[newModel]
  }

  function submitAddModel(ep: EndpointConfig) {
    const model = newModel.trim()
    if (!model) return
    run(async () => {
      await api.addEndpointModel(ep.id, model, newModelVision)
      addingFor = null
    })
  }

  // ── create / edit form ──

  function openCreate() {
    editing = null
    fId = ''
    fName = ''
    fProvider = providers[0]?.id ?? 'custom'
    fBaseUrl = ''
    fApiKey = ''
    fProtocol = 'openai'
    fHeadersText = ''
    fIdTouched = false
    applyProviderPreset()
    view = 'form'
  }

  function openEdit(ep: EndpointConfig) {
    editing = ep
    fId = ep.id
    fName = ep.name ?? ''
    fProvider = ep.provider
    fBaseUrl = ep.base_url ?? ''
    fApiKey = ''
    fProtocol = ep.protocol === 'anthropic' ? 'anthropic' : 'openai'
    fHeadersText = ep.headers && Object.keys(ep.headers).length > 0 ? JSON.stringify(ep.headers, null, 2) : ''
    fIdTouched = true
    view = 'form'
  }

  function applyProviderPreset() {
    const p = presetFor(fProvider)
    if (!editing) {
      // Named vendors resolve their base_url at runtime — keep it empty and
      // let the placeholder show the default. Seed the first model from the
      // catalogue so a fresh endpoint is usable right after saving.
      fBaseUrl = ''
      fModel = p?.default_model ?? ''
      fVision = p?.model_vision?.[fModel] ?? true
      if (!fIdTouched) fId = api.freshEndpointID(fProvider, endpoints)
    }
  }

  let formPreset   = $derived(presetFor(fProvider))
  let formVariants = $derived(formPreset?.endpoint_variants ?? [])
  let isCustom     = $derived(fProvider === 'custom' || !!formPreset?.custom_endpoint)

  async function submitForm() {
    const id = fId.trim()
    if (!id || !fProvider) return

    // Parse the headers textarea up front — invalid JSON blocks the save
    // entirely (for either create or update) rather than silently dropping
    // the field or saving a partial config.
    const headersText = fHeadersText.trim()
    let parsedHeaders: Record<string, string> | undefined
    if (headersText) {
      try {
        parsedHeaders = JSON.parse(headersText)
      } catch {
        showToast($t('models.headers_invalid_json'), 'error')
        return
      }
    }

    busy = true
    try {
      if (!editing) {
        const model = fModel.trim()
        await api.createEndpoint({
          id,
          name: fName.trim() || undefined,
          provider: fProvider,
          base_url: fBaseUrl.trim() || undefined,
          api_key: fApiKey || undefined,
          protocol: isCustom ? fProtocol : undefined,
          headers: parsedHeaders,
          models: model ? [{ model, vision: fVision }] : [],
        })
        // First usable endpoint on a config with no default yet — point the
        // default at it so the save is immediately effective. Non-fatal: the
        // endpoint is already created, so a failure here must not strand the
        // user on the form (retrying would hit an id conflict).
        if (!defaultCid && model) await api.setEndpointDefault(id).catch(() => {})
      } else {
        const patch: api.EndpointUpdateInput = {}
        if (id !== editing.id) patch.new_id = id
        if (fName.trim() && fName.trim() !== (editing.name ?? '')) patch.name = fName.trim()
        if (fProvider !== editing.provider) patch.provider = fProvider
        if (fBaseUrl.trim() && fBaseUrl.trim() !== (editing.base_url ?? '')) patch.base_url = fBaseUrl.trim()
        if (fApiKey) patch.api_key = fApiKey
        // Normalise the stored protocol the same way openEdit seeds the select
        // (absent = openai), so an untouched select never produces a patch.
        const prevProtocol = editing.protocol === 'anthropic' ? 'anthropic' : 'openai'
        if (isCustom && fProtocol !== prevProtocol) patch.protocol = fProtocol
        // headers is a full-replacement patch (server: nil = unchanged, {} =
        // clear all). Only include it when the textarea's parsed value
        // actually differs from what was loaded — an untouched textarea must
        // never send "headers: {}" and silently wipe existing headers.
        const currentCanonical = JSON.stringify(editing.headers ?? {})
        const newCanonical = JSON.stringify(parsedHeaders ?? {})
        if (newCanonical !== currentCanonical) patch.headers = parsedHeaders ?? {}
        if (Object.keys(patch).length > 0) await api.updateEndpoint(editing.id, patch)
      }
      await reload()
      view = 'list'
    } catch (e: any) {
      showToast(e.message ?? 'Save failed', 'error')
    } finally {
      busy = false
    }
  }

  function configureWithAgent() {
    settingsModalOpen.set(false)
    openAgentSession('/config-setup', tr('settings.session_configure_endpoints'))
  }
</script>

<!--
  SettingsModal's .modal stops click propagation to window (so clicking
  inside the modal doesn't bubble out and close it as a backdrop click),
  which means a `<svelte:window onclick>` here never fires. Closing the
  popover menu on outside clicks has to happen on this component's own
  root instead, since toggle buttons and menu items already stopPropagation
  or clear `menu` themselves.
-->
<div class="ep-section" onclick={() => (menu = null)}>
{#if loading}
  <div class="ep-loading">{$t('settings.loading')}</div>
{:else if view === 'form'}
  <div class="form">
    <div class="form-title">
      {editing ? $t('settings.endpoints.form.title.edit') : $t('settings.endpoints.configure')}
    </div>

    <label class="field">
      <span class="field-label">{$t('models.provider')}</span>
      <select class="field-input" bind:value={fProvider} onchange={applyProviderPreset} disabled={busy}>
        {#each providers as p (p.id)}
          <option value={p.id}>{p.name}</option>
        {/each}
        {#if !providers.some((p) => p.id === 'custom')}
          <option value="custom">Custom</option>
        {/if}
      </select>
    </label>

    <label class="field">
      <span class="field-label">{$t('settings.endpoints.form.id')}</span>
      <input
        class="field-input mono"
        type="text"
        bind:value={fId}
        oninput={() => (fIdTouched = true)}
        disabled={busy}
      />
    </label>

    <label class="field">
      <span class="field-label">{$t('settings.endpoints.form.name')}</span>
      <input class="field-input" type="text" bind:value={fName} disabled={busy} />
    </label>

    <label class="field">
      <span class="field-label">{$t('models.baseurl')}</span>
      <input
        class="field-input mono"
        type="text"
        placeholder={formPreset?.base_url ?? ''}
        bind:value={fBaseUrl}
        disabled={busy}
      />
      <VariantChips variants={formVariants} value={fBaseUrl} disabled={busy} onselect={(v) => (fBaseUrl = v.base_url)} />
    </label>

    <label class="field">
      <span class="field-label">{$t('models.apikey')}</span>
      <ApiKeyInput
        bind:value={fApiKey}
        placeholder={editing?.has_api_key ? $t('settings.endpoints.form.key_keep') : ''}
        disabled={busy}
      />
    </label>

    <label class="field">
      <span class="field-label">{$t('models.headers')}</span>
      <textarea
        class="field-input mono headers-textarea"
        rows={4}
        placeholder={$t('models.headers.placeholder')}
        bind:value={fHeadersText}
        disabled={busy}
      ></textarea>
    </label>

    {#if isCustom}
      <label class="field">
        <span class="field-label">{$t('models.protocol')}</span>
        <select class="field-input" bind:value={fProtocol} disabled={busy}>
          <option value="openai">{$t('models.protocol.openai')}</option>
          <option value="anthropic">{$t('models.protocol.anthropic')}</option>
        </select>
      </label>
    {/if}

    {#if !editing}
      <div class="field">
        <span class="field-label">{$t('settings.endpoints.form.initial_model')}</span>
        <input
          class="field-input mono"
          type="text"
          bind:value={fModel}
          oninput={() => { const mv = formPreset?.model_vision; if (mv && Object.hasOwn(mv, fModel)) fVision = mv[fModel] }}
          list="ep-form-models"
          disabled={busy}
        />
        <datalist id="ep-form-models">
          {#each formPreset?.models ?? [] as m (m)}
            <option value={m}></option>
          {/each}
        </datalist>
        <label class="vision-check">
          <input type="checkbox" bind:checked={fVision} disabled={busy} />
          <span>{$t('settings.endpoints.models.vision')}</span>
        </label>
      </div>
    {/if}

    <div class="form-actions">
      <button class="btns" onclick={() => (view = 'list')} disabled={busy}>{$t('common.cancel')}</button>
      <button class="btnp" onclick={submitForm} disabled={busy || !fId.trim() || !fProvider}>{$t('common.save')}</button>
    </div>
  </div>
{:else}
  <div class="ep-head">
    <span class="ep-head-label">{$t('settings.endpoints.list_label')}</span>
    <div class="ep-head-actions">
      <button class="btns" onclick={configureWithAgent}>
        <iconify-icon icon="ant-design:message-outlined" width="13"></iconify-icon>
        {$t('settings.endpoints.configure_with_agent')}
      </button>
      <button class="btnp" onclick={openCreate}>
        <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
        {$t('settings.endpoints.configure')}
      </button>
    </div>
  </div>

  {#if endpoints.length === 0}
    <div class="ep-empty">{$t('settings.endpoints.empty')}</div>
  {:else}
    <div class="ep-list">
      {#each endpoints as ep (ep.id)}
        <div class="ep-card">
          <div class="ep-title-line">
            <span class="ep-id mono">{ep.id}</span>
            {#if ep.name}<span class="ep-name">{ep.name}</span>{/if}
            {#if isDefaultEp(ep)}
              <StatusTag status="success">{$t('settings.endpoints.badge.default')}</StatusTag>
            {/if}
            {#if isLiteEp(ep)}
              <StatusTag status="info">{$t('settings.endpoints.badge.lite')}</StatusTag>
            {/if}
          </div>

          <div class="ep-meta mono">
            <span>{ep.provider}</span>
            {#if ep.base_url || presetFor(ep.provider)?.base_url}
              <span> · {ep.base_url || presetFor(ep.provider)?.base_url}</span>
            {/if}
            {#if ep.protocol}<span> · {ep.protocol}</span>{/if}
            <span> · {$t('settings.endpoints.api_key')}:
              {#if ep.has_api_key}<span class="key-set">{$t('settings.endpoints.api_key.set')}</span>
              {:else if presetFor(ep.provider)?.custom_endpoint}<span class="key-optional">{$t('settings.endpoints.api_key.optional')}</span>
              {:else}<span class="key-missing">{$t('settings.endpoints.api_key.missing')}</span>{/if}
            </span>
          </div>

          <div class="ep-chips">
            {#each ep.models as m (m.model)}
              {@const isDef = defaultCid === cid(ep.id, m.model)}
              {@const isLit = liteCid === cid(ep.id, m.model)}
              {@const isVis = visionCid === cid(ep.id, m.model)}
              <!-- Model-level actions are always-visible icon toggles: the
                   colored (active) icon doubles as the state badge, so the
                   chip reads its roles at a glance and one click toggles. -->
              <div class="chip" class:chip-default={isDef} class:chip-lite={isLit} class:chip-vision={isVis}>
                <span class="chip-label mono">{m.model}</span>
                <button
                  class="chip-act"
                  class:on-default={isDef}
                  title={$t(isDef ? 'settings.endpoints.badge.default' : 'settings.endpoints.set_default')}
                  onclick={() => { if (!isDef) setDefault(ep.id, m.model) }}
                  disabled={busy || isDef}
                >
                  <iconify-icon icon={isDef ? 'ant-design:star-filled' : 'ant-design:star-outlined'} width="13"></iconify-icon>
                </button>
                <button
                  class="chip-act"
                  class:on-lite={isLit}
                  title={$t(isLit ? 'settings.endpoints.unset_lite' : 'settings.endpoints.set_lite')}
                  onclick={() => (isLit ? unsetLite(ep.id) : setLite(ep.id, m.model))}
                  disabled={busy}
                >
                  <iconify-icon icon={isLit ? 'ant-design:thunderbolt-filled' : 'ant-design:thunderbolt-outlined'} width="13"></iconify-icon>
                </button>
                {#if m.vision || isVis}
                  <button
                    class="chip-act"
                    class:on-vision={isVis}
                    title={$t(isVis ? 'settings.endpoints.unset_vision_helper' : 'settings.endpoints.set_vision_helper')}
                    onclick={() => (isVis ? unsetVisionHelper(ep.id) : setVisionHelper(ep.id, m.model))}
                    disabled={busy}
                  >
                    <iconify-icon icon={isVis ? 'ant-design:eye-filled' : 'ant-design:eye-outlined'} width="13"></iconify-icon>
                  </button>
                {/if}
                <button
                  class="chip-x"
                  title={$t('common.delete')}
                  onclick={(e) => { e.stopPropagation(); removeModel(ep, m.model) }}
                  disabled={busy}
                >
                  <iconify-icon icon="ant-design:close-outlined" width="11"></iconify-icon>
                </button>
              </div>
            {/each}

            {#if addingFor === ep.id}
              <div class="add-row">
                <input
                  class="add-input mono"
                  type="text"
                  placeholder={$t('settings.endpoints.add_model.placeholder')}
                  bind:value={newModel}
                  oninput={() => onNewModelInput(ep)}
                  onkeydown={(e) => {
                    if (e.key === 'Enter') submitAddModel(ep)
                    // stopPropagation: SettingsModal closes on Escape; here it
                    // only means "cancel this row".
                    if (e.key === 'Escape') { e.stopPropagation(); addingFor = null }
                  }}
                  list={`ep-models-${ep.id}`}
                  disabled={busy}
                />
                <datalist id={`ep-models-${ep.id}`}>
                  {#each presetFor(ep.provider)?.models ?? [] as pm (pm)}
                    <option value={pm}></option>
                  {/each}
                </datalist>
                <label class="vision-check">
                  <input type="checkbox" bind:checked={newModelVision} disabled={busy} />
                  <span>{$t('settings.endpoints.models.vision')}</span>
                </label>
                <button class="add-ok" onclick={() => submitAddModel(ep)} disabled={busy || !newModel.trim()}>
                  <iconify-icon icon="ant-design:check-outlined" width="13"></iconify-icon>
                </button>
                <button class="add-cancel" onclick={() => (addingFor = null)} disabled={busy}>
                  <iconify-icon icon="ant-design:close-outlined" width="13"></iconify-icon>
                </button>
              </div>
            {:else}
              <div class="chip-wrap">
                <button class="chip chip-add" onclick={(e) => { e.stopPropagation(); startAddModel(ep, e.currentTarget as HTMLElement) }} disabled={busy}>
                  <iconify-icon icon="ant-design:plus-outlined" width="12"></iconify-icon>
                  {$t('settings.endpoints.add_model')}
                </button>
                {#if menu?.kind === 'add' && menu.epId === ep.id}
                  <div class="menu" use:portalMenu style="top:{menuPos.bottom + 4}px;left:{menuPos.left}px">
                    {#each addableModels(ep) as m (m)}
                      <button class="menu-item mono" onclick={() => addCatalogueModel(ep, m)} disabled={busy}>{m}</button>
                    {/each}
                    <div class="menu-sep"></div>
                    <button class="menu-item" onclick={(e) => { e.stopPropagation(); openAddModelInput(ep) }} disabled={busy}>
                      {$t('settings.endpoints.add_model.custom')}
                    </button>
                  </div>
                {/if}
              </div>
            {/if}
          </div>

          <!-- Endpoint-level actions only. Model-level ones (default / Lite /
               vision helper) live on each chip as inline icon toggles — the
               footer duplicates existed because those used to hide behind a
               chip-click dropdown. -->
          <div class="ep-actions">
            <button class="act" onclick={() => openEdit(ep)} disabled={busy}>{$t('common.edit')}</button>
            <button class="act danger" onclick={() => removeEndpoint(ep)} disabled={busy}>{$t('common.delete')}</button>
          </div>
        </div>
      {/each}
    </div>
  {/if}
{/if}
</div>

<style>
.ep-section { display: contents; }
.ep-loading { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }

/* ── list head ── */
.ep-head { display: flex; align-items: center; justify-content: space-between; padding-bottom: 14px; }
.ep-head-label { font-size: 13px; color: var(--text-secondary); }
.ep-head-actions { display: flex; align-items: center; gap: 8px; }

.ep-empty { padding: 28px 16px; text-align: center; font-size: 13px; color: var(--text-tertiary); }

/* ── cards ── */
.ep-list { display: flex; flex-direction: column; gap: 14px; }
.ep-card {
  background: var(--bg-container); border: 1px solid var(--border);
  border-radius: var(--radius-card); padding: 16px 18px;
  display: flex; flex-direction: column; gap: 8px;
}
.ep-title-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.ep-id { font-size: 15px; font-weight: 700; color: var(--text-heading); }
.ep-name { font-size: 13px; color: var(--text-secondary); }
.ep-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-wrap: wrap; word-break: break-all; }
.key-set { color: var(--success-text); }
.key-missing { color: var(--error); }
/* A keyless Custom endpoint (local Ollama/vLLM) is complete, not missing. */
.key-optional { color: var(--text-tertiary); }

/* ── model chips ── */
.ep-chips { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; padding: 2px 0; }
.chip-wrap { position: relative; }
.chip {
  display: inline-flex; align-items: center; gap: 2px; height: 28px;
  border: 1px solid var(--border); border-radius: 8px; background: var(--bg-layout);
  overflow: hidden;
}
.chip-default { border-color: var(--success-text); }
.chip-lite { border-color: var(--blue-5); }
.chip-vision { border-color: var(--purple-5, var(--blue-5)); }
.chip-label {
  height: 100%; padding: 0 4px 0 10px;
  font-size: 12.5px; color: var(--text);
  display: inline-flex; align-items: center;
}
/* Inline action toggles: grey when inactive, role-colored when active (the
   colored icon IS the state badge). */
.chip-act {
  height: 100%; padding: 0 4px; border: none; background: transparent;
  color: var(--text-quaternary); cursor: pointer;
  display: inline-flex; align-items: center;
}
.chip-act:hover:not(:disabled) { color: var(--text-secondary); }
.chip-act:disabled { cursor: default; }
.chip-act.on-default, .chip-act.on-default:disabled { color: var(--success-text); }
.chip-act.on-lite { color: var(--blue-6); }
.chip-act.on-vision { color: var(--purple-6, var(--blue-6)); }
.chip-x {
  height: 100%; padding: 0 8px 0 4px; border: none; background: transparent;
  color: var(--text-quaternary); cursor: pointer; display: inline-flex; align-items: center;
}
.chip-x:hover:not(:disabled) { color: var(--error); }
.chip-add {
  height: 28px; padding: 0 12px; cursor: pointer; background: var(--bg-container);
  border-style: dashed; color: var(--text-secondary); font-size: 12.5px; font-family: inherit;
  display: inline-flex; align-items: center; gap: 5px;
}
.chip-add:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }

/* ── inline add-model row ── */
.add-row { display: inline-flex; align-items: center; gap: 6px; }
.add-input {
  height: 28px; width: 200px; padding: 0 10px; border: 1px solid var(--blue-5);
  border-radius: 8px; font-size: 12.5px; color: var(--text); background: var(--bg-container); outline: none;
}
.vision-check {
  display: inline-flex; align-items: center; gap: 4px; font-size: 12px;
  color: var(--text-secondary); cursor: pointer; white-space: nowrap; margin-top: 2px;
}
.vision-check input { accent-color: var(--blue-6); }
.add-ok, .add-cancel {
  width: 28px; height: 28px; border: 1px solid var(--border); border-radius: 8px;
  background: var(--bg-container); cursor: pointer; display: inline-flex;
  align-items: center; justify-content: center; color: var(--text-secondary);
}
.add-ok:hover:not(:disabled) { color: var(--success-text); border-color: var(--success-text); }
.add-cancel:hover:not(:disabled) { color: var(--error); border-color: var(--error); }
.add-ok:disabled { opacity: 0.5; cursor: not-allowed; }

/* ── footer actions ── */
.ep-actions { display: flex; align-items: center; gap: 16px; padding-top: 2px; }
.act {
  border: none; background: transparent; padding: 0; font-size: 13px; font-weight: 500;
  color: var(--blue-6); cursor: pointer; font-family: inherit;
}
.act:hover:not(:disabled) { text-decoration: underline; }
.act:disabled { opacity: 0.5; cursor: not-allowed; }
.act.danger { color: var(--error); }

/* ── popover menu ── */
.menu {
  position: fixed; z-index: 1001; min-width: 160px; overflow-y: auto;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: 8px;
  box-shadow: 0 8px 24px rgba(15,23,42,0.14); padding: 4px; display: flex; flex-direction: column;
}
.menu-sep { height: 1px; background: var(--border-secondary); margin: 4px 2px; }
.menu-item {
  border: none; background: transparent; text-align: left; padding: 6px 8px;
  border-radius: 6px; font-size: 12.5px; color: var(--text); cursor: pointer; font-family: inherit;
}
.menu-item:hover:not(:disabled) { background: var(--hover-neutral); }
.menu-item:disabled { color: var(--text-quaternary); cursor: not-allowed; }

/* ── form ── */
.form { display: flex; flex-direction: column; gap: 14px; max-width: 480px; }
.form-title { font-size: 14px; font-weight: 600; color: var(--text-heading); }
.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { font-size: 12.5px; color: var(--text-secondary); }
.field-input {
  height: 36px; padding: 0 12px; border: 1px solid var(--border); border-radius: 8px;
  font-size: 13px; color: var(--text); background: var(--bg-container); outline: none; font-family: inherit;
}
.field-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 3px var(--active-blue-bg); }
select.field-input { cursor: pointer; }
textarea.headers-textarea { height: auto; min-height: 90px; padding: 8px 12px; resize: vertical; white-space: pre; }
.form-actions { display: flex; justify-content: flex-end; gap: 8px; padding-top: 4px; }

/* ── shared buttons (mirrors SettingsModal) ── */
.btnp {
  height: 32px; padding: 0 14px; border: none; background: var(--blue-6);
  border-radius: 8px; font-size: 13px; font-weight: 600; color: #fff; cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 6px;
  box-shadow: 0 1px 2px rgba(0,122,255,0.35);
}
.btnp:hover:not(:disabled) { background: var(--blue-5); }
.btnp:disabled { opacity: 0.5; cursor: not-allowed; }
.btns {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; font-size: 13px; font-weight: 500; color: var(--text); cursor: pointer;
  font-family: inherit; display: flex; align-items: center; gap: 6px;
}
.btns:hover:not(:disabled) { background: var(--hover-neutral); border-color: var(--text-quaternary); }
.btns:disabled { opacity: 0.5; cursor: not-allowed; }

.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
</style>

<script lang="ts">
  import { onMount } from 'svelte'
  import Segment from '../components/ui/Segment.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import ModelConfigForm from '../components/settings/ModelConfigForm.svelte'
  import ApiKeyInput from '../components/settings/ApiKeyInput.svelte'
  import VariantChips from '../components/settings/VariantChips.svelte'
  import { get } from 'svelte/store'
  import { showToast, nativeShell } from '../lib/stores'
  import { setLocale, t, tr } from '../lib/i18n'
  import { confirmDialog } from '../lib/confirm'
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import { notificationsEnabled, setNotificationsEnabled } from '../lib/notifications'
  import * as api from '../lib/api'
  import type { ModelEntry, ProviderPreset, ModelConfigInput, EndpointConfig } from '../lib/api'

  // --- local state ---
  let language      = $state('en')
  let fontSize      = $state('Medium')
  let theme         = $state('Light')
  let reasoning     = $state('Medium')
  let permMode      = $state('Ask')
  let workspaceDir  = $state('')
  let showReasoning = $state(true)
  let coauthor      = $state(true)
  let autostart     = $state(false) // desktop shell only
  let versionStr    = $state('')
  // Update state, populated by loadVersion — used by the desktop-only "Check for
  // updates" row. The desktop build reports upgrade_mode 'installer', so the row
  // links to the release page rather than swapping the binary in place.
  let latestStr     = $state('')
  let updateAvail   = $state(false)
  let downloadUrl   = $state('')
  let checkingUpdate = $state(false)
  let saving        = $state(false)
  let loading       = $state(true)
  let providersLoaded = $state(false)

  // Original values for dirty-checking
  let origLanguage     = 'en'
  let origWorkspaceDir = ''
  let origReasoning    = 'Medium'
  let origShowReasoning = true
  let origCoauthor = true
  let origPermMode     = 'Ask'
  // Index into `models` for the current default entry — Reasoning Effort and
  // Permission Mode below are that entry's own settings, saved via
  // api.updateModel (PATCH /api/config/models/{id}), same as everything in
  // the Models section below. No session is involved: "Agent Defaults" edits
  // global config, not whichever chat session happens to be open.
  let defaultModelIdx = 0

  // ── Models section (config-level entries: add/edit/delete/default/lite) ──────
  let models       = $state<ModelEntry[]>([])
  let providers    = $state<ProviderPreset[]>([])
  let modelModalOpen = $state(false)
  let editingModel = $state<ModelEntry | null>(null)
  let busyModelId  = $state<string | null>(null)
  let modelModalEl = $state<HTMLDivElement | null>(null)

  // ── Endpoints section (PR4b: two-level read-only preview; PR6: editable) ──
  // Mirrors GET /api/config/endpoints. PR6 makes the endpoint cards editable:
  // add/edit/delete endpoint, add/delete model, set default/lite, rename with
  // confirmation. The old flat "AI Models" section below is hidden ({#if false})
  // and its handlers are stubs — PR6 doesn't remove them to keep the diff
  // small, but they're dead code once the endpoint editor is live.
  let endpoints     = $state<EndpointConfig[]>([])
  let defaultCid    = $state('')
  let liteCid       = $state('')

  // Endpoint editor modal state.
  let epModalOpen   = $state(false)
  let editingEpId   = $state<string | null>(null) // null = creating new
  let epForm        = $state({
    id: '', name: '', provider: 'anthropic', base_url: '', api_key: '', protocol: '',
  })
  let busyEpId      = $state<string | null>(null)
  let epModalEl     = $state<HTMLDivElement | null>(null)
  // Add-model inline form per endpoint.
  let addModelEpId  = $state<string | null>(null)
  let newModelName  = $state('')
  let newModelVision = $state(false)

  // Focus trap for the endpoint modal.
  $effect(() => {
    if (epModalOpen) epModalEl?.focus()
  })

  // Track the last provider whose preset base_url we auto-filled, so that
  // switching providers only overwrites base_url when the user hasn't hand-edited it.
  let autoFilledBaseURL = $state('')

  // Resolve the ProviderPreset for the current epForm.provider so the template
  // can show the "Get API key" link, variant chips, and lock base_url for named vendors.
  let epPreset = $derived(providers.find(p => p.id === epForm.provider) ?? null)
  // Named vendors (custom_endpoint=false) have a fixed base_url resolved at
  // runtime — the field is readonly so the user can't break the endpoint.
  // Custom vendor (or an empty provider) unlocks it for free-form entry.
  let epBaseUrlLocked = $derived(!!epPreset && !epPreset.custom_endpoint)
  let epVariants = $derived(epPreset?.endpoint_variants ?? [])

  // A named vendor's stored base_url may be empty (the backend resolves the
  // registry default at runtime), which would render an empty readonly field.
  // Backfill from the preset — mirrors ModelConfigForm's backfill effect.
  $effect(() => {
    if (epBaseUrlLocked && !epForm.base_url && epPreset?.base_url) {
      epForm.base_url = epPreset.base_url
      autoFilledBaseURL = epPreset.base_url
    }
  })

  function onProviderChange() {
    if (!epPreset || epForm.provider === 'custom') {
      if (epForm.provider === 'custom') {
        autoFilledBaseURL = ''
        // Custom has no registry-pinned wire format and an empty protocol
        // fails at client build time, so the select offers no blank option —
        // seed the binding to match (mirrors the TUI wizard's forced choice).
        if (!epForm.protocol) epForm.protocol = 'anthropic'
      }
      return
    }
    // Product rule: named vendors always use their registry base_url — the
    // field is readonly for them, so refill unconditionally on switch. A
    // relay/proxy/self-hosted address goes through the custom provider
    // instead. (The backend still honors a base_url override in the config
    // file, but the UI no longer offers one for named vendors.)
    epForm.base_url = epPreset.base_url
    autoFilledBaseURL = epPreset.base_url
  }

  function openEditEndpoint(ep: EndpointConfig) {
    editingEpId = ep.id
    epForm = {
      id: ep.id,
      name: ep.name ?? '',
      provider: ep.provider,
      base_url: ep.base_url ?? '',
      api_key: '', // never prefill the key — server only returns has_api_key
      // A stored custom endpoint with no protocol is unbuildable anyway;
      // default the select to anthropic so saving repairs it.
      protocol: ep.protocol || (ep.provider === 'custom' ? 'anthropic' : ''),
    }
    autoFilledBaseURL = ep.base_url ?? ''
    epModalOpen = true
  }

  function closeEpModal() {
    epModalOpen = false
  }

  function openAddEndpoint() {
    editingEpId = null
    epForm = { id: '', name: '', provider: 'anthropic', base_url: '', api_key: '', protocol: '' }
    autoFilledBaseURL = ''
    epModalOpen = true
  }

  async function submitEndpoint() {
    const id = epForm.id.trim()
    if (!id) { showToast($t('settings.endpoints.error.invalid_id'), 'error'); return }
    if (!/^[a-zA-Z0-9_-]+$/.test(id)) { showToast($t('settings.endpoints.error.invalid_id'), 'error'); return }
    try {
      if (editingEpId) {
        // Edit existing. If id changed, confirm rename (cascade).
        if (id !== editingEpId) {
          if (!(await confirmDialog($t('settings.endpoints.rename_confirm')))) return
        }
        const patch: api.EndpointUpdateInput = {
          new_id: id !== editingEpId ? id : undefined,
          name: epForm.name || undefined,
          provider: epForm.provider || undefined,
          base_url: epForm.base_url || undefined,
          api_key: epForm.api_key || undefined,
          protocol: epForm.protocol || undefined,
        }
        await api.updateEndpoint(editingEpId, patch)
      } else {
        await api.createEndpoint({
          id,
          name: epForm.name || undefined,
          provider: epForm.provider,
          base_url: epForm.base_url || undefined,
          api_key: epForm.api_key || undefined,
          protocol: epForm.protocol || undefined,
          models: [],
        })
      }
      epModalOpen = false
      await loadConfig()
      showToast(tr('settings.toast_saved'), 'success')
    } catch (e: any) {
      showToast(e?.message ?? 'Save failed', 'error')
    }
  }

  async function deleteEndpointRow(ep: EndpointConfig) {
    if (!(await confirmDialog($t('settings.endpoints.confirm_delete')))) return
    busyEpId = ep.id
    try {
      await api.deleteEndpoint(ep.id)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Delete failed', 'error')
    } finally {
      busyEpId = null
    }
  }

  async function setEndpointDefaultRow(ep: EndpointConfig) {
    busyEpId = ep.id
    try {
      await api.setEndpointDefault(ep.id)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Failed', 'error')
    } finally {
      busyEpId = null
    }
  }

  async function toggleEndpointLite(ep: EndpointConfig) {
    busyEpId = ep.id
    try {
      // If this endpoint holds the current lite, unset by setting lite to a
      // different endpoint — but the API only has "set lite" (no unset). PR6
      // simplifies: clicking lite on the endpoint that already holds it is a
      // no-op; clicking on another endpoint moves lite there. Unsetting lite
      // entirely requires a follow-up API (DELETE /api/config/lite) — not in
      // PR5/PR6 scope.
      if (!liteCid.startsWith(`${ep.id}::`)) {
        await api.setEndpointLite(ep.id)
        await loadConfig()
      }
    } catch (e: any) {
      showToast(e?.message ?? 'Failed', 'error')
    } finally {
      busyEpId = null
    }
  }

  function toggleAddModel(ep: EndpointConfig) {
    if (addModelEpId === ep.id) {
      addModelEpId = null
    } else {
      addModelEpId = ep.id
      newModelName = ''
      newModelVision = false
    }
  }

  async function submitAddModel(ep: EndpointConfig) {
    const model = newModelName.trim()
    if (!model) { showToast($t('settings.endpoints.error.empty'), 'error'); return }
    busyEpId = ep.id
    try {
      await api.addEndpointModel(ep.id, model, newModelVision)
      addModelEpId = null
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Add failed', 'error')
    } finally {
      busyEpId = null
    }
  }

  async function deleteEndpointModelRow(ep: EndpointConfig, model: string) {
    if (!(await confirmDialog($t('settings.endpoints.confirm_delete_model')))) return
    busyEpId = ep.id
    try {
      await api.deleteEndpointModel(ep.id, model)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Delete failed', 'error')
    } finally {
      busyEpId = null
    }
  }

  // Focus trap so Esc doesn't leak past the modal (#1112).
  $effect(() => {
    if (modelModalOpen) modelModalEl?.focus()
  })

  function closeModelModal() {
    modelModalOpen = false
  }

  const langOptions = [
    { value: 'en', label: 'English' },
    { value: 'zh', label: '简体中文' },
  ]

  // The whole UI is sized in px, so a :root font-size has no effect. Scale the
  // app with `zoom` instead — that visibly resizes text (and everything else),
  // which is what "base text size" means in practice here.
  const fontZoomMap: Record<string, string> = { Small: '0.9', Medium: '1', Large: '1.1' }

  const modeToThemeLabel: Record<string, string> = { light: 'Light', dark: 'Dark', system: 'System' }

  onMount(async () => {
    const savedFont = localStorage.getItem('octo.fontSize')
    if (savedFont) fontSize = savedFont
    theme = modeToThemeLabel[getMode()] ?? 'Light'
    // Load providers first and wait, so the ModelConfigForm datalist has data
    // when the Add/Edit modal opens immediately.
    await api.listProviders().then(p => { providers = p; providersLoaded = true }).catch(() => { providersLoaded = true })
    await Promise.all([loadConfig(), loadVersion()])
    if (get(nativeShell)) api.getAutostart().then(v => (autostart = v)).catch(() => {})
  })

  // Desktop shell: toggle launch-at-login. Applied immediately (not part of the
  // Save batch); snaps back if the native call fails.
  async function toggleAutostart(v: boolean) {
    try {
      await api.setAutostart(v)
      autostart = v
    } catch (e: any) {
      showToast(e.message ?? 'Failed to change autostart', 'error')
      autostart = !v
    }
  }

  // ── model actions ───────────────────────────────────────────────────────────
  function openAddModel() {
    editingModel = null
    modelModalOpen = true
  }
  function openEditModel(m: ModelEntry) {
    editingModel = m
    // Ensure providers are loaded before opening the modal, so the datalist
    // has options to show. This can happen if the user clicks Edit very early.
    if (!providersLoaded) {
      api.listProviders().then(p => { providers = p; providersLoaded = true }).catch(() => { providersLoaded = true })
    }
    modelModalOpen = true
  }
  async function submitModel(req: ModelConfigInput) {
    if (editingModel) {
      await api.updateModel(editingModel.id, req)
    } else {
      await api.saveModel(req)
    }
    modelModalOpen = false
    await loadConfig()
    showToast(tr('settings.toast_saved'), 'success')
  }
  async function deleteModelRow(m: ModelEntry) {
    if (!(await confirmDialog(tr('settings.models.confirm_remove')))) return
    busyModelId = m.id
    try {
      await api.deleteModel(m.id)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Delete failed', 'error')
    } finally {
      busyModelId = null
    }
  }
  async function setDefaultModel(m: ModelEntry) {
    busyModelId = m.id
    try {
      await api.setDefaultModel(m.id)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Failed', 'error')
    } finally {
      busyModelId = null
    }
  }
  async function toggleLite(m: ModelEntry) {
    busyModelId = m.id
    try {
      await api.setLiteModel(m.id)
      await loadConfig()
    } catch (e: any) {
      showToast(e?.message ?? 'Failed', 'error')
    } finally {
      busyModelId = null
    }
  }
  function maskedFor(m: ModelEntry): string {
    return m.api_key_masked || ''
  }

  async function loadConfig() {
    loading = true
    try {
      const cfg = await api.getConfig() as any
      // PR5: GET /api/config no longer returns a models list (the flat
      // Models field is deleted). The two-level endpoint view
      // (GET /api/config/endpoints) is the only model surface now; the
      // flat AI Models editor section below is hidden until PR6 ships a
      // two-level editor. reasoning_effort is now global.
      defaultModelIdx = 0
      reasoning = capitalize(cfg.reasoning_effort ?? 'medium')
      permMode  = permissionModeToLabel(cfg.permission_mode ?? 'interactive')
      showReasoning = cfg.show_reasoning ?? true
      coauthor = cfg.coauthor ?? true
      workspaceDir = cfg.workspace_dir ?? ''
      origReasoning = reasoning
      origShowReasoning = showReasoning
      origPermMode = permMode
      origCoauthor = coauthor
      origWorkspaceDir = workspaceDir
      // Font size is a client-only preference (persisted in localStorage); the
      // server only hardcodes a placeholder, so don't let it clobber the saved
      // choice. Language still seeds from config when present.
      if (cfg.language)   language = cfg.language
      origLanguage = language

      // PR4b/PR5: load the two-level endpoint view in parallel. Failure is
      // non-fatal — the Endpoints section just renders empty.
      try {
        const ep = await api.getEndpoints()
        endpoints = ep.endpoints ?? []
        defaultCid = ep.default ?? ''
        liteCid = ep.lite ?? ''
      } catch {
        endpoints = []
        defaultCid = ''
        liteCid = ''
      }
    } catch (e: any) {
      showToast(`Failed to load config: ${e.message}`, 'error')
    } finally {
      loading = false
    }
  }

  async function loadVersion() {
    try {
      const v = await api.getVersion() as any
      versionStr = v.current ?? v.version ?? ''
      latestStr = v.latest ?? ''
      updateAvail = !!v.needs_update
      downloadUrl = v.download_url ?? ''
    } catch { /* non-critical */ }
  }

  // Desktop-only "Check for updates" action: re-fetch /api/version (which
  // performs the latest-release lookup) and either open the release page or
  // report that this build is current. Same install-through-installer flow as
  // the version badge; the desktop shell is always a loopback peer, so the
  // native bridge opens the page in the system browser.
  async function checkUpdate() {
    if (checkingUpdate) return
    checkingUpdate = true
    try {
      await loadVersion()
      if (updateAvail && downloadUrl) {
        try { await api.openExternal(downloadUrl) }
        catch { window.open(downloadUrl, '_blank', 'noopener') }
      } else {
        showToast($t('settings.update.uptodate'), 'success')
      }
    } finally {
      checkingUpdate = false
    }
  }

  function capitalize(s: string): string {
    if (!s) return s
    return s[0].toUpperCase() + s.slice(1).toLowerCase()
  }

  function permissionModeToLabel(mode: string): string {
    const map: Record<string, string> = {
      interactive: 'Ask',
      auto: 'Auto',
      strict: 'Strict',
    }
    return map[mode.toLowerCase()] ?? 'Ask'
  }

  function labelToPermissionMode(label: string): string {
    const map: Record<string, string> = {
      Ask: 'interactive',
      Auto: 'auto',
      Strict: 'strict',
    }
    return map[label] ?? 'interactive'
  }

  $effect(() => {
    // Apply font size via zoom and remember it across reloads.
    ;(document.documentElement.style as any).zoom = fontZoomMap[fontSize] ?? '1'
    localStorage.setItem('octo.fontSize', fontSize)
  })

  const themeLabelToMode: Record<string, ThemeMode> = { Light: 'light', Dark: 'dark', System: 'system' }
  $effect(() => {
    // Apply + persist the chosen theme mode (light / dark / system).
    setMode(themeLabelToMode[theme] ?? 'light')
  })

  $effect(() => {
    // Apply language
    setLocale(language === 'zh' || language === 'zh-TW' ? 'zh' : 'en')
  })

  const effortMap: Record<string, string> = { Low: 'low', Medium: 'medium', High: 'high', Xhigh: 'xhigh', Max: 'max' }

  async function handleSave() {
    saving = true
    try {
      // Reasoning Effort + Permission Mode: PR5 made these global config
      // (PUT /api/config/reasoning_effort + PUT /api/config/permission_mode),
      // not per-entry. The old api.updateModel call was deleted — save each
      // changed field through its own global endpoint.
      if (reasoning !== origReasoning) {
        await api.updateReasoningEffort(effortMap[reasoning] ?? 'medium')
        origReasoning = reasoning
      }
      if (permMode !== origPermMode) {
        await api.updatePermissionMode(labelToPermissionMode(permMode))
        origPermMode = permMode
      }

      // Show Reasoning is a separate global fallback (PUT
      // /api/config/show_reasoning) that entries without their own override
      // inherit from — see settings.show_reasoning_desc — so it's saved on
      // its own, not folded into the entry update above.
      if (showReasoning !== origShowReasoning) {
        await api.updateShowReasoning(showReasoning)
        origShowReasoning = showReasoning
      }
      if (coauthor !== origCoauthor) {
        await api.updateCoauthor(coauthor)
        origCoauthor = coauthor
      }

      // Language: persisted globally on the server so a refresh lands on the
      // same locale, then immediately applied.
      if (language !== origLanguage) {
        await api.updateLanguage(language)
        origLanguage = language
        setLocale(language === 'zh' || language === 'zh-TW' ? 'zh' : 'en')
      }

      // Default workspace directory: global config for new web sessions.
      // Empty = no override (use the server's launch directory); "auto" =
      // ~/Desktop/octo; anything else is used as a literal path.
      if (workspaceDir !== origWorkspaceDir) {
        await api.updateWorkspaceDir(workspaceDir)
        origWorkspaceDir = workspaceDir
      }

      showToast(tr('settings.toast_saved'), 'success')
    } catch (e: any) {
      showToast(`Save failed: ${e.message}`, 'error')
    } finally {
      saving = false
    }
  }
</script>

<div class="page">
  <div class="inner">
    <div class="page-header">
      <h2>{$t('settings.title')}</h2>
      <p>{$t('settings.subtitle')}</p>
    </div>

    {#if loading}
      <div class="loading-state">{$t('settings.loading')}</div>
    {:else}
      <!-- Endpoints (PR6: two-level editable) -->
      <div class="section-card">
        <div class="section-head">
          <span class="section-title-inline">{$t('settings.endpoints.title')}</span>
          <button class="btn-add" onclick={openAddEndpoint}>
            <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
            {$t('settings.endpoints.add')}
          </button>
        </div>
        {#if endpoints.length === 0}
          <div class="models-empty">{$t('settings.endpoints.empty')}</div>
        {:else}
          {#each endpoints as ep (ep.id)}
            <div class="endpoint-card">
              <div class="endpoint-head">
                <div class="endpoint-title-line">
                  <span class="endpoint-id mono">{ep.id}</span>
                  {#if ep.name}<span class="endpoint-name">{ep.name}</span>{/if}
                  {#if defaultCid.startsWith(`${ep.id}::`)}
                    <StatusTag status="success">{$t('settings.endpoints.badge.default')}</StatusTag>
                  {/if}
                  {#if liteCid.startsWith(`${ep.id}::`)}
                    <StatusTag status="info">{$t('settings.endpoints.badge.lite')}</StatusTag>
                  {/if}
                </div>
                <div class="endpoint-meta mono">
                  <span>{ep.provider}</span>
                  {#if ep.base_url}<span> · {ep.base_url}</span>{/if}
                  {#if ep.protocol}<span> · {ep.protocol}</span>{/if}
                </div>
                <div class="endpoint-key">
                  {#if ep.has_api_key}
                    <span class="key-set">{$t('settings.endpoints.api_key')}: {$t('settings.endpoints.api_key.set')}</span>
                  {:else}
                    <span class="key-missing">{$t('settings.endpoints.api_key')}: {$t('settings.endpoints.api_key.missing')}</span>
                  {/if}
                </div>
                <div class="endpoint-actions">
                  {#if !defaultCid.startsWith(`${ep.id}::`)}
                    <button class="act-text" disabled={busyEpId === ep.id} onclick={() => setEndpointDefaultRow(ep)}>{$t('settings.endpoints.set_default')}</button>
                  {/if}
                  <button class="act-text" disabled={busyEpId === ep.id} onclick={() => toggleEndpointLite(ep)}>
                    {liteCid.startsWith(`${ep.id}::`) ? $t('settings.endpoints.badge.lite') : $t('settings.endpoints.set_lite')}
                  </button>
                  <button class="act-btn" title={$t('settings.endpoints.edit')} onclick={() => openEditEndpoint(ep)}>
                    <iconify-icon icon="ant-design:edit-outlined" width="14"></iconify-icon>
                  </button>
                  <button class="act-btn del" title={$t('settings.endpoints.delete')} disabled={busyEpId === ep.id} onclick={() => deleteEndpointRow(ep)}>
                    <iconify-icon icon="ant-design:delete-outlined" width="14"></iconify-icon>
                  </button>
                </div>
              </div>
              <div class="endpoint-models">
                <div class="endpoint-models-head">
                  {$t('settings.endpoints.models')}
                  <button class="act-text ep-add-model-btn" disabled={busyEpId === ep.id} onclick={() => toggleAddModel(ep)}>{$t('settings.endpoints.models.add')}</button>
                </div>
                {#each ep.models as m (m.model)}
                  <div class="endpoint-model-row">
                    <span class="mono">{ep.id}::{m.model}</span>
                    {#if m.vision}<span class="vision-tag">{$t('settings.endpoints.models.vision')}</span>{/if}
                    {#if defaultCid === `${ep.id}::${m.model}`}
                      <StatusTag status="success">{$t('settings.endpoints.badge.default')}</StatusTag>
                    {/if}
                    {#if liteCid === `${ep.id}::${m.model}`}
                      <StatusTag status="info">{$t('settings.endpoints.badge.lite')}</StatusTag>
                    {/if}
                    <button class="act-btn del ep-model-del" title={$t('common.delete')} disabled={busyEpId === ep.id} onclick={() => deleteEndpointModelRow(ep, m.model)}>
                      <iconify-icon icon="ant-design:delete-outlined" width="12"></iconify-icon>
                    </button>
                  </div>
                {/each}
                {#if addModelEpId === ep.id}
                  <div class="ep-add-model-form">
                    <input class="input ep-add-model-input" placeholder={$t('settings.endpoints.models.model')} bind:value={newModelName} />
                    <label class="ep-vision-toggle">
                      <input type="checkbox" bind:checked={newModelVision} />
                      <span>{$t('settings.endpoints.models.vision_hint')}</span>
                    </label>
                    <button class="btn-secondary" disabled={busyEpId === ep.id} onclick={() => submitAddModel(ep)}>{$t('settings.endpoints.modal.save')}</button>
                    <button class="btn-secondary" onclick={() => { addModelEpId = null }}>{$t('settings.endpoints.modal.cancel')}</button>
                  </div>
                {/if}
              </div>
            </div>
          {/each}
        {/if}
      </div>

      <!-- AI Models (config-level entries) — PR5: hidden. The flat Models
           editor and its backend routes (POST/PATCH/DELETE /api/config/models*)
           are deleted in PR5. A two-level endpoint editor lands in PR6.
           The section block is kept (wrapped in {#if false}) so PR6 can
           revive it; the i18n keys and handler stubs remain. -->
      {#if false}
      <div class="section-card">
        <div class="section-head">
          <span class="section-title-inline">{$t('settings.models.title')}</span>
          <button class="btn-add" onclick={openAddModel}>
            <iconify-icon icon="ant-design:plus-outlined" width="13"></iconify-icon>
            {$t('settings.models.add')}
          </button>
        </div>
        {#if models.length === 0}
          <div class="models-empty">{$t('settings.models.empty')}</div>
        {:else}
          {#each models as m (m.id)}
            <div class="model-row">
              <div class="model-info">
                <div class="model-name-line">
                  <span class="model-name mono">{m.model}</span>
                  {#if m.type === 'default'}<StatusTag status="success">{$t('settings.models.badge.default')}</StatusTag>{/if}
                  {#if m.type === 'lite'}<StatusTag status="info">{$t('settings.models.badge.lite')}</StatusTag>{/if}
                </div>
                <span class="model-meta mono">{m.base_url}{maskedFor(m) ? ` · ${maskedFor(m)}` : ''}</span>
              </div>
              <div class="model-actions">
                {#if m.type !== 'default'}
                  <button class="act-text" disabled={busyModelId === m.id} onclick={() => setDefaultModel(m)}>{$t('settings.models.set_default')}</button>
                {/if}
                <button class="act-text" disabled={busyModelId === m.id} title={$t('settings.models.lite_hint')} onclick={() => toggleLite(m)}>
                  {m.type === 'lite' ? $t('settings.models.unset_lite') : $t('settings.models.set_lite')}
                </button>
                <button class="act-btn" title={$t('common.edit')} onclick={() => openEditModel(m)}>
                  <iconify-icon icon="ant-design:edit-outlined" width="14"></iconify-icon>
                </button>
                <button class="act-btn del" title={$t('common.delete')} disabled={busyModelId === m.id} onclick={() => deleteModelRow(m)}>
                  <iconify-icon icon="ant-design:delete-outlined" width="14"></iconify-icon>
                </button>
              </div>
            </div>
          {/each}
        {/if}
      </div>
      {/if}

      <!-- General -->
      <div class="section-card">
        <div class="section-title">{$t('settings.general')}</div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.language')}</span>
            <span class="setting-desc">{$t('settings.language_desc')}</span>
          </div>
          <select class="sel" bind:value={language}>
            {#each langOptions as o}
              <option value={o.value}>{o.label}</option>
            {/each}
          </select>
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.font_size')}</span>
            <span class="setting-desc">{$t('settings.font_size_desc')}</span>
          </div>
          <Segment options={['Small', 'Medium', 'Large']} labels={{ Small: $t('settings.fs_small'), Medium: $t('settings.fs_medium'), Large: $t('settings.fs_large') }} bind:value={fontSize} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.theme')}</span>
            <span class="setting-desc">{$t('settings.theme_desc')}</span>
          </div>
          <Segment options={['Light', 'Dark', 'System']} labels={{ Light: $t('settings.theme_light'), Dark: $t('settings.theme_dark'), System: $t('settings.theme_system') }} bind:value={theme} />
        </div>
        {#if $nativeShell}
          <div class="setting-row">
            <div class="setting-info">
              <span class="setting-label">{$t('settings.autostart')}</span>
              <span class="setting-desc">{$t('settings.autostart_desc')}</span>
            </div>
            <Switch checked={autostart} onchange={(v) => toggleAutostart(v)} />
          </div>
          <div class="setting-row">
            <div class="setting-info">
              <span class="setting-label">{$t('settings.update')}</span>
              <span class="setting-desc">
                {#if updateAvail}
                  {$t('settings.update_available')} v{latestStr}
                {:else}
                  {$t('settings.update_desc')}
                {/if}
              </span>
            </div>
            <button class="btn-secondary" onclick={checkUpdate} disabled={checkingUpdate}>
              {checkingUpdate ? $t('settings.update.checking') : updateAvail ? $t('upgrade.btn.download') : $t('settings.update.check')}
            </button>
          </div>
        {/if}
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.notifications')}</span>
            <span class="setting-desc">{$t('settings.notifications_desc')}</span>
          </div>
          <!-- Client-only preference (localStorage), applied immediately — not
               part of the Save batch. Enabling requests browser permission and
               may snap back off if it's denied; setNotificationsEnabled owns
               that, and $notificationsEnabled reflects the settled state. -->
          <Switch checked={$notificationsEnabled} onchange={(v) => setNotificationsEnabled(v)} />
        </div>
      </div>

      <!-- Agent defaults -->
      <div class="section-card">
        <div class="section-title">{$t('settings.agent')}</div>
        <!-- Default Model itself is set from the Models list below (the
             "Set as default" action on a model card) — no separate control
             needed here. -->
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.reasoning')}</span>
            <span class="setting-desc">{$t('settings.reasoning_desc')}</span>
          </div>
          <Segment options={['Low', 'Medium', 'High', 'Xhigh', 'Max']} labels={{ Low: $t('settings.re_low'), Medium: $t('settings.re_medium'), High: $t('settings.re_high'), Xhigh: $t('settings.re_xhigh'), Max: $t('settings.re_max') }} bind:value={reasoning} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.perm_mode')}</span>
            <span class="setting-desc">{$t('settings.perm_mode_desc')}</span>
          </div>
          <Segment options={['Ask', 'Auto', 'Strict']} labels={{ Ask: $t('settings.pm_ask'), Auto: $t('settings.pm_auto'), Strict: $t('settings.pm_strict') ?? 'Strict' }} bind:value={permMode} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.show_reasoning')}</span>
            <span class="setting-desc">{$t('settings.show_reasoning_desc')}</span>
          </div>
          <Switch bind:checked={showReasoning} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.coauthor')}</span>
            <span class="setting-desc">{$t('settings.coauthor_desc')}</span>
          </div>
          <Switch bind:checked={coauthor} />
        </div>
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.workspace_dir')}</span>
            <span class="setting-desc">{$t('settings.workspace_dir_desc')}</span>
          </div>
          <input class="input" bind:value={workspaceDir} placeholder="auto or ~/code/my-project" />
        </div>
      </div>

      <!-- Save -->
      <div class="save-row">
        <button
          class="btn-primary"
          onclick={handleSave}
          disabled={saving}
        >
          {saving ? $t('settings.saving') : $t('settings.save_changes')}
        </button>
        {#if versionStr}
          <span class="version-badge">{$t('common.version')} {versionStr}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

<!-- Add / edit model modal -->
{#if modelModalOpen}
  <!-- #1112: backdrop is inert (no dismiss-on-click) — a stray click used to
       silently discard the in-progress form. Esc closes explicitly. -->
  <div class="modal-backdrop" role="presentation">
    <div class="modal" bind:this={modelModalEl} onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); closeModelModal() } }} role="dialog" aria-modal="true" tabindex="-1">
      <div class="modal-header">
        <span class="modal-title">{editingModel ? $t('settings.models.modal_edit') : $t('settings.models.modal_add')}</span>
        <button class="modal-close" onclick={closeModelModal} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body">
        <ModelConfigForm
          {providers}
          initial={editingModel}
          requireKey={!editingModel}
          submitLabel={$t('models.btn.save')}
          onSubmit={submitModel}
        />
      </div>
    </div>
  </div>
{/if}

<!-- Add / edit endpoint modal (PR6) -->
{#if epModalOpen}
  <div class="modal-backdrop" role="presentation">
    <div class="modal" bind:this={epModalEl} onkeydown={(e) => { if (e.key === 'Escape') { e.preventDefault(); closeEpModal() } }} role="dialog" aria-modal="true" tabindex="-1">
      <div class="modal-header">
        <span class="modal-title">{editingEpId ? $t('settings.endpoints.modal.edit_title') : $t('settings.endpoints.modal.add_title')}</span>
        <button class="modal-close" onclick={closeEpModal} aria-label={$t('common.close')}>
          <iconify-icon icon="ant-design:close-outlined" width="14"></iconify-icon>
        </button>
      </div>
      <div class="modal-body ep-form">
        <label class="ep-field">
          <span class="ep-label">{$t('settings.endpoints.field.id')}</span>
          <input class="ep-input" bind:value={epForm.id} placeholder="relay-a" />
        </label>
        <label class="ep-field">
          <span class="ep-label">{$t('settings.endpoints.field.name')}</span>
          <input class="ep-input" bind:value={epForm.name} placeholder={$t('settings.endpoints.field.name')} />
        </label>
        <label class="ep-field">
          <span class="ep-label">{$t('settings.endpoints.field.provider')}</span>
          <select class="ep-input" bind:value={epForm.provider} onchange={onProviderChange}>
            {#each providers as p}
              <option value={p.id}>{p.name}</option>
            {/each}
          </select>
        </label>
        {#if epForm.provider === 'custom'}
          <label class="ep-field">
            <span class="ep-label">{$t('settings.endpoints.field.protocol')}</span>
            <select class="ep-input" bind:value={epForm.protocol}>
              <option value="anthropic">anthropic</option>
              <option value="openai">openai</option>
            </select>
          </label>
        {/if}
        <label class="ep-field">
          <span class="ep-label">{$t('settings.endpoints.field.base_url')}</span>
          <input class="ep-input" bind:value={epForm.base_url} readonly={epBaseUrlLocked} placeholder="https://api.example.com" />
          <VariantChips variants={epVariants} value={epForm.base_url} onselect={(v) => { epForm.base_url = v.base_url; autoFilledBaseURL = v.base_url }} />
        </label>

        <div class="ep-divider"></div>

        <label class="ep-field">
          <span class="ep-label">{$t('settings.endpoints.field.api_key')}</span>
          <ApiKeyInput bind:value={epForm.api_key} placeholder={$t('settings.endpoints.field.api_key_hint')} />
          {#if epPreset?.website_url}
            <a class="field-link" href={epPreset.website_url} target="_blank" rel="noreferrer">{$t('models.get_apikey')}</a>
          {/if}
        </label>
        <div class="ep-form-actions">
          <button class="btn-secondary" onclick={closeEpModal}>{$t('settings.endpoints.modal.cancel')}</button>
          <button class="btn-primary" onclick={submitEndpoint}>{$t('settings.endpoints.modal.save')}</button>
        </div>
      </div>
    </div>
  </div>
{/if}

<style>
.page { flex: 1; overflow-y: auto; min-height: 0; }
.inner { max-width: 800px; margin: 0 auto; padding: 24px; display: flex; flex-direction: column; gap: 24px; }
.page-header { display: flex; flex-direction: column; gap: 4px; }
h2 { margin: 0; font-size: 24px; font-weight: 600; color: var(--text-heading); }
p { margin: 0; font-size: 14px; color: var(--text-secondary); }
.loading-state { padding: 40px; text-align: center; color: var(--text-tertiary); font-size: 14px; }
.section-card { background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow); overflow: hidden; }
.section-title { padding: 16px 24px; border-bottom: 1px solid var(--border-table); font-size: 16px; font-weight: 600; color: var(--text-heading); }
.setting-row {
  display: flex; align-items: center; justify-content: space-between;
  gap: 24px; padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.setting-row.last { border-bottom: none; }
.setting-info { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.setting-label { font-size: 14px; color: var(--text); }
.setting-desc { font-size: 12px; color: var(--text-tertiary); }
.sel {
  width: 220px; flex: 0 0 auto; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 6px; font-size: 13px;
  color: var(--text); font-family: inherit; background: var(--bg-container); cursor: pointer; outline: none;
}
.sel:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.input {
  width: 220px; flex: 0 0 auto; height: 32px; padding: 0 10px;
  border: 1px solid var(--border); border-radius: 6px; font-size: 13px;
  color: var(--text); font-family: inherit; outline: none;
}
.input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(5,145,255,0.1); }
.save-row { display: flex; align-items: center; gap: 16px; }
.btn-primary { height: 32px; padding: 0 16px; border: none; background: var(--blue-6); border-radius: 6px; font-size: 14px; color: #fff; cursor: pointer; font-family: inherit; }
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
.version-badge { font-size: 12px; color: var(--text-tertiary); }

/* ── Models section ─────────────────────────────────────────────────────────── */
.section-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.section-title-inline { font-size: 16px; font-weight: 600; color: var(--text-heading); }

/* ── Endpoints section (PR6 two-level editable) ────────────────────────────── */
.endpoint-card {
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.endpoint-card:last-child { border-bottom: none; }
.endpoint-head { display: flex; flex-direction: column; gap: 4px; margin-bottom: 8px; }
.endpoint-title-line { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.endpoint-id { font-size: 14px; font-weight: 600; color: var(--text); }
.endpoint-name { font-size: 13px; color: var(--text-secondary); }
.endpoint-meta { font-size: 12px; color: var(--text-tertiary); display: flex; flex-wrap: wrap; }
.endpoint-key { font-size: 12px; }
.endpoint-key .key-set { color: var(--green-6, #16a34a); }
.endpoint-key .key-missing { color: var(--red-6, #dc2626); }
.endpoint-models { margin-top: 6px; padding-left: 12px; border-left: 2px solid var(--border-table); }
.endpoint-models-head {
  font-size: 12px; color: var(--text-tertiary);
  text-transform: uppercase; letter-spacing: 0.04em; margin-bottom: 4px;
}
.endpoint-model-row {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  padding: 4px 0; font-size: 13px; color: var(--text);
}
.vision-tag {
  font-size: 11px; padding: 1px 6px; border-radius: 4px;
  background: var(--bg-subtle, rgba(0,0,0,0.04)); color: var(--text-tertiary);
}

/* PR6 endpoint editor */
.endpoint-actions { display: flex; align-items: center; gap: 6px; margin-top: 4px; }
.ep-add-model-btn { font-size: 11px; padding: 0 6px; margin-left: 8px; }
.ep-model-del { opacity: 0.5; }
.ep-model-del:hover { opacity: 1; color: var(--red-6, #dc2626); }
.ep-add-model-form {
  display: flex; align-items: center; gap: 8px; flex-wrap: wrap;
  margin-top: 6px; padding: 8px; background: var(--bg-subtle, rgba(0,0,0,0.02));
  border-radius: 6px;
}
.ep-add-model-input { flex: 1; min-width: 140px; }
.ep-vision-toggle { display: flex; align-items: center; gap: 4px; font-size: 12px; color: var(--text-tertiary); }
.ep-form { display: flex; flex-direction: column; gap: 14px; }
.ep-field { display: flex; flex-direction: column; gap: 6px; }
.ep-label { font-size: 13px; font-weight: 500; color: var(--text-secondary); }
.ep-input {
  width: 100%; height: 36px; padding: 0 12px;
  border: 1px solid var(--border); border-radius: 8px; font-size: 13px;
  color: var(--text); font-family: inherit; background: var(--bg-container); outline: none;
  transition: border-color 0.15s, box-shadow 0.15s;
}
.ep-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 3px var(--active-blue-bg); }
.ep-input[readonly] { background: var(--bg-table-header); color: var(--text-tertiary); cursor: not-allowed; }
select.ep-input { cursor: pointer; }
/* -24px matches .modal-body's horizontal padding so the divider bleeds full width. */
.ep-divider { height: 1px; margin: 4px -24px; background: var(--border-table); }
.field-link { font-size: 12px; color: var(--blue-6); text-decoration: none; align-self: flex-start; }
.field-link:hover { text-decoration: underline; }
.ep-form-actions { display: flex; justify-content: flex-end; gap: 10px; margin-top: 8px; }
.ep-form-actions .btn-secondary,
.ep-form-actions .btn-primary { height: 36px; border-radius: 8px; }

.btn-add {
  height: 36px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.btn-add:hover { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary {
  height: 30px; padding: 0 14px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer;
  font-family: inherit; white-space: nowrap;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
.models-empty { padding: 28px 24px; text-align: center; font-size: 13px; color: var(--text-tertiary); }
.model-row {
  display: flex; align-items: center; justify-content: space-between; gap: 16px;
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.model-row:last-child { border-bottom: none; }
.model-info { display: flex; flex-direction: column; gap: 4px; min-width: 0; }
.model-name-line { display: flex; align-items: center; gap: 8px; }
.model-name { font-size: 14px; color: var(--text); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.model-meta { font-size: 12px; color: var(--text-tertiary); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.model-actions { display: flex; align-items: center; gap: 6px; flex: 0 0 auto; }
.act-text {
  height: 28px; padding: 0 10px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.act-text:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.act-text:disabled { opacity: 0.5; cursor: not-allowed; }
.act-btn {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-tertiary);
}
.act-btn:hover { background: var(--hover-neutral); color: var(--blue-6); }
.act-btn.del:hover { color: var(--error); }
.act-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }

/* ── modal ──────────────────────────────────────────────────────────────────── */
.modal-backdrop {
  position: fixed; inset: 0; background: var(--text-tertiary);
  display: flex; align-items: flex-start; justify-content: center; z-index: 200;
  padding: 48px 16px; overflow-y: auto;
}
.modal {
  width: 520px; max-width: 100%;
  background: var(--bg-container); border-radius: 16px; box-shadow: 0 24px 48px rgba(15,23,42,0.18);
  display: flex; flex-direction: column; overflow: hidden;
}
.modal:focus { outline: none; }
.modal-header {
  display: flex; align-items: center; justify-content: space-between;
  padding: 18px 24px 16px; border-bottom: 1px solid var(--border-table);
}
.modal-title { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.modal-close {
  width: 28px; height: 28px; border: none; background: transparent; border-radius: 6px;
  display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-tertiary);
}
.modal-close:hover { background: var(--hover-neutral); color: var(--text); }
.modal-body { padding: 20px 24px; }
</style>

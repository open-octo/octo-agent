<script lang="ts">
  import { onMount } from 'svelte'
  import Segment from '../components/ui/Segment.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import StatusTag from '../components/ui/StatusTag.svelte'
  import ModelConfigForm from '../components/settings/ModelConfigForm.svelte'
  import { get } from 'svelte/store'
  import { showToast, activeSessionId, chatWorkingDir } from '../lib/stores'
  import { setLocale, t, tr } from '../lib/i18n'
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import * as api from '../lib/api'
  import type { ModelEntry, ProviderPreset, ModelConfigInput } from '../lib/api'

  // --- local state ---
  let language      = $state('en')
  let fontSize      = $state('Medium')
  let theme         = $state('Light')
  let reasoning     = $state('Medium')
  let permMode      = $state('Ask')
  let workdir       = $state('')
  let showReasoning = $state(true)
  let coauthor      = $state(true)
  let versionStr    = $state('')
  let saving        = $state(false)
  let loading       = $state(true)
  let providersLoaded = $state(false)

  // Original values for dirty-checking
  let origLanguage    = 'en'
  let origWorkdir      = ''
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
    // Seed the working-dir field from the active session's current dir (tracked
    // in the store from session_update/list), so it shows where tools run
    // rather than a blank box. Empty leaves the placeholder.
    const sid = get(activeSessionId)
    if (sid) {
      const wd = get(chatWorkingDir)[sid]
      if (wd) { workdir = wd; origWorkdir = wd }
    }
    // Load providers first and wait, so the ModelConfigForm datalist has data
    // when the Add/Edit modal opens immediately.
    await api.listProviders().then(p => { providers = p; providersLoaded = true }).catch(() => { providersLoaded = true })
    await Promise.all([loadConfig(), loadVersion()])
  })

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
    if (!confirm(tr('settings.models.confirm_remove'))) return
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
      // models list
      const ms: any[] = cfg.models ?? []
      models = ms as ModelEntry[]
      defaultModelIdx = cfg.default_model_idx ?? 0
      const def = ms[defaultModelIdx]
      if (def) {
        reasoning = capitalize(def.reasoning_effort ?? 'medium')
        permMode  = permissionModeToLabel(def.permission_mode ?? 'interactive')
      }
      showReasoning = cfg.show_reasoning ?? true
      coauthor = cfg.coauthor ?? true
      origReasoning = reasoning
      origShowReasoning = showReasoning
      origPermMode = permMode
      origCoauthor = coauthor
      // Font size is a client-only preference (persisted in localStorage); the
      // server only hardcodes a placeholder, so don't let it clobber the saved
      // choice. Language still seeds from config when present.
      if (cfg.language)   language = cfg.language
      origLanguage = language
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
    } catch { /* non-critical */ }
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

  // Working directory is the only field left on this page that's genuinely
  // per-session (there's no such thing as a "default" working directory) —
  // everything else here is global config. Drives the "start a session"
  // hint, shown only when it's actually relevant.
  let needsSession = $derived(!$activeSessionId && workdir !== origWorkdir)

  async function handleSave() {
    saving = true
    const sid = $activeSessionId
    try {
      // Agent Defaults (Reasoning Effort + Permission Mode) — the default
      // model entry's own settings, saved the same way the Models section
      // below saves any entry: api.updateModel(id, ...the full entry...).
      // No session is involved; this is config, not session state. update-
      // Model isn't a partial PATCH, so the rest of the entry's fields (base
      // URL, key, provider, vision) are resent unchanged alongside the two
      // that actually changed — dropping them would blank those fields out.
      if (reasoning !== origReasoning || permMode !== origPermMode) {
        const def = models[defaultModelIdx]
        if (def) {
          await api.updateModel(def.id, {
            model: def.model,
            base_url: def.base_url ?? '',
            api_key: def.api_key_masked ?? '',
            provider: def.provider,
            anthropic_format: def.anthropic_format,
            vision: def.vision,
            reasoning_effort: effortMap[reasoning] ?? 'medium',
            permission_mode: labelToPermissionMode(permMode),
          })
          origReasoning = reasoning
          origPermMode = permMode
        }
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

      // Working directory: the one field that actually needs an active
      // session, since it's inherently per-session state.
      if (workdir !== origWorkdir) {
        if (!sid) {
          showToast(tr('settings.no_session_tooltip'), 'warning')
          return
        }
        await api.updateSessionWorkingDir(sid, workdir)
        origWorkdir = workdir
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
      <!-- AI Models (config-level entries) -->
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
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.theme')}</span>
            <span class="setting-desc">{$t('settings.theme_desc')}</span>
          </div>
          <Segment options={['Light', 'Dark', 'System']} labels={{ Light: $t('settings.theme_light'), Dark: $t('settings.theme_dark'), System: $t('settings.theme_system') }} bind:value={theme} />
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
            <span class="setting-label">{$t('settings.workdir')}</span>
            <span class="setting-desc">{$t('settings.workdir_desc')}</span>
          </div>
          <input class="input" bind:value={workdir} placeholder="~/code/my-project" />
        </div>
      </div>

      <!-- Save -->
      <div class="save-row">
        <button
          class="btn-primary"
          onclick={handleSave}
          disabled={saving}
          title={needsSession ? tr('settings.no_session_tooltip') : ''}
        >
          {saving ? $t('settings.saving') : $t('settings.save_changes')}
        </button>
        {#if needsSession}
          <span class="session-hint">{tr('settings.no_session_tooltip')}</span>
        {/if}
        {#if versionStr}
          <span class="version-badge">{$t('common.version')} {versionStr}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

<!-- Add / edit model modal -->
{#if modelModalOpen}
  <!-- svelte-ignore a11y_click_events_have_key_events -->
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="modal-backdrop" onclick={(e) => { if ((e.target as HTMLElement).classList.contains('modal-backdrop')) modelModalOpen = false }}>
    <div class="modal" role="dialog" aria-modal="true">
      <div class="modal-header">
        <span class="modal-title">{editingModel ? $t('settings.models.modal_edit') : $t('settings.models.modal_add')}</span>
        <button class="modal-close" onclick={() => (modelModalOpen = false)} aria-label={$t('common.close')}>
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
.session-hint { font-size: 12px; color: var(--text-tertiary); }

/* ── Models section ─────────────────────────────────────────────────────────── */
.section-head {
  display: flex; align-items: center; justify-content: space-between;
  padding: 14px 24px; border-bottom: 1px solid var(--border-table);
}
.section-title-inline { font-size: 16px; font-weight: 600; color: var(--text-heading); }
.btn-add {
  height: 30px; padding: 0 12px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; display: flex; align-items: center; gap: 6px;
  font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.btn-add:hover { border-color: var(--blue-5); color: var(--blue-5); }
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

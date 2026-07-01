<script lang="ts">
  import { untrack } from 'svelte'
  import { t } from '../../lib/i18n'
  import * as api from '../../lib/api'
  import type { ProviderPreset, ModelEntry, ModelConfigInput } from '../../lib/api'
  import Switch from '../ui/Switch.svelte'

  // Shared provider/key form used by both the first-run setup panel and the
  // Settings → Models section. It owns the Test-connection button internally;
  // persistence differs per caller, so Save is delegated via onSubmit.
  let {
    providers = [],
    initial = null,
    requireKey = true,
    showPrefs = true,
    submitLabel,
    onSubmit,
  }: {
    providers?: ProviderPreset[]
    initial?: Partial<ModelEntry> | null
    requireKey?: boolean
    // First-run onboard hides these: the /onboard ceremony collects permission
    // mode / reasoning effort / show-reasoning itself, so the bootstrap form
    // would only ask the same questions twice.
    showPrefs?: boolean
    submitLabel: string
    onSubmit: (req: ModelConfigInput) => Promise<void>
  } = $props()

  // ── form state ──────────────────────────────────────────────────────────────
  // Snapshot the prop once: this form is mounted fresh each time a modal opens,
  // so seeding editable state from the initial value (not tracking it) is what
  // we want — and the snapshot avoids the state_referenced_locally warning.
  const seed = untrack(() => initial) ?? {}

  // Preselect the stored provider id (a real vendor, always present in the
  // presets list); an unknown id just falls back to the placeholder option.
  let providerId   = $state(seed.provider ?? '')
  // Some legacy/shortened entries have an empty model field while the entry id
  // carries the intended model name. Use id as a fallback so the edit form is
  // pre-filled and saveable without re-typing.
  let model        = $state(seed.model || (seed as Partial<ModelEntry>).id || '')
  let baseUrl      = $state(seed.base_url ?? '')
  // Protocol is only meaningful for the Custom vendor; seed from the entry's
  // stored wire format (surfaced as anthropic_format) so an edit round-trips.
  let protocol     = $state<'openai' | 'anthropic'>(seed.anthropic_format ? 'anthropic' : 'openai')
  let apiKey       = $state('')            // never prefilled; placeholder shows the masked key
  let permMode     = $state(seed.permission_mode ?? 'interactive')
  let reasoning    = $state(seed.reasoning_effort ?? 'off')
  let showReason   = $state(seed.show_reasoning ?? false)
  // Vision is always recorded. Editing seeds from the stored value; a new entry
  // gets the picked model's catalogue value (via applyPresetVision on select),
  // falling back to true until the user picks or toggles.
  let vision       = $state(seed.vision ?? true)
  let showKey      = $state(false)

  let testing      = $state(false)
  let saving       = $state(false)
  let result       = $state<{ ok: boolean; msg: string } | null>(null)

  let preset    = $derived.by(() => {
    if (providerId) {
      return providers.find(p => p.id === providerId) ?? null
    }
    // If providerId is empty but baseUrl is set, try to infer the provider
    // from the endpoint (for legacy entries without a stored provider field).
    if (baseUrl) {
      const normalized = baseUrl.replace(/\/$/, '')
      for (const p of providers) {
        if (p.base_url.replace(/\/$/, '') === normalized) {
          return p
        }
        for (const v of p.endpoint_variants ?? []) {
          if (v.base_url.replace(/\/$/, '') === normalized) {
            return p
          }
        }
      }
    }
    return null
  })
  let variants  = $derived(preset?.endpoint_variants ?? [])

  // When preset is inferred from baseUrl (providerId is empty), auto-fill
  // providerId so the datalist renders and the save includes the provider.
  $effect(() => {
    if (!providerId && preset && baseUrl) {
      providerId = preset.id
    }
  })
  // A named vendor's base URL is resolved from the vendor default at runtime and
  // may not be persisted in the stored entry. Editing such an entry seeds an
  // empty baseUrl into a readonly (locked) field, which would fail validation
  // and block Test/Save. Backfill it from the preset default when empty.
  $effect(() => {
    if (preset && !preset.custom_endpoint && !baseUrl && preset.base_url) {
      baseUrl = preset.base_url
    }
  })
  // Catalogue vendors are pinned to their endpoint; only custom_endpoint
  // providers take a typed Base URL.
  let baseUrlLocked = $derived(!!preset && !preset.custom_endpoint)
  // The Custom vendor has no fixed wire protocol (empty api), so the user picks
  // it explicitly. A hand-typed endpoint with no preset is Custom too.
  let isCustom = $derived(preset ? (!!preset.custom_endpoint && !preset.api) : !!baseUrl)
  let keyPlaceholder = $derived(initial?.api_key_masked || $t('models.apikey.placeholder'))

  function variantLabel(v: api.EndpointVariant): string {
    if (v.label_key) {
      const tr = $t(v.label_key)
      if (tr && tr !== v.label_key) return tr
    }
    return v.label || v.base_url
  }

  // Selecting a preset fills model + base_url.
  function onProviderChange() {
    result = null
    if (providerId === '') {
      return
    }
    if (preset) {
      model = preset.default_model || ''
      baseUrl = preset.base_url || ''
      applyPresetVision()
    }
  }

  // A predefined model carries its vision capability in the preset catalogue;
  // selecting one pre-fills the toggle (the user can still override it, and the
  // server honours whatever is sent). Custom / unknown models keep the current
  // value.
  function applyPresetVision() {
    const v = preset?.model_vision?.[model]
    if (v !== undefined) vision = v
  }

  // For named vendors the protocol is decided by the chosen preset; for the
  // Custom vendor (or a hand-typed endpoint with no preset) the user picks it.
  function buildReq(): ModelConfigInput {
    const providerID = preset ? preset.id : 'custom'
    const anthropic = isCustom
      ? protocol === 'anthropic'
      : !!(preset && preset.api === 'anthropic-messages')
    const req: ModelConfigInput = {
      type: 'default',
      model: model.trim(),
      base_url: baseUrl.trim(),
      api_key: apiKey.trim(),
      provider: providerID,
      anthropic_format: anthropic,
    }
    // Onboard hides prefs; leave them to server defaults + the /onboard ceremony.
    // (Vision, when omitted, is resolved server-side from the catalogue/heuristic.)
    if (showPrefs) {
      req.permission_mode = permMode
      req.reasoning_effort = reasoning
      req.show_reasoning = showReason
      req.vision = vision
    }
    return req
  }

  function valid(): boolean {
    return !!model.trim() && !!baseUrl.trim() && (!requireKey || !!apiKey.trim())
  }

  async function handleTest() {
    if (!valid()) { result = { ok: false, msg: $t('models.error.required') }; return }
    testing = true
    result = null
    try {
      const r = await api.testConfig(buildReq())
      result = { ok: r.ok, msg: r.ok ? $t('models.test.ok') : (r.message || $t('models.test.fail')) }
    } catch (e: any) {
      result = { ok: false, msg: e?.message ?? $t('models.test.fail') }
    } finally {
      testing = false
    }
  }

  async function handleSubmit() {
    if (!valid()) { result = { ok: false, msg: $t('models.error.required') }; return }
    saving = true
    try {
      await onSubmit(buildReq())
    } catch (e: any) {
      result = { ok: false, msg: e?.message ?? $t('models.save.fail') }
      saving = false
    }
    // On success the parent typically unmounts this form (closes modal / advances
    // onboard), so we leave `saving` true to keep the button disabled until then.
  }
</script>

<div class="form">
  <!-- Provider -->
  <label class="field">
    <span class="field-label">{$t('models.provider')}</span>
    <select class="field-input" bind:value={providerId} onchange={onProviderChange} disabled={saving}>
      <option value="">{$t('models.provider.placeholder')}</option>
      {#each providers as p (p.id)}
        <option value={p.id}>{p.name}</option>
      {/each}
    </select>
    {#if preset?.website_url}
      <a class="field-link" href={preset.website_url} target="_blank" rel="noreferrer">{$t('models.get_apikey')}</a>
    {/if}
  </label>

  <!-- Protocol (Custom vendor only — named vendors pin their own wire format) -->
  {#if isCustom}
    <label class="field">
      <span class="field-label">{$t('models.protocol')}</span>
      <select class="field-input" bind:value={protocol} disabled={saving}>
        <option value="openai">{$t('models.protocol.openai')}</option>
        <option value="anthropic">{$t('models.protocol.anthropic')}</option>
      </select>
    </label>
  {/if}

  <!-- Model -->
  <label class="field">
    <span class="field-label">{$t('models.model')}</span>
    <!-- Use a real <select> dropdown when the provider has a model catalogue,
         so users can see and pick from all available options. Fall back to a
         free-text input for the custom vendor with no preset model list. -->
    {#if preset && preset.models && preset.models.length > 0}
      <select class="field-input mono" bind:value={model} onchange={applyPresetVision} disabled={saving}>
        {#each preset.models as m}
          <option value={m}>{m}</option>
        {/each}
      </select>
    {:else}
      <input
        class="field-input mono"
        type="text"
        placeholder={$t('models.model.placeholder')}
        bind:value={model}
        disabled={saving}
      />
    {/if}
  </label>

  <!-- Base URL -->
  <label class="field">
    <span class="field-label">{$t('models.baseurl')}</span>
    <input
      class="field-input mono"
      type="text"
      placeholder={$t('models.baseurl.placeholder')}
      bind:value={baseUrl}
      readonly={baseUrlLocked}
      disabled={saving}
    />
    {#if variants.length > 0}
      <div class="variants">
        {#each variants as v (v.base_url)}
          <button
            type="button"
            class="variant-chip"
            class:active={baseUrl === v.base_url}
            onclick={() => (baseUrl = v.base_url)}
            disabled={saving}
          >{variantLabel(v)}</button>
        {/each}
      </div>
    {/if}
  </label>

  <!-- API Key -->
  <label class="field">
    <span class="field-label">{$t('models.apikey')}</span>
    <div class="key-row">
      <input
        class="field-input mono"
        type={showKey ? 'text' : 'password'}
        placeholder={keyPlaceholder}
        bind:value={apiKey}
        disabled={saving}
      />
      <button type="button" class="key-toggle" onclick={() => (showKey = !showKey)} title={showKey ? 'Hide' : 'Show'}>
        <iconify-icon icon={showKey ? 'ant-design:eye-invisible-outlined' : 'ant-design:eye-outlined'} width="15"></iconify-icon>
      </button>
    </div>
  </label>

  <!-- Preferences (hidden during onboard — the /onboard ceremony collects them) -->
  {#if showPrefs}
    <div class="prefs">
      <label class="field half">
        <span class="field-label">{$t('models.permission_mode')}</span>
        <select class="field-input" bind:value={permMode} disabled={saving}>
          <option value="interactive">{$t('models.permission_mode.interactive')}</option>
          <option value="auto">{$t('models.permission_mode.auto')}</option>
        </select>
      </label>
      <label class="field half">
        <span class="field-label">{$t('models.reasoning_effort')}</span>
        <select class="field-input" bind:value={reasoning} disabled={saving}>
          <option value="off">{$t('models.reasoning.off')}</option>
          <option value="low">{$t('models.reasoning.low')}</option>
          <option value="medium">{$t('models.reasoning.medium')}</option>
          <option value="high">{$t('models.reasoning.high')}</option>
          <option value="xhigh">{$t('models.reasoning.xhigh')}</option>
          <option value="max">{$t('models.reasoning.max')}</option>
        </select>
      </label>
    </div>

    <div class="toggles">
      <div class="toggle-row">
        <span class="toggle-label">{$t('models.show_reasoning')}</span>
        <Switch bind:checked={showReason} />
      </div>
      <div class="toggle-row">
        <span class="toggle-label">{$t('models.vision')}</span>
        <Switch bind:checked={vision} />
      </div>
    </div>
  {/if}

  <!-- Result -->
  {#if result}
    <div class="result" class:ok={result.ok} class:fail={!result.ok}>
      {result.ok ? '✓' : '✗'} {result.msg}
    </div>
  {/if}

  <!-- Actions -->
  <div class="actions">
    <button class="btn-secondary" onclick={handleTest} disabled={testing || saving}>
      {testing ? $t('models.btn.testing') : $t('models.btn.test')}
    </button>
    <button class="btn-primary" onclick={handleSubmit} disabled={saving || testing}>
      {saving ? $t('models.btn.saving') : submitLabel}
    </button>
  </div>
</div>

<style>
.form { display: flex; flex-direction: column; gap: 16px; }
.field { display: flex; flex-direction: column; gap: 6px; }
.field-label { font-size: 13px; font-weight: 500; color: var(--text-secondary); }
.field-input {
  font-family: inherit; font-size: 14px; color: var(--text);
  border: 1px solid var(--border); border-radius: 8px;
  padding: 8px 11px; outline: none; background: var(--bg-container);
  transition: border-color 0.15s;
}
.field-input:focus { border-color: var(--blue-6); box-shadow: 0 0 0 2px rgba(22,119,255,0.1); }
.field-input[readonly], .field-input:disabled { background: var(--bg-table-header); cursor: not-allowed; }
.field-link { font-size: 12px; color: var(--blue-6); text-decoration: none; align-self: flex-start; }
.field-link:hover { text-decoration: underline; }
.mono { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
.variants { display: flex; flex-wrap: wrap; gap: 6px; }
.variant-chip {
  height: 24px; padding: 0 10px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 999px; font-size: 12px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.variant-chip:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.variant-chip.active { border-color: var(--blue-6); color: var(--blue-6); background: var(--active-blue-bg); }
.key-row { display: flex; align-items: center; gap: 8px; }
.key-row .field-input { flex: 1; min-width: 0; }
.key-toggle {
  width: 34px; height: 36px; flex: 0 0 34px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 8px; display: flex; align-items: center; justify-content: center; cursor: pointer; color: var(--text-tertiary);
}
.key-toggle:hover { border-color: var(--blue-5); color: var(--blue-5); }
.prefs { display: flex; gap: 12px; }
.field.half { flex: 1; min-width: 0; }
.toggles { display: flex; flex-direction: column; border: 1px solid var(--border); border-radius: 8px; overflow: hidden; }
.toggle-row { display: flex; align-items: center; justify-content: space-between; gap: 12px; padding: 11px 12px; }
.toggle-row + .toggle-row { border-top: 1px solid var(--border); }
.toggle-label { font-size: 13px; color: var(--text-secondary); }
.result { font-size: 13px; padding: 8px 12px; border-radius: 8px; }
.result.ok { background: var(--surface-info); color: var(--success); }
.result.fail { background: var(--warning-bg); color: var(--error); }
.actions { display: flex; align-items: center; justify-content: flex-end; gap: 8px; }
.btn-primary {
  height: 34px; padding: 0 16px; border: none; background: var(--blue-6); border-radius: 6px;
  font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.btn-primary:hover:not(:disabled) { background: var(--blue-5); }
.btn-primary:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-secondary {
  height: 34px; padding: 0 16px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 6px; font-size: 13px; color: var(--text-secondary); cursor: pointer; font-family: inherit;
}
.btn-secondary:hover:not(:disabled) { border-color: var(--blue-5); color: var(--blue-5); }
.btn-secondary:disabled { opacity: 0.5; cursor: not-allowed; }
</style>

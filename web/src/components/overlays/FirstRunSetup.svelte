<script lang="ts">
  import { onMount } from 'svelte'
  import { t, locale, setLocale } from '../../lib/i18n'
  import { onboardPhase, openAgentSession, showToast } from '../../lib/stores'
  import * as api from '../../lib/api'
  import type { ProviderPreset, ModelConfigInput, ModelEntry } from '../../lib/api'
  import ModelConfigForm from '../settings/ModelConfigForm.svelte'
  import BrowserSetupForm from '../settings/BrowserSetupForm.svelte'
  import OctoLogo from '../layout/OctoLogo.svelte'

  // Blocking first-run panel shown when no API key is configured (the agent
  // can't run without a key, so the key must be collected natively — not via a
  // chat). Steps: pick language, connect a model, then an optional browser-
  // automation setup. On finish it marks onboarding complete and auto-launches
  // an /onboard chat to personalise soul.md / user.md.
  //
  // Settings → About → "Re-run" opens this same panel on an already-configured
  // install (see rerunFirstRun in SettingsModal). modelInitial seeds the model
  // step from whatever's currently the default endpoint/model + agent defaults,
  // so re-running is "review and confirm" rather than "start from a blank
  // form" — a genuinely first run has no default yet, so it resolves to null
  // and the form behaves exactly as before.

  let step = $state<'lang' | 'model' | 'browser'>('lang')
  let providers = $state<ProviderPreset[]>([])
  let modelInitial = $state<Partial<ModelEntry> | null>(null)
  let lang = $state<'en' | 'zh'>(($locale?.startsWith('zh') ? 'zh' : 'en'))

  onMount(async () => {
    try {
      providers = await api.listProviders()
    } catch {
      /* non-fatal: the form still works with Custom */
    }
    await loadModelInitial()
  })

  async function loadModelInitial() {
    try {
      const [ep, cfg] = await Promise.all([api.getEndpoints(), api.getConfig()])
      const defaultCid = ep.default ?? ''
      const match = ep.endpoints.find(e => defaultCid.startsWith(`${e.id}::`))
      if (!match) return // genuine first run — no default endpoint yet
      const modelName = defaultCid.slice(match.id.length + 2)
      const m = match.models.find(mm => mm.model === modelName)
      modelInitial = {
        provider: match.provider,
        model: modelName,
        base_url: match.base_url ?? '',
        anthropic_format: match.protocol === 'anthropic',
        permission_mode: cfg.permission_mode,
        reasoning_effort: cfg.reasoning_effort,
        show_reasoning: cfg.show_reasoning,
        vision: m?.vision,
      }
    } catch {
      /* non-fatal: form just starts blank, same as before */
    }
  }

  function pickLang(l: 'en' | 'zh') {
    lang = l
    setLocale(l)
  }

  // The model is required, so save it then move on to the optional browser step
  // rather than finishing — the user can set browser up now or skip.
  async function onSubmit(req: ModelConfigInput) {
    await api.saveModel(req)
    step = 'browser'
  }

  // finishOnboard runs after the browser step (set up or skipped): mark
  // onboarding complete and hand off to the personalisation chat. The gate falls
  // through to the normal UI, where ChatView auto-sends the queued /onboard.
  async function finishOnboard() {
    // Persist the language choice so a refresh lands on it.
    await api.updateLanguage(lang).catch(() => {})
    await api.completeOnboard()
    // This IS the first-run auto-launch of /onboard, so record the one-shot
    // nudge marker before opening the chat — otherwise interrupting it and
    // reopening re-fires the soul_setup nudge (this path set phase to '' and
    // never went through maybeLaunchOnboard, so nothing else marked it) (#1660).
    await api.markOnboardAttempted().catch(() => {})
    await openAgentSession(`/onboard lang:${lang}`, '✨ Onboard')
    onboardPhase.set('')
  }
</script>

<div class="setup">
  <div class="card">
    <div class="brand">
      <div class="logo"><OctoLogo size={28} /></div>
      <div class="brand-text">
        <h1>{$t('onboard.title')}</h1>
        <p>{$t('onboard.subtitle')}</p>
      </div>
    </div>

    <div class="steps">
      <span class="ostep" class:on={step === 'lang'} class:done={step !== 'lang'}>1 · {$t('onboard.step.lang')}</span>
      <span class="step-sep"></span>
      <span class="ostep" class:on={step === 'model'} class:done={step === 'browser'}>2 · {$t('onboard.step.model')}</span>
      <span class="step-sep"></span>
      <span class="ostep" class:on={step === 'browser'}>3 · {$t('onboard.step.browser')}</span>
    </div>

    {#if step === 'lang'}
      <p class="prompt">{$t('onboard.lang.prompt')}</p>
      <div class="lang-list">
        <div class="orow" class:sel={lang === 'en'} onclick={() => pickLang('en')}>
          <span class="odot"></span>
          <span>English</span>
        </div>
        <div class="orow" class:sel={lang === 'zh'} onclick={() => pickLang('zh')}>
          <span class="odot"></span>
          <span>简体中文</span>
        </div>
      </div>
      <div class="actions">
        <button class="btn-primary" onclick={() => (step = 'model')}>{$t('onboard.lang.next')}</button>
      </div>
    {:else if step === 'model'}
      <p class="prompt">{$t('onboard.key.title')}</p>
      <ModelConfigForm
        {providers}
        initial={modelInitial}
        requireKey={true}
        showPrefs={false}
        submitLabel={$t('models.btn.test_save')}
        {onSubmit}
      />
      <div class="back-row">
        <button class="link-btn" onclick={() => (step = 'lang')}>{$t('onboard.key.btn.back')}</button>
      </div>
    {:else}
      <p class="prompt">{$t('onboard.browser.prompt')}</p>
      <p class="sub">{$t('onboard.browser.sub')}</p>
      <BrowserSetupForm
        secondaryLabel={$t('onboard.browser.skip')}
        onSecondary={finishOnboard}
        onVerified={finishOnboard}
      />
    {/if}
  </div>
</div>

<style>
.setup {
  position: fixed; inset: 0; z-index: 1000;
  display: flex; align-items: center; justify-content: center;
  background: var(--bg-layout); padding: 24px; overflow-y: auto;
}
.card {
  width: 520px; max-width: 100%;
  background: var(--bg-container); border: 1px solid var(--border); border-radius: var(--radius-card); box-shadow: var(--card-shadow);
  padding: 32px; display: flex; flex-direction: column; gap: 20px;
}
.brand { display: flex; align-items: center; gap: 14px; }
.logo {
  width: 44px; height: 44px; flex: 0 0 44px; border-radius: 12px;
  background: var(--blue-6); color: #fff; display: flex; align-items: center; justify-content: center;
  overflow: hidden;
}
.brand-text h1 { margin: 0; font-size: 20px; font-weight: 600; color: var(--text-heading); }
.brand-text p { margin: 2px 0 0; font-size: 13px; color: var(--text-secondary); }
.steps { display: flex; align-items: center; gap: 10px; }
.ostep {
  font-size: 12px; color: var(--text-secondary); padding: 3px 11px;
  border-radius: 999px; background: var(--hover-neutral); white-space: nowrap;
}
.ostep.on { background: var(--blue-6); color: #fff; font-weight: 600; }
.ostep.done { background: var(--success-bg); color: var(--success-text); }
.step-sep { flex: 1; height: 1px; background: var(--border); max-width: 60px; }
.prompt { margin: 0; font-size: 14px; font-weight: 500; color: var(--text); }
.sub { margin: -12px 0 0; font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.lang-list { display: flex; flex-direction: column; }
.orow {
  display: flex; align-items: center; gap: 10px; padding: 11px 14px;
  border: 1px solid var(--border); border-radius: 10px; font-size: 14px; color: var(--text);
  cursor: pointer; margin-bottom: 8px; background: var(--bg-container);
}
.orow:hover { border-color: var(--text-quaternary); }
.orow.sel { border-color: var(--blue-6); background: var(--active-blue-bg); }
.odot { width: 16px; height: 16px; flex: none; border-radius: 50%; border: 1.5px solid var(--border); }
.orow.sel .odot { border-color: var(--blue-6); background: var(--blue-6); box-shadow: inset 0 0 0 3px var(--bg-container); }
.actions { display: flex; justify-content: flex-end; }
.btn-primary {
  height: 36px; padding: 0 18px; border: none; background: var(--blue-6); border-radius: 8px;
  font-size: 14px; font-weight: 600; color: #fff; cursor: pointer; font-family: inherit;
  box-shadow: 0 1px 2px rgba(0,122,255,0.35);
}
.btn-primary:hover { background: var(--blue-5); }
.back-row { display: flex; justify-content: flex-start; }
.link-btn { border: none; background: transparent; color: var(--text-tertiary); font-size: 13px; cursor: pointer; font-family: inherit; padding: 0; }
.link-btn:hover { color: var(--blue-6); }
</style>

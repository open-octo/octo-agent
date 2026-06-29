<script lang="ts">
  import { onMount } from 'svelte'
  import { t, locale, setLocale } from '../../lib/i18n'
  import { onboardPhase, openAgentSession, showToast } from '../../lib/stores'
  import * as api from '../../lib/api'
  import type { ProviderPreset, ModelConfigInput } from '../../lib/api'
  import ModelConfigForm from '../settings/ModelConfigForm.svelte'
  import BrowserSetupForm from '../settings/BrowserSetupForm.svelte'
  import OctoLogo from '../layout/OctoLogo.svelte'

  // Blocking first-run panel shown when no API key is configured (the agent
  // can't run without a key, so the key must be collected natively — not via a
  // chat). Steps: pick language, connect a model, then an optional browser-
  // automation setup. On finish it marks onboarding complete and auto-launches
  // an /onboard chat to personalise soul.md / user.md.

  let step = $state<'lang' | 'model' | 'browser'>('lang')
  let providers = $state<ProviderPreset[]>([])
  let lang = $state<'en' | 'zh'>(($locale?.startsWith('zh') ? 'zh' : 'en'))

  onMount(async () => {
    try {
      providers = await api.listProviders()
    } catch {
      /* non-fatal: the form still works with Custom */
    }
  })

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
    await api.completeOnboard()
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
      <span class="step" class:active={step === 'lang'} class:done={step !== 'lang'}>1 · {$t('onboard.step.lang')}</span>
      <span class="step-sep"></span>
      <span class="step" class:active={step === 'model'} class:done={step === 'browser'}>2 · {$t('onboard.step.model')}</span>
      <span class="step-sep"></span>
      <span class="step" class:active={step === 'browser'}>3 · {$t('onboard.step.browser')}</span>
    </div>

    {#if step === 'lang'}
      <p class="prompt">{$t('onboard.lang.prompt')}</p>
      <div class="lang-row">
        <button class="lang-btn" class:active={lang === 'en'} onclick={() => pickLang('en')}>English</button>
        <button class="lang-btn" class:active={lang === 'zh'} onclick={() => pickLang('zh')}>简体中文</button>
      </div>
      <div class="actions">
        <button class="btn-primary" onclick={() => (step = 'model')}>{$t('onboard.lang.next')}</button>
      </div>
    {:else if step === 'model'}
      <p class="prompt">{$t('onboard.key.title')}</p>
      <ModelConfigForm
        {providers}
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
  background: var(--bg-container); border-radius: 16px; box-shadow: var(--card-shadow);
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
.step { font-size: 12px; color: var(--text-tertiary); }
.step.active { color: var(--blue-6); font-weight: 600; }
.step.done { color: var(--success); }
.step-sep { flex: 1; height: 1px; background: var(--border); max-width: 60px; }
.prompt { margin: 0; font-size: 14px; font-weight: 500; color: var(--text); }
.sub { margin: -12px 0 0; font-size: 13px; color: var(--text-secondary); line-height: 1.5; }
.lang-row { display: flex; gap: 12px; }
.lang-btn {
  flex: 1; height: 44px; border: 1px solid var(--border); background: var(--bg-container);
  border-radius: 10px; font-size: 14px; color: var(--text); cursor: pointer; font-family: inherit;
}
.lang-btn:hover { border-color: var(--blue-5); }
.lang-btn.active { border-color: var(--blue-6); color: var(--blue-6); background: var(--active-blue-bg); }
.actions { display: flex; justify-content: flex-end; }
.btn-primary {
  height: 36px; padding: 0 18px; border: none; background: var(--blue-6); border-radius: 8px;
  font-size: 14px; color: #fff; cursor: pointer; font-family: inherit;
}
.btn-primary:hover { background: var(--blue-5); }
.back-row { display: flex; justify-content: flex-start; }
.link-btn { border: none; background: transparent; color: var(--text-tertiary); font-size: 13px; cursor: pointer; font-family: inherit; padding: 0; }
.link-btn:hover { color: var(--blue-6); }
</style>

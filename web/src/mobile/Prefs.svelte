<script lang="ts">
  // Preferences (appearance, language) and about (version): the tail of the
  // Me tab's root (ConfigTab). Appearance switches the app-wide data-theme so
  // it also flips the mobile UI's dark tokens live; language is this device's
  // own (language.ts). There is no notification switch:
  // the app's WebView has no Notification API and the app has no push.
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import { t } from '../lib/i18n'
  import * as api from '../lib/api'
  import { getLang, pinLang, followServer, type LangChoice } from './language'

  let mode = $state<ThemeMode>(getMode())
  const themes: { m: ThemeMode; key: string }[] = [
    { m: 'light', key: 'm.theme_light' },
    { m: 'dark', key: 'm.theme_dark' },
    { m: 'system', key: 'm.theme_system' },
  ]
  function pickTheme(m: ThemeMode) {
    mode = m
    setMode(m)
  }

  // Language names stay in their own language, as on the desktop's picker.
  let lang = $state<LangChoice>(getLang())
  const langs: { c: LangChoice; label: string }[] = [
    { c: 'server', label: '' },
    { c: 'en', label: 'English' },
    { c: 'zh', label: '简体中文' },
  ]
  function pickLang(c: LangChoice) {
    lang = c
    if (c === 'server') followServer(() => api.getConfig().then(cfg => cfg.language ?? ''))
    else pinLang(c)
  }

  let version = $state('')
  $effect(() => {
    fetch('/api/version')
      .then(r => r.json())
      .then(d => { version = d.current ?? (d.version ?? '').replace(/^v/, '') })
      .catch(() => {})
  })
</script>

<p class="lbl">{$t('m.prefs')}</p>
<div class="card group">
  <div class="row col">
    <span class="rlabel">{$t('m.appearance')}</span>
    <div class="seg">
      {#each themes as th (th.m)}
        <button class="segi" class:on={mode === th.m} aria-pressed={mode === th.m} onclick={() => pickTheme(th.m)}>{$t(th.key)}</button>
      {/each}
    </div>
  </div>
  <div class="row col">
    <span class="rlabel">{$t('settings.language')}</span>
    <div class="seg">
      {#each langs as l (l.c)}
        <button class="segi" class:on={lang === l.c} aria-pressed={lang === l.c} onclick={() => pickLang(l.c)}>{l.label || $t('m.lang_server')}</button>
      {/each}
    </div>
  </div>
</div>

<p class="lbl">{$t('m.about')}</p>
<div class="card group">
  <div class="row">
    <span class="rlabel">{$t('m.about_octo')}</span>
    <span class="rval mono">{version ? `v${version}` : ''}</span>
  </div>
</div>

<style>
  .card { background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card); }
  .lbl { margin: 2px 2px 8px; font: 600 12px/1 system-ui; letter-spacing: .5px; text-transform: uppercase; color: var(--m-text-3); }
  .group { overflow: hidden; margin-bottom: 14px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 14px 16px; }
  .row.col { flex-direction: column; align-items: stretch; gap: 10px; }
  .row + .row { border-top: 1px solid var(--m-divider); }
  .rlabel { flex: 1; font-size: 14.5px; color: var(--m-text); }
  .rval { flex: none; font-size: 12px; color: var(--m-text-3); }
  .mono { font-family: ui-monospace, Menlo, monospace; }

  .seg { display: flex; gap: 4px; background: var(--m-bg); border-radius: 8px; padding: 3px; }
  .segi {
    flex: 1; text-align: center; padding: 7px 0; border-radius: 6px; border: none;
    background: none; font-size: 13px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .segi.on { background: var(--m-accent); color: #fff; font-weight: 600; }
</style>

<script lang="ts">
  import { onMount } from 'svelte'
  import Segment from '../components/ui/Segment.svelte'
  import Switch from '../components/ui/Switch.svelte'
  import { showToast, activeSessionId } from '../lib/stores'
  import { setLocale, t, tr } from '../lib/i18n'
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import * as api from '../lib/api'

  // --- local state ---
  let language      = $state('en')
  let fontSize      = $state('Medium')
  let theme         = $state('Light')
  let model         = $state('')
  let modelOptions  = $state<string[]>([])
  let reasoning     = $state('Medium')
  let permMode      = $state('Ask')
  let workdir       = $state('')
  let desktopNotif  = $state(true)
  let failureNotif  = $state(true)
  let versionStr    = $state('')
  let saving        = $state(false)
  let loading       = $state(true)

  // Original values for dirty-checking
  let origModel   = ''
  let origWorkdir = ''

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
    await Promise.all([loadConfig(), loadVersion()])
  })

  async function loadConfig() {
    loading = true
    try {
      const cfg = await api.getConfig() as any
      // models list
      const ms: any[] = cfg.models ?? []
      modelOptions = ms.map((m: any) => m.model ?? m.id)
      const defaultIdx = cfg.default_model_idx ?? 0
      const def = ms[defaultIdx]
      if (def) {
        model = def.model ?? def.id ?? ''
        reasoning = capitalize(def.reasoning_effort ?? 'medium')
        permMode  = capitalize(def.permission_mode ?? 'ask')
      }
      origModel = model
      // Font size is a client-only preference (persisted in localStorage); the
      // server only hardcodes a placeholder, so don't let it clobber the saved
      // choice. Language still seeds from config when present.
      if (cfg.language)   language = cfg.language
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

  async function handleSave() {
    saving = true
    const sid = $activeSessionId
    try {
      if (sid) {
        const promises: Promise<void>[] = []
        if (model !== origModel) {
          promises.push(api.updateSessionModel(sid, model))
        }
        if (workdir !== origWorkdir) {
          promises.push(api.updateSessionWorkingDir(sid, workdir))
        }
        const effortMap: Record<string, string> = { Low: 'low', Medium: 'medium', High: 'high' }
        promises.push(api.updateSessionReasoningEffort(sid, effortMap[reasoning] ?? 'medium'))
        await Promise.all(promises)
        origModel   = model
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
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.default_model')}</span>
            <span class="setting-desc">{$t('settings.default_model_desc')}</span>
          </div>
          {#if modelOptions.length > 0}
            <select class="sel" bind:value={model}>
              {#each modelOptions as o}<option value={o}>{o}</option>{/each}
            </select>
          {:else}
            <input class="input" bind:value={model} placeholder="e.g. claude-sonnet-4-5" />
          {/if}
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.reasoning')}</span>
            <span class="setting-desc">{$t('settings.reasoning_desc')}</span>
          </div>
          <Segment options={['Low', 'Medium', 'High']} labels={{ Low: $t('settings.re_low'), Medium: $t('settings.re_medium'), High: $t('settings.re_high') }} bind:value={reasoning} />
        </div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.perm_mode')}</span>
            <span class="setting-desc">{$t('settings.perm_mode_desc')}</span>
          </div>
          <Segment options={['Ask', 'Auto']} labels={{ Ask: $t('settings.pm_ask'), Auto: $t('settings.pm_auto') }} bind:value={permMode} />
        </div>
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.workdir')}</span>
            <span class="setting-desc">{$t('settings.workdir_desc')}</span>
          </div>
          <input class="input" bind:value={workdir} placeholder="~/code/my-project" />
        </div>
      </div>

      <!-- Notifications -->
      <div class="section-card">
        <div class="section-title">{$t('settings.notifications')}</div>
        <div class="setting-row">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.desktop_notif')}</span>
            <span class="setting-desc">{$t('settings.desktop_notif_desc')}</span>
          </div>
          <Switch bind:checked={desktopNotif} />
        </div>
        <div class="setting-row last">
          <div class="setting-info">
            <span class="setting-label">{$t('settings.failure_notif')}</span>
            <span class="setting-desc">{$t('settings.failure_notif_desc')}</span>
          </div>
          <Switch bind:checked={failureNotif} />
        </div>
      </div>

      <!-- Save -->
      <div class="save-row">
        <button class="btn-primary" onclick={handleSave} disabled={saving}>
          {saving ? $t('settings.saving') : $t('settings.save_changes')}
        </button>
        {#if versionStr}
          <span class="version-badge">{$t('common.version')} {versionStr}</span>
        {/if}
      </div>
    {/if}
  </div>
</div>

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
</style>

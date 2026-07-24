<script lang="ts">
  // Settings tab: device (connection state), preferences (notifications,
  // appearance), and about (version). Reuses the shared theme + notification
  // stores; appearance switches the app-wide data-theme so it also flips the
  // mobile UI's dark tokens live.
  import { notificationsEnabled, setNotificationsEnabled } from '../lib/notifications'
  import { getMode, setMode, type ThemeMode } from '../lib/theme'
  import { wsState } from '../lib/ws'
  import { t } from '../lib/i18n'

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

  let version = $state('')
  $effect(() => {
    fetch('/api/version')
      .then(r => r.json())
      .then(d => { version = d.current ?? (d.version ?? '').replace(/^v/, '') })
      .catch(() => {})
  })
</script>

<header class="head"><h1>{$t('m.tab_settings')}</h1></header>

<div class="scroll">
  <div class="card device">
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="var(--m-accent)" stroke-width="2"><rect x="2" y="4" width="20" height="13" rx="2"/><path d="M8 21h8"/></svg>
    <span class="dname">{$t('m.dev_connected')}</span>
    <span class="dot" class:live={$wsState === 'connected'}></span>
  </div>

  <p class="lbl">{$t('m.prefs')}</p>
  <div class="card group">
    <div class="row">
      <span class="rlabel">{$t('m.notifications')}</span>
      <button
        class="switch"
        class:on={$notificationsEnabled}
        role="switch"
        aria-checked={$notificationsEnabled}
        aria-label={$t('m.notifications')}
        onclick={() => setNotificationsEnabled(!$notificationsEnabled)}
      ><span class="knob"></span></button>
    </div>
    <div class="row col">
      <span class="rlabel">{$t('m.appearance')}</span>
      <div class="seg">
        {#each themes as th (th.m)}
          <button class="segi" class:on={mode === th.m} aria-pressed={mode === th.m} onclick={() => pickTheme(th.m)}>{$t(th.key)}</button>
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
</div>

<style>
  .head { flex: none; padding: 8px 18px 12px; }
  .head h1 { margin: 0; font-size: 24px; font-weight: 600; color: var(--m-text-strong); }
  .scroll { flex: 1; min-height: 0; overflow-y: auto; -webkit-overflow-scrolling: touch; padding: 0 16px 20px; }
  .card { background: var(--m-surface); border-radius: 14px; box-shadow: var(--m-shadow-card); }
  .device { display: flex; align-items: center; gap: 10px; padding: 14px 16px; margin-bottom: 14px; }
  .device .dname { flex: 1; font-size: 14.5px; font-weight: 600; color: var(--m-text); }
  .device .dot { width: 8px; height: 8px; border-radius: 50%; background: var(--m-text-4); }
  .device .dot.live { background: var(--m-success); }
  .lbl { margin: 2px 2px 8px; font: 600 12px/1 system-ui; letter-spacing: .5px; text-transform: uppercase; color: var(--m-text-3); }
  .group { overflow: hidden; margin-bottom: 20px; }
  .row { display: flex; align-items: center; gap: 12px; padding: 14px 16px; }
  .row + .row { border-top: 1px solid var(--m-divider); }
  .row.col { flex-direction: column; align-items: stretch; gap: 10px; }
  .rlabel { flex: 1; font-size: 14.5px; color: var(--m-text); }
  .rval { flex: none; font-size: 12px; color: var(--m-text-3); }
  .mono { font-family: ui-monospace, Menlo, monospace; }

  .switch {
    flex: none; width: 42px; height: 24px; border-radius: 9999px; border: none;
    padding: 0; position: relative; cursor: pointer; background: var(--m-border);
    transition: background .15s;
  }
  .switch.on { background: var(--m-accent); }
  .switch .knob {
    position: absolute; top: 2px; left: 2px; width: 20px; height: 20px; border-radius: 50%;
    background: #fff; box-shadow: 0 1px 2px rgba(0,0,0,.2); transition: left .15s;
  }
  .switch.on .knob { left: 20px; }

  .seg { display: flex; gap: 4px; background: var(--m-bg); border-radius: 8px; padding: 3px; }
  .segi {
    flex: 1; text-align: center; padding: 7px 0; border-radius: 6px; border: none;
    background: none; font-size: 13px; color: var(--m-text-2); font-family: inherit; cursor: pointer;
  }
  .segi.on { background: var(--m-accent); color: #fff; font-weight: 600; }
</style>
